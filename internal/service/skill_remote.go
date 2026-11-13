package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/eushing/agentwork/internal/gitutil"
)

// ── remote source model ──

// SkillSource is the remote git repository binding of a skill. The git
// token is carried in Token for server-side clone/push calls only — it is
// NEVER serialized (json:"-"); the API exposes HasToken instead.
type SkillSource struct {
	RepoURL        string `json:"repo_url"`
	Provider       string `json:"provider"`
	Branch         string `json:"branch"`
	Path           string `json:"path"`
	HasToken       bool   `json:"has_token"`
	LastSyncedAt   string `json:"last_synced_at"`
	LastSyncAction string `json:"last_sync_action"`
	LastRemoteSHA  string `json:"last_remote_sha"`
	Token          string `json:"-"`
}

// newSkillSource builds the API-visible source from one skill row. It
// returns nil when no repo is configured (the JSON field is omitted).
func newSkillSource(repoURL, provider, branch, path, token, lastSyncedAt, lastSyncAction, lastRemoteSHA string) *SkillSource {
	if strings.TrimSpace(repoURL) == "" {
		return nil
	}
	return &SkillSource{
		RepoURL:        repoURL,
		Provider:       provider,
		Branch:         branch,
		Path:           path,
		HasToken:       strings.TrimSpace(token) != "",
		LastSyncedAt:   lastSyncedAt,
		LastSyncAction: lastSyncAction,
		LastRemoteSHA:  lastRemoteSHA,
		Token:          token,
	}
}

// SkillSourceInput is the PUT /skills/{id}/source request body.
type SkillSourceInput struct {
	RepoURL  string `json:"repo_url"`
	Provider string `json:"provider"`
	Branch   string `json:"branch"`
	Path     string `json:"path"`
	Token    string `json:"token"`
}

// PullProvider is the LOCAL structural interface the skill service needs
// from an issue provider. It deliberately mirrors the two primitive methods
// of issue.Provider (CreatePull / CheckPushPermission) so issue.NewProvider
// satisfies it structurally without service importing issue.
type PullProvider interface {
	CreatePull(ctx context.Context, repo, title, head, base, body string) (url string, number int, err error)
	CheckPushPermission(ctx context.Context, repo string) (identity string, canPush bool, err error)
}

// remoteGit is the injectable git surface: tests inject a fake so skill sync
// never touches the network or a real git binary.
type remoteGit interface {
	Fetch(ctx context.Context, cfg remoteConfig) (*remoteState, error)
	Push(ctx context.Context, cfg remoteConfig, files map[string]string, branch, message string) (commit string, err error)
}

// remoteState is one fetched remote branch.
type remoteState struct {
	SHA   string
	Files map[string]string // relative to the skill's repo path (package.zip/.git excluded)
	Dir   string            // temp clone dir (valid until Cleanup)
	// Cleanup removes the temp clone dir. May be nil (fakes).
	Cleanup func()
}

// remoteConfig is the resolved git/API configuration for one skill source.
type remoteConfig struct {
	RepoURL  string
	Provider string
	Branch   string
	Path     string
	Token    string
}

// slug returns the repo identifier the issue providers use ("owner/repo").
func (c remoteConfig) slug() string { return skillRepoSlug(c.RepoURL) }

// SetPullProviderFactory wires the issue-provider factory (server.go does
// this with issue.NewProvider; tests inject a fake).
func (s *SkillService) SetPullProviderFactory(f func(provider, token string) (PullProvider, error)) {
	s.pullProviderFactory = f
}

// SetRemoteGit replaces the git CLI implementation (tests inject a fake).
func (s *SkillService) SetRemoteGit(g remoteGit) {
	if g != nil {
		s.git = g
	}
}

// ── source configuration ──

