package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/link"
	"github.com/eushing/agentwork/internal/proto"
	"github.com/eushing/agentwork/internal/service"
	"github.com/eushing/agentwork/internal/store"
	"github.com/eushing/agentwork/internal/zipx"
)

// fakeMachinePeer runs a /connect-style WebSocket server that registers itself
// as machineID's peer and records every config.push it receives.
func fakeMachinePeer(t *testing.T, d *Daemon, machineID string) <-chan link.ConfigPushParams {
	t.Helper()
	pushes := make(chan link.ConfigPushParams, 8)
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	registered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// The MACHINE side of the link: it answers config.push requests the
		// daemon sends. (The daemon's own peer must wrap the CLIENT conn — a
		// peer around the server conn would read its own writes.)
		peer := link.NewPeer(conn)
		peer.Handle(link.MethodConfigPush, func(_ context.Context, raw json.RawMessage) (any, *link.RPCError) {
			var params link.ConfigPushParams
			if err := json.Unmarshal(raw, &params); err != nil {
				return nil, &link.RPCError{Code: link.CodeInvalidParams, Message: err.Error()}
			}
			written := make([]string, 0, len(params.Skills))
			for _, sk := range params.Skills {
				written = append(written, sk.Name)
			}
			pushes <- params
			return link.ConfigPushResult{Written: written}, nil
		})
		close(registered)
	}))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial fake machine: %v", err)
	}
	<-registered
	// The daemon's end of the link is the client conn.
	d.RegisterMachinePeer(machineID, link.NewPeer(conn))
	t.Cleanup(func() {
		d.MachinePeer(machineID).Close()
		conn.Close()
		srv.CloseClientConnections()
		srv.Close()
	})
	deadline := time.Now().Add(3 * time.Second)
	for d.MachinePeer(machineID) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("machine %q peer never registered", machineID)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return pushes
}

// newSkillPushDaemon builds a daemon wired to real services over a temp home.
func newSkillPushDaemon(t *testing.T) (*Daemon, *service.SkillService, *service.AgentService) {
	t.Helper()
	t.Setenv("AGENTWORK_HOME", t.TempDir())
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	bus := events.NewBus()
	goalSvc := service.NewGoalService(st, bus)
	runSvc := service.NewRunService(st, bus)
	commentSvc := service.NewCommentService(st, bus)
	agentSvc := service.NewAgentService(st, bus)
	goalSvc.SetRunService(runSvc)
	runSvc.SetGoalService(goalSvc)
	squadSvc := service.NewSquadService(st, bus)
	schedSvc := service.NewScheduleService(st, bus)
	d := New(st, bus, "127.0.0.1:0", proto.NewRegistry(), goalSvc, runSvc, commentSvc, agentSvc, squadSvc, schedSvc, nil, nil, nil)
	return d, service.NewSkillService(st), agentSvc
}

func seedAgentWithSkill(t *testing.T, d *Daemon, agents *service.AgentService, sk *service.Skill) *service.Agent {
	t.Helper()
	ctx := context.Background()
	rt, err := service.NewRuntimeService(d.st).Create(ctx, service.Runtime{Name: "opencode@m1", MachineID: "m1"})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	ag, err := agents.Create(ctx, service.Agent{Name: "skill-push-agent", RuntimeID: rt.ID, Skills: []string{sk.ID}})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return ag
}

// TestPushAgentSkillsSendsLatestArchive: the archive config.push carries is the
// skill's CURRENT files — after a sync rewrote them (UpsertByName is the same
// write path sync pull uses) the push must carry the new content, not the
// stale package.zip. This is the daemon half of "the agent sees the new skill
// after the sync interface, without restarting anything".
func TestPushAgentSkillsSendsLatestArchive(t *testing.T) {
	d, skillSvc, agentSvc := newSkillPushDaemon(t)
	ctx := context.Background()
	sk, err := skillSvc.Create(ctx, map[string]string{
		"SKILL.md": "---\nname: demo-push\ndescription: demo\n---\nversion one\n",
	})
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	ag := seedAgentWithSkill(t, d, agentSvc, sk)
	pushes := fakeMachinePeer(t, d, "m1")

	d.PushAgentSkills(ctx, ag.ID)
	first := waitPush(t, pushes)
	if len(first.Skills) != 1 || first.Skills[0].Name != "demo-push" {
		t.Fatalf("first push skills = %+v", first.Skills)
	}
	if got := archiveFile(t, first.Skills[0].Archive, "SKILL.md"); !strings.Contains(got, "version one") {
		t.Fatalf("first push archive = %q", got)
	}

	// Simulate the sync interface rewriting the skill (pull or push + local
	// update both land through writeSkillFilesAndArchive).
	if _, err := skillSvc.UpsertByName(ctx, "demo-push", "demo", map[string]string{
		"SKILL.md":     "---\nname: demo-push\ndescription: demo\n---\nversion two\n",
		"scripts/x.sh": "echo two\n",
	}); err != nil {
		t.Fatalf("update skill: %v", err)
	}

	d.PushAgentSkills(ctx, ag.ID)
	second := waitPush(t, pushes)
	if len(second.Skills) != 1 {
		t.Fatalf("second push skills = %+v", second.Skills)
	}
	if got := archiveFile(t, second.Skills[0].Archive, "SKILL.md"); !strings.Contains(got, "version two") {
		t.Fatalf("second push archive must carry the updated SKILL.md, got %q", got)
	}
	if got := archiveFile(t, second.Skills[0].Archive, "scripts/x.sh"); got != "echo two\n" {
		t.Fatalf("second push must carry new files, got %q", got)
	}
}

// TestPushAgentSkillsRefusesEmptyPush: when every selected archive is
// unreadable, the push must NOT reach the machine — an empty config.push makes
// the machine wipe its staging dir and install 0 skills (the live
// "0 skill(s) installed" + stale workdir bug).
func TestPushAgentSkillsRefusesEmptyPush(t *testing.T) {
	d, skillSvc, agentSvc := newSkillPushDaemon(t)
	ctx := context.Background()
	sk, err := skillSvc.Create(ctx, map[string]string{
		"SKILL.md": "---\nname: unreadable\ndescription: demo\n---\nbody\n",
	})
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	ag := seedAgentWithSkill(t, d, agentSvc, sk)
	pushes := fakeMachinePeer(t, d, "m1")

	// Reproduce the live failure: the DB row exists but the skill dir is gone
	// (old sweep bug / copied DB) — PackageZip can read neither the archive
	// nor the files.
	if err := os.RemoveAll(filepath.Join(os.Getenv("AGENTWORK_HOME"), "runs", "skills", sk.ID)); err != nil {
		t.Fatalf("remove skill dir: %v", err)
	}

	d.PushAgentSkills(ctx, ag.ID)
	select {
	case params := <-pushes:
		t.Fatalf("empty push must be refused, machine received %d skill(s): %+v", len(params.Skills), params.Skills)
	case <-time.After(300 * time.Millisecond):
	}
}

func waitPush(t *testing.T, pushes <-chan link.ConfigPushParams) link.ConfigPushParams {
	t.Helper()
	select {
	case p := <-pushes:
		return p
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for config.push")
		return link.ConfigPushParams{}
	}
}

func archiveFile(t *testing.T, archive []byte, path string) string {
	t.Helper()
	dir := t.TempDir()
	if err := zipx.Extract(archive, dir); err != nil {
		t.Fatalf("extract archive: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		t.Fatalf("read %s from archive: %v", path, err)
	}
	return string(b)
}
