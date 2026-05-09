package server_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wesm/agentsview/internal/db"
	"github.com/wesm/agentsview/internal/server"
)

func TestCopySessionFilesCopiesRootFamilyAndOpensTarget(t *testing.T) {
	copyRoot := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	var opened string
	te := setupWithServerOpts(t, []server.Option{
		server.WithSessionCopyRoot(copyRoot),
		server.WithSessionCopyNow(func() time.Time { return now }),
		server.WithOpenDirectoryFunc(func(dir string) error {
			opened = dir
			return nil
		}),
	})

	sourceDir := t.TempDir()
	parentPath := filepath.Join(sourceDir, "parent.jsonl")
	childPath := filepath.Join(sourceDir, "child.jsonl")
	nestedPath := filepath.Join(sourceDir, "nested.jsonl")
	for path, content := range map[string]string{
		parentPath: "parent",
		childPath:  "child",
		nestedPath: "nested",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing source file %s: %v", path, err)
		}
	}

	parentID := "codex:parent"
	childID := "codex:child"
	nestedID := "codex:nested"
	te.seedSession(t, parentID, "proj", 2, func(s *db.Session) {
		s.Agent = "codex"
		s.FilePath = &parentPath
	})
	te.seedSession(t, childID, "proj", 1, func(s *db.Session) {
		s.Agent = "codex"
		s.ParentSessionID = &parentID
		s.RelationshipType = "subagent"
		s.FilePath = &childPath
	})
	te.seedSession(t, nestedID, "proj", 1, func(s *db.Session) {
		s.Agent = "codex"
		s.ParentSessionID = &childID
		s.RelationshipType = "subagent"
		s.FilePath = &nestedPath
	})

	w := te.post(t,
		"/api/v1/sessions/"+url.PathEscape(childID)+"/copy-files",
		"{}",
	)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var resp struct {
		Path     string   `json:"path"`
		Sessions int      `json:"sessions"`
		Copied   int      `json:"copied"`
		Missing  []string `json:"missing"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	wantTarget := filepath.Join(copyRoot, "codex", "20260509", "codex_parent")
	if resp.Path != wantTarget {
		t.Fatalf("path = %q, want %q", resp.Path, wantTarget)
	}
	if opened != wantTarget {
		t.Fatalf("opened = %q, want %q", opened, wantTarget)
	}
	if resp.Sessions != 3 || resp.Copied != 3 || len(resp.Missing) != 0 {
		t.Fatalf("response = %+v, want 3 sessions, 3 copied, no missing", resp)
	}

	for name, content := range map[string]string{
		"parent.jsonl": "parent",
		"child.jsonl":  "child",
		"nested.jsonl": "nested",
	} {
		got, err := os.ReadFile(filepath.Join(wantTarget, name))
		if err != nil {
			t.Fatalf("reading copied %s: %v", name, err)
		}
		if string(got) != content {
			t.Fatalf("%s content = %q, want %q", name, got, content)
		}
	}
}