// SetSource binds (or updates) the skill's remote repository source. An
// empty token keeps the previously stored one (single-user convenience);
// binding a DIFFERENT repo resets the previous sync bookkeeping.
func (s *SkillService) SetSource(ctx context.Context, id string, in SkillSourceInput) (*Skill, error) {
	sk, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	src, err := normalizeSourceInput(in, sk.Source)
	if err != nil {
		return nil, err
	}
	lastSyncedAt, lastSyncAction, lastRemoteSHA := "", "", ""
	if sk.Source != nil && sk.Source.RepoURL == src.RepoURL {
		lastSyncedAt = sk.Source.LastSyncedAt
		lastSyncAction = sk.Source.LastSyncAction
		lastRemoteSHA = sk.Source.LastRemoteSHA
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`UPDATE skill SET repo_url=?,repo_provider=?,repo_branch=?,repo_path=?,repo_token=?,last_synced_at=?,last_sync_action=?,last_remote_sha=? WHERE id=?`,
		src.RepoURL, src.Provider, src.Branch, src.Path, src.Token,
		lastSyncedAt, lastSyncAction, lastRemoteSHA, id); err != nil {
		return nil, fmt.Errorf("update skill source: %w", err)
	}
	return s.Get(ctx, id)
}

// ClearSource removes the remote repository binding (including the token
// and the sync bookkeeping); the skill stays local-only.
func (s *SkillService) ClearSource(ctx context.Context, id string) (*Skill, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`UPDATE skill SET repo_url='',repo_provider='',repo_branch='',repo_path='',repo_token='',last_synced_at='',last_sync_action='',last_remote_sha='' WHERE id=?`,
		id); err != nil {
		return nil, fmt.Errorf("clear skill source: %w", err)
	}
	return s.Get(ctx, id)
}

// normalizeSourceInput validates a PUT source body and fills defaults.
func normalizeSourceInput(in SkillSourceInput, prev *SkillSource) (*SkillSource, error) {
	rawURL := strings.TrimSpace(in.RepoURL)
	if rawURL == "" {
		return nil, NewValidationError("repo_url is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, NewValidationError("repo_url must be an http(s) URL")
	}
	if skillRepoSlug(rawURL) == "" {
		return nil, NewValidationError("repo_url must point at a repository (https://host/owner/repo)")
	}
	provider := strings.ToLower(strings.TrimSpace(in.Provider))
	if provider == "" {
		provider = providerFromHost(u.Hostname())
	}
	if provider != "github" && provider != "gitcode" {
		return nil, NewValidationError(fmt.Sprintf("unknown repo provider %q (github|gitcode)", provider))
	}
	branch := strings.TrimSpace(in.Branch)
	if branch == "" {
		branch = "main"
	}
	if strings.ContainsAny(branch, " \t\r\n") {
		return nil, NewValidationError(fmt.Sprintf("invalid branch name %q", branch))
	}
	repoPath, err := normalizeRepoPath(in.Path)
	if err != nil {
		return nil, NewValidationError(err.Error())
	}
	token := strings.TrimSpace(in.Token)
	if token == "" && prev != nil {
		token = prev.Token
	}
	return &SkillSource{
		RepoURL:  rawURL,
		Provider: provider,
		Branch:   branch,
		Path:     repoPath,
		HasToken: token != "",
		Token:    token,
	}, nil
}

// providerFromHost derives the git platform from the URL host.
func providerFromHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch {
	case host == "github.com" || strings.HasSuffix(host, ".github.com"):
		return "github"
	case host == "gitcode.com" || strings.HasSuffix(host, ".gitcode.com"):
		return "gitcode"
	default:
		return ""
	}
}

// skillRepoSlug extracts "owner/repo" from a repository URL. Empty when the URL
// does not carry at least an owner and a repo name.
func skillRepoSlug(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	p := strings.Trim(strings.TrimSuffix(u.Path, "/"), "/")
	p = strings.TrimSuffix(p, ".git")
	if p == "" || strings.Contains(p, "..") {
		return ""
	}
	parts := strings.Split(p, "/")
	if len(parts) < 2 {
		return ""
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return ""
		}
	}
	return p
}

