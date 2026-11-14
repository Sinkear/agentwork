package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GitCodeClient is the GitCode (gitcode.com) issue API adapter (M4-B):
// api.gitcode.com/api/v5, access_token query auth. The provider implements
// the same Provider surface as GitHub, so the poller/closer/comment paths
// are provider-agnostic.
type GitCodeClient struct {
	token   string
	http    *http.Client
	baseURL string
}

func NewGitCodeClient(token string) *GitCodeClient {
	return &GitCodeClient{token: token, http: &http.Client{Timeout: 30 * time.Second}, baseURL: "https://api.gitcode.com/api/v5"}
}

func (c *GitCodeClient) do(ctx context.Context, method, path string, form url.Values, out any) error {
	var rdr io.Reader
	if form != nil {
		rdr = strings.NewReader(form.Encode())
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	u := c.baseURL + path + sep + "access_token=" + url.QueryEscape(c.token)
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("gitcode %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// gitCodeIssue is GitCode's issue wire shape — its number is a STRING
// ("1"), unlike GitHub's int (the provider difference the adapter exists
// for). Parsed into the common Issue with an int number.
type gitCodeIssue struct {
	Number  string `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
}

// ListOpenIssues returns the repo's open issues (GitCode issues have no PR
// rows — merge requests are a separate collection — so no PR filtering).
func (c *GitCodeClient) ListOpenIssues(ctx context.Context, repo string) ([]Issue, error) {
	var raw []gitCodeIssue
	err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/issues?state=open&per_page=100", nil, &raw)
	if err != nil {
		return nil, err
	}
	out := []Issue{}
	for _, i := range raw {
		n, err := strconv.Atoi(i.Number)
		if err != nil {
			continue // a malformed number is not an issue we can track
		}
		out = append(out, Issue{Number: n, Title: i.Title, Body: i.Body, HTMLURL: i.HTMLURL})
	}
	return out, nil
}

// gitCodeComment mirrors GitCode's comment shape (id is a string too).
type gitCodeComment struct {
	ID   string `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (c *GitCodeClient) ListComments(ctx context.Context, repo string, number int) ([]Comment, error) {
	var raw []gitCodeComment
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100", repo, number), nil, &raw)
	if err != nil {
		return nil, err
	}
	out := []Comment{}
	for _, cm := range raw {
		id, _ := strconv.Atoi(cm.ID)
		out = append(out, Comment{ID: id, Body: cm.Body, User: struct {
			Login string `json:"login"`
		}{Login: cm.User.Login}})
	}
	return out, nil
}

func (c *GitCodeClient) CreateComment(ctx context.Context, repo string, number int, body string) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, number),
		url.Values{"body": {body}}, nil)
}

// CloseIssue: GitCode closes via PATCH /repos/{owner}/issues/{number} with
// formData (repo, number, state="close") — note the path carries owner but
// NOT the repo segment.
func (c *GitCodeClient) CloseIssue(ctx context.Context, repo string, number int) error {
	owner, repoName, ok := strings.Cut(repo, "/")
	if !ok {
		return fmt.Errorf("gitcode: bad repo %q (want owner/repo)", repo)
	}
	return c.do(ctx, http.MethodPatch, "/repos/"+owner+"/issues/"+strconv.Itoa(number),
		url.Values{"repo": {repoName}, "number": {strconv.Itoa(number)}, "state": {"close"}}, nil)
}

// CommitURL is the GitCode commit link (verified: /commit/<sha> resolves).
func (c *GitCodeClient) CommitURL(repo, sha string) string {
	return "https://gitcode.com/" + repo + "/commit/" + sha
}

// ── Pull-request surface (PR pull) ──

// flexInt decodes a JSON number OR a JSON string holding digits (GitCode's
// wire shapes have varied across revisions: the issue number is a STRING,
// the documented PR number an INTEGER — accept both instead of betting on
// one).
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("flexInt: %q: %w", s, err)
	}
	*f = flexInt(n)
	return nil
}

