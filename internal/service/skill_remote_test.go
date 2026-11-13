package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/eushing/agentwork/internal/store"
	"github.com/eushing/agentwork/internal/zipx"
)

// ── fakes: no network / no git binary in these tests ──

type fakeRemoteGit struct {
	files         map[string]string
	sha           string
	commit        string
	fetchErr      error
	pushErr       error
	fetches       int
	pushes        int
	pushedFiles   map[string]string
	pushedBranch  string
	pushedMessage string
	pushedToken   string
}

func copyFiles(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (f *fakeRemoteGit) Fetch(_ context.Context, cfg remoteConfig) (*remoteState, error) {
	f.fetches++
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return &remoteState{SHA: f.sha, Files: copyFiles(f.files)}, nil
}

func (f *fakeRemoteGit) Push(_ context.Context, cfg remoteConfig, files map[string]string, branch, message string) (string, error) {
	f.pushes++
	f.pushedFiles = copyFiles(files)
	f.pushedBranch = branch
	f.pushedMessage = message
	f.pushedToken = cfg.Token
	if f.pushErr != nil {
		return "", f.pushErr
	}
	return f.commit, nil
}

type fakePullProvider struct {
	identity  string
	canPush   bool
	permErr   error
	prURL     string
	prNumber  int
	prErr     error
	permCalls int
	prCalls   int
	gotRepo   string
	gotTitle  string
	gotHead   string
	gotBase   string
	gotBody   string
}

func (p *fakePullProvider) CreatePull(_ context.Context, repo, title, head, base, body string) (string, int, error) {
	p.prCalls++
	p.gotRepo, p.gotTitle, p.gotHead, p.gotBase, p.gotBody = repo, title, head, base, body
	if p.prErr != nil {
		return "", 0, p.prErr
	}
	return p.prURL, p.prNumber, nil
}

func (p *fakePullProvider) CheckPushPermission(_ context.Context, repo string) (string, bool, error) {
	p.permCalls++
	p.gotRepo = repo
	if p.permErr != nil {
		return p.identity, false, p.permErr
	}
	return p.identity, p.canPush, nil
}

// remoteTestHarness builds an isolated skill service (temp AGENTWORK_HOME)
// with one skill and fake git/provider surfaces.
func remoteTestHarness(t *testing.T, local map[string]string) (*SkillService, *Skill, *fakeRemoteGit, *fakePullProvider) {
	t.Helper()
	t.Setenv("AGENTWORK_HOME", t.TempDir())
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkillService(st)
	git := &fakeRemoteGit{sha: "remote-sha-1", commit: "commit-1", files: map[string]string{}}
	prov := &fakePullProvider{identity: "alice", canPush: true, prURL: "https://github.com/acme/skills/pull/7", prNumber: 7}
	svc.SetRemoteGit(git)
	svc.SetPullProviderFactory(func(name, token string) (PullProvider, error) {
		return prov, nil
	})
	zipData, err := zipx.Build(local)
	if err != nil {
		t.Fatal(err)
	}
	sk, err := svc.CreateFromZip(context.Background(), zipData)
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	return svc, sk, git, prov
}

func mustSetSource(t *testing.T, svc *SkillService, id string, in SkillSourceInput) *Skill {
	t.Helper()
	sk, err := svc.SetSource(context.Background(), id, in)
	if err != nil {
		t.Fatalf("SetSource: %v", err)
	}
	return sk
}

// ── source configuration ──

func TestSkillSourceConfig(t *testing.T) {
	ctx := context.Background()
	svc, sk, _, _ := remoteTestHarness(t, map[string]string{
		"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nbody\n",
	})

	// Configure: provider derived from host, branch defaults to main.
	got := mustSetSource(t, svc, sk.ID, SkillSourceInput{
		RepoURL: "https://github.com/acme/skills",
		Token:   "secret-token",
	})
	if got.Source == nil {
		t.Fatal("source must be exposed after PUT")
	}
	if got.Source.Provider != "github" || got.Source.Branch != "main" || got.Source.Path != "" {
		t.Fatalf("derived source = %+v", got.Source)
	}
	if !got.Source.HasToken || got.Source.Token != "secret-token" {
		t.Fatalf("token config = has_token=%v token=%q", got.Source.HasToken, got.Source.Token)
	}

	// The serialized skill never carries the token; it exposes has_token.
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"token"`) || strings.Contains(string(raw), "secret-token") {
		t.Fatalf("skill JSON leaks the token: %s", raw)
	}
	if !strings.Contains(string(raw), `"has_token":true`) {
		t.Fatalf("skill JSON must expose has_token: %s", raw)
	}

	// Service list exposes the same source object.
	list, err := svc.List(ctx)
	if err != nil || len(list) != 1 || list[0].Source == nil || list[0].Source.RepoURL != "https://github.com/acme/skills" {
		t.Fatalf("List = %+v, %v", list, err)
	}

	// Prior sync bookkeeping survives a re-PUT with an empty token (token
	// kept) on the SAME repo.
	if err := svc.markSynced(ctx, sk.ID, "pulled", "abc123"); err != nil {
		t.Fatal(err)
	}
	got = mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills"})
	if got.Source.Token != "secret-token" || got.Source.LastSyncAction != "pulled" || got.Source.LastRemoteSHA != "abc123" {
		t.Fatalf("same-repo update = %+v", got.Source)
	}

	// A different repo resets the sync bookkeeping and re-derives the host.
	got = mustSetSource(t, svc, sk.ID, SkillSourceInput{
		RepoURL: "https://gitcode.com/acme/other",
		Token:   "t2",
	})
	if got.Source.Provider != "gitcode" || got.Source.LastSyncAction != "" || got.Source.LastRemoteSHA != "" || got.Source.LastSyncedAt != "" {
		t.Fatalf("different-repo update = %+v", got.Source)
	}

	// Validation: bad URL / unknown provider.
	if _, err := svc.SetSource(ctx, sk.ID, SkillSourceInput{RepoURL: "ftp://acme/skills"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad scheme err = %v, want ErrValidation", err)
	}
	if _, err := svc.SetSource(ctx, sk.ID, SkillSourceInput{RepoURL: "https://example.com/acme/skills", Provider: "bitbucket"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown provider err = %v, want ErrValidation", err)
	}

	// Delete clears the binding and the token; source is omitted from JSON.
	got, err = svc.ClearSource(ctx, sk.ID)
	if err != nil {
		t.Fatalf("ClearSource: %v", err)
	}
	if got.Source != nil {
		t.Fatalf("source after DELETE = %+v, want nil", got.Source)
	}
	raw, _ = json.Marshal(got)
	if strings.Contains(string(raw), `"source"`) {
		t.Fatalf("cleared skill JSON still has source: %s", raw)
	}
	var token string
	if err := svc.st.DB().QueryRowContext(ctx, `SELECT repo_token FROM skill WHERE id=?`, sk.ID).Scan(&token); err != nil || token != "" {
		t.Fatalf("stored token after clear = %q, %v", token, err)
	}
}

// ── compare ──

// TestDiffSkillFilesPullViewpoint pins the contract/frontend semantics:
// added = remote-only, removed = local-only, modified = both differ;
// local changes drive local_ahead, remote changes drive remote_ahead.
func TestDiffSkillFilesPullViewpoint(t *testing.T) {
	entries, counts := diffSkillFiles(
		map[string]string{"SKILL.md": "local", "local-only.md": "x"},
		map[string]string{"SKILL.md": "remote", "remote-only.md": "y"},
	)
	byPath := map[string]SyncEntry{}
	for _, e := range entries {
		byPath[e.Path] = e
	}
	if byPath["remote-only.md"].Status != "added" || byPath["remote-only.md"].RemoteSHA256 == "" || byPath["remote-only.md"].LocalSHA256 != "" {
		t.Fatalf("remote-only entry = %+v", byPath["remote-only.md"])
	}
	if byPath["local-only.md"].Status != "removed" || byPath["local-only.md"].LocalSHA256 == "" || byPath["local-only.md"].RemoteSHA256 != "" {
		t.Fatalf("local-only entry = %+v", byPath["local-only.md"])
	}
	if byPath["SKILL.md"].Status != "modified" || byPath["SKILL.md"].LocalSHA256 == byPath["SKILL.md"].RemoteSHA256 {
		t.Fatalf("modified entry = %+v", byPath["SKILL.md"])
	}
	if counts.Added != 1 || counts.Removed != 1 || counts.Modified != 1 || counts.Unchanged != 0 {
		t.Fatalf("counts = %+v", counts)
	}
	if got := syncState(SyncCounts{Removed: 1, Modified: 1}); got != "local_ahead" {
		t.Fatalf("local-only state = %q", got)
	}
	if got := syncState(SyncCounts{Added: 1}); got != "remote_ahead" {
		t.Fatalf("remote-only state = %q", got)
	}
	if got := syncState(SyncCounts{Added: 1, Removed: 1}); got != "diverged" {
		t.Fatalf("diverged state = %q", got)
	}
	if got := syncState(SyncCounts{}); got != "in_sync" {
		t.Fatalf("in-sync state = %q", got)
	}
}

func TestSkillCompareStates(t *testing.T) {
	const skillMD = "---\nname: sync-skill\ndescription: demo\n---\nbody\n"
	ctx := context.Background()

	cases := []struct {
		name       string
		local      map[string]string
		remote     map[string]string
		wantState  string
		wantCounts SyncCounts
		wantStatus map[string]string
	}{
		{
			name:       "in sync",
			local:      map[string]string{"SKILL.md": skillMD},
			remote:     map[string]string{"SKILL.md": skillMD, "package.zip": "ignored"},
			wantState:  "in_sync",
			wantCounts: SyncCounts{Unchanged: 1},
		},
		{
			name:       "local modified SKILL.md",
			local:      map[string]string{"SKILL.md": skillMD + "local edit\n"},
			remote:     map[string]string{"SKILL.md": skillMD},
			wantState:  "local_ahead",
			wantCounts: SyncCounts{Modified: 1},
			wantStatus: map[string]string{"SKILL.md": "modified"},
		},
		{
			name:       "local-only extra file",
			local:      map[string]string{"SKILL.md": skillMD, "local-only.md": "mine"},
			remote:     map[string]string{"SKILL.md": skillMD},
			wantState:  "local_ahead",
			wantCounts: SyncCounts{Removed: 1, Unchanged: 1},
			wantStatus: map[string]string{"local-only.md": "removed"},
		},
		{
			name:       "remote-only file",
			local:      map[string]string{"SKILL.md": skillMD},
			remote:     map[string]string{"SKILL.md": skillMD, "remote-only.md": "theirs"},
			wantState:  "remote_ahead",
			wantCounts: SyncCounts{Added: 1, Unchanged: 1},
			wantStatus: map[string]string{"remote-only.md": "added"},
		},
		{
			name:       "diverged",
			local:      map[string]string{"SKILL.md": skillMD + "local edit\n", "local-only.md": "mine"},
			remote:     map[string]string{"SKILL.md": skillMD, "remote-only.md": "theirs"},
			wantState:  "diverged",
			wantCounts: SyncCounts{Added: 1, Removed: 1, Modified: 1},
			wantStatus: map[string]string{"SKILL.md": "modified", "local-only.md": "removed", "remote-only.md": "added"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, sk, git, prov := remoteTestHarness(t, c.local)
			git.files = c.remote
			prov.canPush = true
			mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})

			cmp, err := svc.Compare(ctx, sk.ID)
			if err != nil {
				t.Fatalf("Compare: %v", err)
			}
			if cmp.State != c.wantState || cmp.Counts != c.wantCounts {
				t.Fatalf("Compare state/counts = %q %+v, want %q %+v", cmp.State, cmp.Counts, c.wantState, c.wantCounts)
			}
			if cmp.Identity != "alice" || !cmp.CanPush {
				t.Fatalf("identity/can_push = %q %v", cmp.Identity, cmp.CanPush)
			}
			if cmp.BaseBranch != "main" || cmp.RemoteSHA != "remote-sha-1" || cmp.CheckedAt == "" {
				t.Fatalf("compare metadata = %+v", cmp)
			}
			statuses := map[string]string{}
			for _, e := range cmp.Entries {
				statuses[e.Path] = e.Status
				if e.Path == "package.zip" {
					t.Fatalf("package.zip must be excluded from the diff: %+v", cmp.Entries)
				}
			}
			for p, want := range c.wantStatus {
				if statuses[p] != want {
					t.Fatalf("entry %q status = %q, want %q (entries %+v)", p, statuses[p], want, cmp.Entries)
				}
			}
			if len(statuses) != len(c.wantStatus) {
				t.Fatalf("entries = %+v, want %d entries", cmp.Entries, len(c.wantStatus))
			}
		})
	}
}

// TestSkillCompareReportsNoPermission: the GET compare is read-only and
// reports can_push=false instead of failing.
func TestSkillCompareReportsNoPermission(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: s\n---\n"})
	git.files = map[string]string{"SKILL.md": "---\nname: s\n---\n"}
	prov.canPush = false
	prov.identity = "viewer"
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})

	cmp, err := svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatalf("Compare must not fail on missing push permission: %v", err)
	}
	if cmp.CanPush || cmp.Identity != "viewer" || cmp.State != "in_sync" {
		t.Fatalf("compare = %+v", cmp)
	}
}

// ── pull ──

func TestSkillSyncPull(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{
		"SKILL.md":  "---\nname: sync-skill\ndescription: demo\n---\nold\n",
		"stale.txt": "remove me",
	})
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok", Path: "skills/demo"})
	prov.canPush = true

	git.files = map[string]string{
		"SKILL.md":      "---\nname: sync-skill\ndescription: demo\n---\nnew remote body\n",
		"sub/helper.md": "helper\n",
		"package.zip":   "remote artifact must be ignored",
	}

	out, err := svc.Sync(ctx, sk.ID, "pull")
	if err != nil {
		t.Fatalf("Sync pull: %v", err)
	}
	if out.Action != "pulled" || out.Identity != "alice" || out.RemoteSHA != "remote-sha-1" || out.Branch != "main" {
		t.Fatalf("pull result = %+v", out)
	}

	files, err := svc.Files(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["stale.txt"]; ok {
		t.Fatalf("pull must delete local-only files: %v", files)
	}
	if files["SKILL.md"] != git.files["SKILL.md"] || files["sub/helper.md"] != "helper\n" {
		t.Fatalf("pull must replace local content: %v", files)
	}

	// package.zip is regenerated from the fetched files (not copied from the
	// remote) and must contain the new layout.
	archive, err := svc.PackageZip(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := zipxEntries(archive)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e] = true
	}
	if !got["SKILL.md"] || !got["sub/helper.md"] || got["package.zip"] || got["stale.txt"] {
		t.Fatalf("package.zip entries after pull = %v", entries)
	}

	gotSkill, err := svc.Get(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotSkill.Source.LastSyncAction != "pulled" || gotSkill.Source.LastRemoteSHA != "remote-sha-1" || gotSkill.Source.LastSyncedAt == "" {
		t.Fatalf("last_* after pull = %+v", gotSkill.Source)
	}
	if git.pushes != 0 {
		t.Fatalf("pull must not push, pushes=%d", git.pushes)
	}
}

// ── push / permission ──

func TestSkillSyncRequiresPushPermission(t *testing.T) {
	ctx := context.Background()
	for _, direction := range []string{"pull", "push"} {
		t.Run(direction, func(t *testing.T) {
			svc, sk, git, prov := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: s\n---\nlocal\n"})
			git.files = map[string]string{"SKILL.md": "---\nname: s\n---\nremote\n"}
			prov.canPush = false
			prov.identity = "viewer"
			mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})

			_, err := svc.Sync(ctx, sk.ID, direction)
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("%s err = %v, want ErrForbidden", direction, err)
			}
			if git.pushes != 0 || git.fetches != 0 {
				t.Fatalf("%s must stop before git work (pushes=%d fetches=%d)", direction, git.pushes, git.fetches)
			}
		})
	}
}

func TestSkillSyncPush(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{
		"SKILL.md": "---\nname: sync-skill\n---\nlocal change\n",
	})
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})
	prov.canPush = true
	git.files = map[string]string{"SKILL.md": "---\nname: sync-skill\n---\nold remote\n"}

	out, err := svc.Sync(ctx, sk.ID, "push")
	if err != nil {
		t.Fatalf("Sync push: %v", err)
	}
	if out.Action != "pushed" || out.Commit != "commit-1" || out.RemoteSHA != "commit-1" ||
		out.Branch != "main" || out.Identity != "alice" {
		t.Fatalf("push result = %+v", out)
	}
	if git.pushes != 1 || git.pushedBranch != "main" {
		t.Fatalf("push calls = %d branch=%q", git.pushes, git.pushedBranch)
	}
	if git.pushedFiles["SKILL.md"] != "---\nname: sync-skill\n---\nlocal change\n" {
		t.Fatalf("pushed files = %v", git.pushedFiles)
	}
	if _, ok := git.pushedFiles["package.zip"]; ok {
		t.Fatalf("push must not send package.zip: %v", git.pushedFiles)
	}
	if !strings.Contains(git.pushedMessage, "sync skill") {
		t.Fatalf("push message = %q", git.pushedMessage)
	}

	gotSkill, err := svc.Get(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotSkill.Source.LastSyncAction != "pushed" || gotSkill.Source.LastRemoteSHA != "commit-1" {
		t.Fatalf("last_* after push = %+v", gotSkill.Source)
	}

	// No local changes → 400 validation, and no second push.
	git.files = map[string]string{"SKILL.md": "---\nname: sync-skill\n---\nlocal change\n"}
	if _, err := svc.Sync(ctx, sk.ID, "push"); !errors.Is(err, ErrValidation) {
		t.Fatalf("no-change push err = %v, want ErrValidation", err)
	}
	if git.pushes != 1 {
		t.Fatalf("no-change push contacted git, pushes=%d", git.pushes)
	}

	// Unknown direction is a validation error.
	if _, err := svc.Sync(ctx, sk.ID, "sideways"); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad direction err = %v, want ErrValidation", err)
	}
}

// ── pull request ──

func TestSkillPullRequest(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{
		"SKILL.md": "---\nname: sync-skill\n---\nlocal change\n",
	})
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})
	prov.canPush = true
	prov.prURL = "https://github.com/acme/skills/pull/7"
	prov.prNumber = 7
	git.files = map[string]string{"SKILL.md": "---\nname: sync-skill\n---\nold remote\n"}

	out, err := svc.PullRequest(ctx, sk.ID, "Update skill", "why")
	if err != nil {
		t.Fatalf("PullRequest: %v", err)
	}
	if out.PRURL != prov.prURL || out.PRNumber != 7 || out.Base != "main" || out.Identity != "alice" || out.Commit != "commit-1" {
		t.Fatalf("PR result = %+v", out)
	}
	wantBranch := "agentwork/skill-" + firstID8(sk.ID) + "-"
	if !strings.HasPrefix(out.Branch, wantBranch) {
		t.Fatalf("PR branch = %q, want prefix %q", out.Branch, wantBranch)
	}
	if git.pushes != 1 || git.pushedBranch != out.Branch {
		t.Fatalf("PR push = %d branch=%q (want %q)", git.pushes, git.pushedBranch, out.Branch)
	}
	if prov.prCalls != 1 || prov.gotRepo != "acme/skills" || prov.gotHead != out.Branch ||
		prov.gotBase != "main" || prov.gotTitle != "Update skill" || prov.gotBody != "why" {
		t.Fatalf("CreatePull args = repo=%q title=%q head=%q base=%q body=%q", prov.gotRepo, prov.gotTitle, prov.gotHead, prov.gotBase, prov.gotBody)
	}
	gotSkill, err := svc.Get(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotSkill.Source.LastSyncAction != "pr" || gotSkill.Source.LastRemoteSHA != "commit-1" {
		t.Fatalf("last_* after PR = %+v", gotSkill.Source)
	}

	// No changes → validation error, no branch push / PR call.
	git.files = map[string]string{"SKILL.md": "---\nname: sync-skill\n---\nlocal change\n"}
	if _, err := svc.PullRequest(ctx, sk.ID, "", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("no-change PR err = %v, want ErrValidation", err)
	}
	if git.pushes != 1 || prov.prCalls != 1 {
		t.Fatalf("no-change PR contacted git/provider: pushes=%d prCalls=%d", git.pushes, prov.prCalls)
	}
}

// TestSkillPullRequestForbidden: PR submission is gated by the same push
// permission check as sync.
func TestSkillPullRequestForbidden(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: s\n---\nlocal\n"})
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})
	prov.canPush = false
	prov.identity = "viewer"
	git.files = map[string]string{"SKILL.md": "---\nname: s\n---\nremote\n"}

	_, err := svc.PullRequest(ctx, sk.ID, "", "")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("PR err = %v, want ErrForbidden", err)
	}
	if git.pushes != 0 || prov.prCalls != 0 {
		t.Fatalf("forbidden PR must not push/open: pushes=%d prCalls=%d", git.pushes, prov.prCalls)
	}
}

// TestSkillSyncUnconfigured: operations on a local-only skill are validation
// errors (no remote to talk to).
func TestSkillSyncUnconfigured(t *testing.T) {
	ctx := context.Background()
	svc, sk, _, _ := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: s\n---\n"})
	if _, err := svc.Compare(ctx, sk.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("Compare err = %v, want ErrValidation", err)
	}
	if _, err := svc.Sync(ctx, sk.ID, "pull"); !errors.Is(err, ErrValidation) {
		t.Fatalf("Sync err = %v, want ErrValidation", err)
	}
	if _, err := svc.PullRequest(ctx, sk.ID, "", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("PullRequest err = %v, want ErrValidation", err)
	}
}

// TestIgnoredSkillFile: local build artifacts (package.zip, Python bytecode
// caches, OS metadata) and git metadata never sync; ordinary files do.
func TestIgnoredSkillFile(t *testing.T) {
	ignored := []string{
		"package.zip",
		"./package.zip",
		".git/config",
		"scripts/__pycache__/base_cases.cpython-312.pyc",
		"nested/__pycache__/mod.pyc",
		"cache.pyo",
		".DS_Store",
		"docs/Thumbs.db",
	}
	for _, p := range ignored {
		if !ignoredSkillFile(p) {
			t.Fatalf("ignoredSkillFile(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"SKILL.md", "scripts/run.py", "docs/notes.md", ".gitkeep"} {
		if ignoredSkillFile(p) {
			t.Fatalf("ignoredSkillFile(%q) = true, want false", p)
		}
	}
}