// normalizeRepoPath cleans a repo-relative subdirectory. Empty means the
// repo root. Absolute paths and ".." segments are rejected.
func normalizeRepoPath(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.Trim(p, "/")
	if p == "" {
		return "", nil
	}
	clean := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(p)), "/")
	if clean == "." || clean == "" {
		return "", nil
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid repo path %q: must be relative to the repo root", p)
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return "", fmt.Errorf("invalid repo path %q: must not contain ..", p)
		}
	}
	return clean, nil
}

// remoteConfigFor resolves the git config for a bound skill.
func remoteConfigFor(sk *Skill) (remoteConfig, error) {
	if sk.Source == nil || strings.TrimSpace(sk.Source.RepoURL) == "" {
		return remoteConfig{}, NewValidationError("skill has no remote repository source configured")
	}
	branch := sk.Source.Branch
	if branch == "" {
		branch = "main"
	}
	return remoteConfig{
		RepoURL:  sk.Source.RepoURL,
		Provider: sk.Source.Provider,
		Branch:   branch,
		Path:     sk.Source.Path,
		Token:    sk.Source.Token,
	}, nil
}

// provider builds the issue provider for the configured platform.
func (s *SkillService) provider(cfg remoteConfig) (PullProvider, error) {
	if s.pullProviderFactory == nil {
		return nil, errors.New("pull provider is not configured")
	}
	p, err := s.pullProviderFactory(cfg.Provider, cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("build %s provider: %w", cfg.Provider, err)
	}
	if p == nil {
		return nil, errors.New("pull provider is not configured")
	}
	return p, nil
}

// probePush reports identity/can_push without failing the read-only compare:
// an API error simply leaves can_push=false.
func (s *SkillService) probePush(ctx context.Context, cfg remoteConfig) (string, bool) {
	if strings.TrimSpace(cfg.Token) == "" {
		return "", false
	}
	p, err := s.provider(cfg)
	if err != nil {
		return "", false
	}
	identity, canPush, _ := p.CheckPushPermission(ctx, cfg.slug())
	return identity, canPush
}

// requirePush is the POST-path gate: the configured token identity MUST have
// push/PR permission, else ErrForbidden (HTTP 403).
func (s *SkillService) requirePush(ctx context.Context, cfg remoteConfig) (PullProvider, string, error) {
	p, err := s.provider(cfg)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, "", fmt.Errorf("%w: no git token configured for the skill source; cannot verify push permission", ErrForbidden)
	}
	identity, canPush, err := p.CheckPushPermission(ctx, cfg.slug())
	if err != nil {
		return nil, identity, fmt.Errorf("check push permission: %w", err)
	}
	if !canPush {
		return nil, identity, fmt.Errorf("%w: identity %q does not have push permission on %s", ErrForbidden, identity, cfg.slug())
	}
	return p, identity, nil
}

// ── compare / pull / push / PR ──

// SyncEntry is one differing file between the local skill and the remote
// repo path. Status uses the PULL viewpoint: added = only in remote (pull
// creates it), removed = only local (pull deletes it), modified = both but
// different content.
type SyncEntry struct {
	Path         string `json:"path"`
	Status       string `json:"status"`
	LocalSHA256  string `json:"local_sha256"`
	RemoteSHA256 string `json:"remote_sha256"`
}

// SyncCounts summarizes a compare.
type SyncCounts struct {
	Added     int `json:"added"`
	Modified  int `json:"modified"`
	Removed   int `json:"removed"`
	Unchanged int `json:"unchanged"`
}

// SyncCompare is the GET /skills/{id}/sync response.
type SyncCompare struct {
	SkillID    string      `json:"skill_id"`
	Identity   string      `json:"identity"`
	CanPush    bool        `json:"can_push"`
	State      string      `json:"state"`
	BaseBranch string      `json:"base_branch"`
	RemoteSHA  string      `json:"remote_sha"`
	Entries    []SyncEntry `json:"entries"`
	Counts     SyncCounts  `json:"counts"`
	CheckedAt  string      `json:"checked_at"`
}

