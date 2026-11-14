// Package backup creates a consistent snapshot of agentwork's persistent data.
//
// The primary artifact is a SQLite online backup produced with VACUUM INTO,
// so it is safe to run while the daemon is serving requests. Optional run
// files (skills) are archived separately, and a small JSON manifest records
// counts for post-restore verification.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eushing/agentwork/internal/paths"
	_ "modernc.org/sqlite"
)

// Result describes a completed backup.
type Result struct {
	CreatedAt    string         `json:"created_at"`
	Database     string         `json:"database"`
	DatabaseSize int64          `json:"database_size"`
	Files        string         `json:"files,omitempty"`
	Manifest     string         `json:"manifest"`
	Counts       map[string]int `json:"counts"`
}

// Options configures a backup run.
type Options struct {
	DBPath    string // defaults to paths.DB()
	OutDir    string // required; created if missing
	WithFiles bool   // also archive runs/skills and agents.yaml
}

// Create writes the backup and returns its manifest.
func Create(ctx context.Context, opts Options) (*Result, error) {
	if opts.OutDir == "" {
		return nil, fmt.Errorf("backup: output directory is required")
	}
	dbPath := opts.DBPath
	if dbPath == "" {
		dbPath = paths.DB()
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("backup: database %s: %w", dbPath, err)
	}
	if err := os.MkdirAll(opts.OutDir, 0755); err != nil {
		return nil, fmt.Errorf("backup: create output dir: %w", err)
	}

	ts := time.Now().Format("20060102-150405")
	dir := filepath.Join(opts.OutDir, "agentwork-"+ts)
	for i := 1; fileExists(dir); i++ {
		dir = fmt.Sprintf("%s-%d", filepath.Join(opts.OutDir, "agentwork-"+ts), i)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("backup: create snapshot dir: %w", err)
	}
	dbDst := filepath.Join(dir, "agentwork.db")

	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("backup: open database: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("backup: ping database: %w", err)
	}

	tmp := dbDst + ".tmp"
	_ = os.Remove(tmp)
	// VACUUM INTO cannot take a bound parameter, so quote the literal. Paths
	// with a single quote are rare but valid; double it per SQL rules.
	quoted := strings.ReplaceAll(tmp, "'", "''")
	if _, err := db.ExecContext(ctx, "VACUUM INTO '"+quoted+"'"); err != nil {
		return nil, fmt.Errorf("backup: vacuum into %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dbDst); err != nil {
		return nil, fmt.Errorf("backup: finalize database: %w", err)
	}
	if err := os.Chmod(dbDst, 0600); err != nil {
		return nil, fmt.Errorf("backup: chmod database: %w", err)
	}
	fi, err := os.Stat(dbDst)
	if err != nil {
		return nil, fmt.Errorf("backup: stat database: %w", err)
	}

	counts, integrity, err := verify(ctx, dbDst)
	if err != nil {
		return nil, err
	}
	if integrity != "ok" {
		return nil, fmt.Errorf("backup: integrity check failed: %s", integrity)
	}

	res := &Result{
		CreatedAt:    time.Now().Format(time.RFC3339),
		Database:     dbDst,
		DatabaseSize: fi.Size(),
		Counts:       counts,
	}
	if opts.WithFiles {
		filesDst := filepath.Join(dir, "agentwork-files.tar.gz")
		if err := archiveFiles(paths.Home(), filesDst); err != nil {
			return nil, fmt.Errorf("backup: archive files: %w", err)
		}
		res.Files = filesDst
	}
	manifest := filepath.Join(dir, "agentwork.db.manifest.json")
	res.Manifest = manifest
	raw, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("backup: encode manifest: %w", err)
	}
	if err := os.WriteFile(manifest, append(raw, '\n'), 0644); err != nil {
		return nil, fmt.Errorf("backup: write manifest: %w", err)
	}
	return res, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func verify(ctx context.Context, path string) (map[string]int, string, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, "", fmt.Errorf("backup: open snapshot: %w", err)
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return nil, "", fmt.Errorf("backup: integrity check: %w", err)
	}
	if integrity != "ok" {
		return nil, integrity, nil
	}
	tables := []string{"domain", "agent", "skill", "squad", "goal", "schedule", "chat_message"}
	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			return nil, "", fmt.Errorf("backup: count %s: %w", table, err)
		}
		counts[table] = n
	}
	return counts, integrity, nil
}

// archiveFiles tars the parts of the data home that are not inside SQLite:
// runs/skills/<skillID>/ (skill packages) and agents.yaml (machine routing).
func archiveFiles(home, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	add := func(abs, rel string) error {
		fi, err := os.Stat(abs)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if fi.IsDir() {
			return filepath.Walk(abs, func(p string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				r, err := filepath.Rel(home, p)
				if err != nil {
					return err
				}
				hdr, err := tar.FileInfoHeader(info, "")
				if err != nil {
					return err
				}
				hdr.Name = filepath.ToSlash(r)
				if info.IsDir() {
					hdr.Name += "/"
				}
				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}
				if info.Mode().IsRegular() {
					in, err := os.Open(p)
					if err != nil {
						return err
					}
					defer in.Close()
					_, err = io.Copy(tw, in)
					return err
				}
				return nil
			})
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		in, err := os.Open(abs)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	}

	if err := add(filepath.Join(home, "runs", "skills"), "runs/skills"); err != nil {
		return err
	}
	if err := add(filepath.Join(home, "agents.yaml"), "agents.yaml"); err != nil {
		return err
	}
	return nil
}