// gitCodePull is GitCode's PR wire shape (minimal): head carries the source
// branch + SHA, base the target branch. A draft PR (draft=true) is skipped
// by the poller — a draft is not a work request yet.
type gitCodePull struct {
	Number  flexInt `json:"number"`
	Title   string  `json:"title"`
	Body    string  `json:"body"`
	HTMLURL string  `json:"html_url"`
	Draft   bool    `json:"draft"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

// ListOpenPulls returns the repo's open PRs. GitCode keeps pull requests in
// a separate /pulls collection (issues and PRs are separate sequences — PRs
// never appear in the issues list). Draft PRs are excluded: a draft is not
// ready for review.
func (c *GitCodeClient) ListOpenPulls(ctx context.Context, repo string) ([]Pull, error) {
	var raw []gitCodePull
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/pulls?state=open&per_page=100", nil, &raw); err != nil {
		return nil, err
	}
	out := []Pull{}
	for _, pr := range raw {
		if pr.Draft {
			continue
		}
		out = append(out, Pull{
			Number: int(pr.Number), Title: pr.Title, Body: pr.Body, HTMLURL: pr.HTMLURL,
			Author: pr.User.Login, SourceBranch: pr.Head.Ref, TargetBranch: pr.Base.Ref,
			HeadSHA: pr.Head.SHA,
		})
	}
	return out, nil
}

// ListPullComments returns the PR's comments. GitCode serves PR dialogue
// through a DEDICATED /pulls/:n/comments endpoint — the issues endpoint
// does NOT cover PR comments (the provider difference this surface exists
// for).
func (c *GitCodeClient) ListPullComments(ctx context.Context, repo string, number int) ([]Comment, error) {
	var raw []gitCodeComment
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d/comments?per_page=100", repo, number), nil, &raw); err != nil {
		return nil, err
	}
	out := []Comment{}
	for _, cm := range raw {
		id, _ := strconv.Atoi(cm.ID)
		out = append(out, Comment{ID: id, Body: cm.Body, User: struct {
			Login string `json:"login"`
		}{Login: cm.User.Login}})
	}
	return out, nil
}

// CreatePullComment posts a comment on the PR (the PR comments endpoint).
func (c *GitCodeClient) CreatePullComment(ctx context.Context, repo string, number int, body string) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/comments", repo, number),
		url.Values{"body": {body}}, nil)
}

// gitCodePermission is GitCode's repo permission object. The wire shape has
// varied between revisions (permission vs permissions, bool-valued push),
// so both wrappers are decoded and either is accepted.
type gitCodePermission struct {
	Push  bool `json:"push"`
	Admin bool `json:"admin"`
}

func (p *gitCodePermission) allowsPush() bool { return p != nil && (p.Push || p.Admin) }

// CreatePull opens a pull request against the repo (the skill sync PR
// submission path). GitCode's response names the URL html_url or web_url
// and the number number or iid — all tolerated.
func (c *GitCodeClient) CreatePull(ctx context.Context, repo, title, head, base, body string) (string, int, error) {
	form := url.Values{"title": {title}, "head": {head}, "base": {base}, "body": {body}}
	var out struct {
		HTMLURL string  `json:"html_url"`
		WebURL  string  `json:"web_url"`
		Number  flexInt `json:"number"`
		IID     flexInt `json:"iid"`
	}
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo+"/pulls", form, &out); err != nil {
		return "", 0, err
	}
	u := out.HTMLURL
	if u == "" {
		u = out.WebURL
	}
	n := int(out.Number)
	if n == 0 {
		n = int(out.IID)
	}
	return u, n, nil
}

// gitCodePermissionAllowsPush maps GitCode's named permission to the push
// capability. The collaborator endpoint reports "read"/"write"/"admin".
func gitCodePermissionAllowsPush(permission string) bool {
	switch strings.ToLower(strings.TrimSpace(permission)) {
	case "write", "admin", "maintainer", "owner":
		return true
	}
	return false
}

// CheckPushPermission reports the token identity and whether it can push to
// the repo. GitCode's repo object does NOT carry a permission block for the
// caller (verified against api.gitcode.com: /repos/{repo} has no
// permission/permissions field), so the authoritative probe is the
// GitHub-compatible collaborator permission endpoint
// /repos/{repo}/collaborators/{login}/permission, which reports
// permission:"write" + access_level:30 for Developers. Older/self-hosted
// shapes that DO embed permission on the repo (or only list collaborators)
// are accepted as fallbacks. identity is returned even when a lookup fails.
func (c *GitCodeClient) CheckPushPermission(ctx context.Context, repo string) (string, bool, error) {
	var user struct {
		Login    string `json:"login"`
		Username string `json:"username"`
	}
	if err := c.do(ctx, http.MethodGet, "/user", nil, &user); err != nil {
		return "", false, err
	}
	identity := user.Login
	if identity == "" {
		identity = user.Username
	}
	// 1) Collaborator permission endpoint: an explicit answer wins.
	if identity != "" {
		var cp struct {
			Permission  string             `json:"permission"`
			AccessLevel flexInt            `json:"access_level"`
			Permissions *gitCodePermission `json:"permissions"`
		}
		if err := c.do(ctx, http.MethodGet,
			"/repos/"+repo+"/collaborators/"+url.PathEscape(identity)+"/permission", nil, &cp); err == nil {
			if cp.Permissions.allowsPush() || gitCodePermissionAllowsPush(cp.Permission) || int(cp.AccessLevel) >= 30 {
				return identity, true, nil
			}
			if cp.Permission != "" || cp.AccessLevel != 0 {
				return identity, false, nil
			}
			// Empty body: fall through to the repo/collaborator probes.
		}
	}
	// 2) Repo-level permission (the shape some GitCode revisions used).
	var r struct {
		Permission  *gitCodePermission `json:"permission"`
		Permissions *gitCodePermission `json:"permissions"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo, nil, &r); err != nil {
		return identity, false, err
	}
	if r.Permission.allowsPush() || r.Permissions.allowsPush() {
		return identity, true, nil
	}
	// 3) Collaborator roster: the repo object lists members but no caller
	// permission, so match the token identity in the roster.
	var members []struct {
		Login       string             `json:"login"`
		Username    string             `json:"username"`
		Permission  string             `json:"permission"`
		AccessLevel flexInt            `json:"access_level"`
		Permissions *gitCodePermission `json:"permissions"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/collaborators", nil, &members); err == nil {
		for _, m := range members {
			if m.Login != identity && m.Username != identity {
				continue
			}
			return identity, m.Permissions.allowsPush() ||
				gitCodePermissionAllowsPush(m.Permission) || int(m.AccessLevel) >= 30, nil
		}
	}
	return identity, false, nil
}