// SyncResult is the POST /skills/{id}/sync response (pull or push).
type SyncResult struct {
	SkillID   string `json:"skill_id"`
	Action    string `json:"action"`
	Branch    string `json:"branch,omitempty"`
	Commit    string `json:"commit,omitempty"`
	RemoteSHA string `json:"remote_sha,omitempty"`
	Identity  string `json:"identity"`
}

// PullRequestResult is the POST /skills/{id}/pull-request response.
type PullRequestResult struct {
	PRURL    string `json:"pr_url"`
	PRNumber int    `json:"pr_number"`
	Branch   string `json:"branch"`
	Base     string `json:"base"`
	Identity string `json:"identity"`
	Commit   string `json:"commit"`
}

// Compare diffs the local skill files against the configured remote repo
// path. It is read-only: it never fails because the identity lacks push
// permission — it reports can_push instead.
func (s *SkillService) Compare(ctx context.Context, id string) (*SyncCompare, error) {
	sk, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg, err := remoteConfigFor(sk)
	if err != nil {
		return nil, err
	}
	local, err := s.localSkillFiles(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load local skill files: %w", err)
	}
	rs, err := s.git.Fetch(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("fetch remote repository: %w", err)
	}
	if rs.Cleanup != nil {
		defer rs.Cleanup()
	}
	remote := cleanRemoteFiles(rs.Files)
	entries, counts := diffSkillFiles(local, remote)
	identity, canPush := s.probePush(ctx, cfg)
	return &SyncCompare{
		SkillID:    sk.ID,
		Identity:   identity,
		CanPush:    canPush,
		State:      syncState(counts),
		BaseBranch: cfg.Branch,
		RemoteSHA:  rs.SHA,
		Entries:    entries,
		Counts:     counts,
		CheckedAt:  now(),
	}, nil
}

// Sync performs a pull (replace local files with remote) or push (commit
// local files to the remote branch). Both directions verify that the
// configured token identity can push — otherwise ErrForbidden.
func (s *SkillService) Sync(ctx context.Context, id, direction string) (*SyncResult, error) {
	sk, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	direction = strings.ToLower(strings.TrimSpace(direction))
	if direction != "pull" && direction != "push" {
		return nil, NewValidationError(`direction must be "pull" or "push"`)
	}
	cfg, err := remoteConfigFor(sk)
	if err != nil {
		return nil, err
	}
	if _, identity, err := s.requirePush(ctx, cfg); err != nil {
		return nil, err
	} else if direction == "pull" {
		return s.pullRemote(ctx, sk, cfg, identity)
	} else {
		return s.pushRemote(ctx, sk, cfg, identity)
	}
}

// PullRequest clones the remote, applies the local skill files on a fresh
// branch, pushes it and opens a PR against the configured branch. Requires
// push permission; a no-diff state is a validation error.
func (s *SkillService) PullRequest(ctx context.Context, id, title, body string) (*PullRequestResult, error) {
	sk, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg, err := remoteConfigFor(sk)
	if err != nil {
		return nil, err
	}
	p, identity, err := s.requirePush(ctx, cfg)
	if err != nil {
		return nil, err
	}
	local, err := s.localSkillFiles(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load local skill files: %w", err)
	}
	rs, err := s.git.Fetch(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("fetch remote repository: %w", err)
	}
	if rs.Cleanup != nil {
		defer rs.Cleanup()
	}
	remote := cleanRemoteFiles(rs.Files)
	if _, counts := diffSkillFiles(local, remote); counts.Added+counts.Modified+counts.Removed == 0 {
		return nil, NewValidationError("no local changes to submit as a pull request")
	}
	branch := fmt.Sprintf("agentwork/skill-%s-%d", firstID8(sk.ID), time.Now().Unix())
	if strings.TrimSpace(title) == "" {
		title = fmt.Sprintf("Update skill %s", sk.Name)
	}
	commit, err := s.git.Push(ctx, cfg, local, branch, fmt.Sprintf("agentwork: update skill %s", sk.Name))
	if err != nil {
		return nil, fmt.Errorf("push pull-request branch: %w", err)
	}
	prURL, prNumber, err := p.CreatePull(ctx, cfg.slug(), title, branch, cfg.Branch, body)
	if err != nil {
		return nil, fmt.Errorf("create pull request: %w", err)
	}
	if err := s.markSynced(ctx, sk.ID, "pr", commit); err != nil {
		return nil, err
	}
	return &PullRequestResult{
		PRURL:    prURL,
		PRNumber: prNumber,
		Branch:   branch,
		Base:     cfg.Branch,
		Identity: identity,
		Commit:   commit,
	}, nil
}

