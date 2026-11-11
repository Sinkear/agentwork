package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/eushing/agentwork/internal/store"
)

func TestCreateSnapshotAndFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTWORK_HOME", home)
	dbPath := filepath.Join(home, "agentwork.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if _, err := st.DB().Exec(`INSERT INTO runtime (id,name,machine_id,args,env,status,created_at) VALUES ('rt','test-rt','','[]','{}','active','now')`); err != nil {
		t.Fatalf("insert runtime: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO agent (id,name,type,description,runtime_id,system_prompt,model,env,mcp_servers,skills,max_concurrent,created_at)
		VALUES ('a1','keeper','standard','','rt','','', '{}','[]','[]',3,'now')`); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	skillFile := filepath.Join(home, "runs", "skills", "s1", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillFile, []byte("# skill\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agents.yaml"), []byte("agents: []\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	res, err := Create(context.Background(), Options{DBPath: dbPath, OutDir: out, WithFiles: true})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if res.Counts["agent"] != 1 {
		t.Fatalf("agent count = %d", res.Counts["agent"])
	}
	if _, err := os.Stat(res.Database); err != nil {
		t.Fatalf("db missing: %v", err)
	}
	if res.Files == "" {
		t.Fatalf("expected files archive")
	}
	if fi, err := os.Stat(res.Files); err != nil || fi.Size() == 0 {
		t.Fatalf("files archive missing/empty: %v", err)
	}
	if _, err := os.Stat(res.Manifest); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}