func (s *SkillService) pullRemote(ctx context.Context, sk *Skill, cfg remoteConfig, identity string) (*SyncResult, error) {
	rs, err := s.git.Fetch(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("fetch remote repository: %w", err)
	}
	if rs.Cleanup != nil {
		defer rs.Cleanup()
	}
	files := cleanRemoteFiles(rs.Files)
	if len(files) == 0 {
		where := cfg.Path
		if where == "" {
			where = "the repository root"
		}
		return nil, NewValidationError(fmt.Sprintf("remote has no skill files at %s", where))
	}
	// Replace the local skill dir wholesale: source config lives in SQLite,
	// so wiping the files is safe. writeSkillFilesAndArchive rewrites the
	// files and regenerates package.zip.
	if err := os.RemoveAll(skillDir(sk.ID)); err != nil {
		return nil, fmt.Errorf("clear local skill files: %w", err)
	}
	if err := s.writeSkillFilesAndArchive(sk.ID, files); err != nil {
		return nil, err
	}
	if err := s.markSynced(ctx, sk.ID, "pulled", rs.SHA); err != nil {
		return nil, err
	}
	return &SyncResult{
		SkillID:   sk.ID,
		Action:    "pulled",
		Branch:    cfg.Branch,
		RemoteSHA: rs.SHA,
		Identity:  identity,
	}, nil
}

func (s *SkillService) pushRemote(ctx context.Context, sk *Skill, cfg remoteConfig, identity string) (*SyncResult, error) {
	local, err := s.localSkillFiles(ctx, sk.ID)
	if err != nil {
		return nil, fmt.Errorf("load local skill files: %w", err)
	}
	if len(local) == 0 {
		return nil, NewValidationError("skill has no files to push")
	}
	rs, err := s.git.Fetch(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("fetch remote repository: %w", err)
	}
	if rs.Cleanup != nil {
		defer rs.Cleanup()
	}
	remote := cleanRemoteFiles(rs.Files)
	if _, counts := diffSkillFiles(local, remote); counts.Added+counts.Modified+counts.Removed == 0 {
		return nil, NewValidationError("no local changes to push")
	}
	commit, err := s.git.Push(ctx, cfg, local, cfg.Branch, fmt.Sprintf("agentwork: sync skill %s", sk.Name))
	if err != nil {
		return nil, fmt.Errorf("push to remote: %w", err)
	}
	if err := s.markSynced(ctx, sk.ID, "pushed", commit); err != nil {
		return nil, err
	}
	return &SyncResult{
		SkillID:   sk.ID,
		Action:    "pushed",
		Branch:    cfg.Branch,
		Commit:    commit,
		RemoteSHA: commit,
		Identity:  identity,
	}, nil
}

// markSynced records the outcome of a sync/PR on the skill row.
func (s *SkillService) markSynced(ctx context.Context, id, action, sha string) error {
	if _, err := s.st.DB().ExecContext(ctx,
		`UPDATE skill SET last_synced_at=?,last_sync_action=?,last_remote_sha=? WHERE id=?`,
		now(), action, sha, id); err != nil {
		return fmt.Errorf("update skill sync state: %w", err)
	}
	return nil
}

// localSkillFiles returns the skill's own files (package.zip and .git
// excluded) — the comparison/push unit.
func (s *SkillService) localSkillFiles(ctx context.Context, id string) (map[string]string, error) {
	files, err := s.Files(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(files))
	for p, content := range files {
		rel := strings.TrimPrefix(filepath.ToSlash(p), "./")
		if ignoredSkillFile(rel) {
			continue
		}
		out[rel] = content
	}
	return out, nil
}

// ignoredSkillFile: the local build artifact (package.zip) and git metadata
// are never part of the remote content.
func ignoredSkillFile(rel string) bool {
	rel = strings.TrimPrefix(rel, "./")
	if rel == "" || rel == "package.zip" {
		return true
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".git" {
			return true
		}
	}
	return false
}

// cleanRemoteFiles filters the remote file map to the skill's own files and
// normalizes path separators. Unsafe (absolute/"..") paths are dropped.
func cleanRemoteFiles(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for p, content := range in {
		rel := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(p)), "./")
		if ignoredSkillFile(rel) {
			continue
		}
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
			continue
		}
		out[rel] = content
	}
	return out
}

// diffSkillFiles computes hash-based entries + counts for the union of
// paths, from the PULL viewpoint: `added` is a file only the remote has
// (pull creates it), `removed` is a file only the local has (pull deletes
// it), `modified` is a path both have with different content. The result is
// sorted by path for stable API output.
func diffSkillFiles(local, remote map[string]string) ([]SyncEntry, SyncCounts) {
	seen := make(map[string]bool, len(local)+len(remote))
	paths := make([]string, 0, len(local)+len(remote))
	for p := range local {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for p := range remote {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	entries := []SyncEntry{}
	counts := SyncCounts{}
	for _, p := range paths {
		lc, lok := local[p]
		rc, rok := remote[p]
		var lh, rh string
		if lok {
			lh = sha256Hex(lc)
		}
		if rok {
			rh = sha256Hex(rc)
		}
		status := ""
		switch {
		case lok && rok && lh == rh:
			counts.Unchanged++
			continue
		case !lok && rok:
			status = "added"
			counts.Added++
		case lok && !rok:
			status = "removed"
			counts.Removed++
		default:
			status = "modified"
			counts.Modified++
		}
		entries = append(entries, SyncEntry{Path: p, Status: status, LocalSHA256: lh, RemoteSHA256: rh})
	}
	return entries, counts
}

// syncState applies the contract heuristic from the pull viewpoint: `removed`
// (local-only) + `modified` are local changes, `added` (remote-only) are
// remote changes. Only local changes → local_ahead; only remote changes →
// remote_ahead; both → diverged; none → in_sync.
func syncState(counts SyncCounts) string {
	localChanges := counts.Removed + counts.Modified
	remoteChanges := counts.Added
	switch {
	case localChanges == 0 && remoteChanges == 0:
		return "in_sync"
	case localChanges > 0 && remoteChanges == 0:
		return "local_ahead"
	case localChanges == 0 && remoteChanges > 0:
		return "remote_ahead"
	default:
		return "diverged"
	}
}

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// firstID8 is the PR branch's skill-id fragment.
func firstID8(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// ── real git CLI implementation ──

const gitCommandTimeout = 120 * time.Second

// gitCLI is the production remoteGit: shallow clone/fetch + commit + push
// through the real git binary. Credentials are embedded with
// gitutil.CloneURL and stripped from error output with gitutil.SanitizeURL.
type gitCLI struct{}

func (g *gitCLI) git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), gitutil.SanitizeURL(msg))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Fetch shallow-clones the configured branch into a temp dir and returns the
// skill files under the configured repo path.
func (g *gitCLI) Fetch(ctx context.Context, cfg remoteConfig) (*remoteState, error) {
	dir, err := os.MkdirTemp("", "agentwork-skill-fetch-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	cloneURL := gitutil.CloneURL(cfg.RepoURL, cfg.Token)
	if _, err := g.git(ctx, "", "clone", "--depth", "1", "--branch", cfg.Branch, cloneURL, dir); err != nil {
		cleanup()
		return nil, err
	}
	sha, err := g.git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		cleanup()
		return nil, err
	}
	root, err := repoSubdir(dir, cfg.Path)
	if err != nil {
		cleanup()
		return nil, err
	}
	files, err := readRepoFiles(root)
	if err != nil {
		cleanup()
		return nil, err
	}
	return &remoteState{SHA: sha, Files: files, Dir: dir, Cleanup: cleanup}, nil
}

// Push clones the configured base branch, writes files under the repo path,
// commits and pushes to the requested branch (the base branch for a direct
// push, a fresh branch for a PR).
func (g *gitCLI) Push(ctx context.Context, cfg remoteConfig, files map[string]string, branch, message string) (string, error) {
	if strings.TrimSpace(branch) == "" {
		return "", NewValidationError("branch is required")
	}
	if strings.HasPrefix(branch, "-") || strings.ContainsAny(branch, " \t\r\n") {
		return "", NewValidationError(fmt.Sprintf("invalid branch name %q", branch))
	}
	repoPath, err := normalizeRepoPath(cfg.Path)
	if err != nil {
		return "", NewValidationError(err.Error())
	}
	dir, err := os.MkdirTemp("", "agentwork-skill-push-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)
	cloneURL := gitutil.CloneURL(cfg.RepoURL, cfg.Token)
	if _, err := g.git(ctx, "", "clone", "--depth", "1", "--branch", cfg.Branch, cloneURL, dir); err != nil {
		return "", err
	}
	if branch != cfg.Branch {
		if _, err := g.git(ctx, dir, "checkout", "-b", branch); err != nil {
			return "", err
		}
	}
	root, err := repoSubdir(dir, repoPath)
	if err != nil {
		return "", err
	}
	if repoPath == "" {
		// Skill files live at the repo root: clear everything except .git.
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", err
		}
		for _, e := range entries {
			if e.Name() == ".git" {
				continue
			}
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return "", err
			}
		}
	} else if err := os.RemoveAll(root); err != nil {
		return "", err
	}
	if err := writeRepoFiles(root, files); err != nil {
		return "", err
	}
	if _, err := g.git(ctx, dir, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := g.git(ctx, dir, "-c", "user.name=agentwork", "-c", "user.email=agentwork@localhost", "commit", "-m", message); err != nil {
		return "", err
	}
	if _, err := g.git(ctx, dir, "push", "origin", "HEAD:refs/heads/"+branch); err != nil {
		return "", err
	}
	return g.git(ctx, dir, "rev-parse", "HEAD")
}

// repoSubdir resolves the configured repo path inside the clone root,
// rejecting traversal outside the repository.
func repoSubdir(root, repoPath string) (string, error) {
	if repoPath == "" {
		return root, nil
	}
	clean := filepath.Clean(filepath.FromSlash(repoPath))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe repo path %q", repoPath)
	}
	target := filepath.Join(root, clean)
	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe repo path %q", repoPath)
	}
	return target, nil
}

// readRepoFiles walks one directory of a clone into a path→content map.
func readRepoFiles(root string) (map[string]string, error) {
	out := map[string]string{}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("repo path %q is not a directory", root)
	}
	err = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			if fi.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if ignoredSkillFile(rel) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// writeRepoFiles writes a skill file map under root, rejecting unsafe paths
// and writing in sorted order for deterministic commits.
func writeRepoFiles(root string, files map[string]string) error {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		clean := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(rel, "./")))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return NewValidationError(fmt.Sprintf("invalid skill file path %q", rel))
		}
		target := filepath.Join(root, clean)
		if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
			return NewValidationError(fmt.Sprintf("unsafe skill file path %q", rel))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, []byte(files[rel]), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}
