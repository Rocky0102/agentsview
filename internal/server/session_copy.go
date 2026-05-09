package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wesm/agentsview/internal/db"
)

const defaultSessionCopyRoot = `D:\`

type sessionCopyResponse struct {
	Path     string   `json:"path"`
	Sessions int      `json:"sessions"`
	Copied   int      `json:"copied"`
	Missing  []string `json:"missing,omitempty"`
}

func (s *Server) handleCopySessionFiles(
	w http.ResponseWriter, r *http.Request,
) {
	if s.db.ReadOnly() {
		writeError(w, http.StatusNotImplemented,
			"not available in remote mode")
		return
	}

	sessionID := r.PathValue("id")
	root, sessions, err := s.collectSessionFamily(r.Context(), sessionID)
	if err != nil {
		if handleContextError(w, err) {
			return
		}
		log.Printf("copy session files lookup: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if root == nil || root.DeletedAt != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	target := s.sessionCopyTarget(root)
	copied, missing, err := copySessionSourceFiles(sessions, target)
	if err != nil {
		log.Printf("copy session files: %v", err)
		writeError(w, http.StatusInternalServerError, "copy failed")
		return
	}
	if copied == 0 {
		writeError(w, http.StatusNotFound, "no source files found")
		return
	}

	if err := s.openDirectory(target); err != nil {
		log.Printf("open copied session directory: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to open directory")
		return
	}

	writeJSON(w, http.StatusOK, sessionCopyResponse{
		Path:     target,
		Sessions: len(sessions),
		Copied:   copied,
		Missing:  missing,
	})
}

func (s *Server) sessionCopyTarget(root *db.Session) string {
	agent := sanitizePathSegment(root.Agent)
	if agent == "" {
		agent = "unknown"
	}
	date := s.sessionCopyNow().Format("20060102")
	sessionID := sanitizePathSegment(root.ID)
	if sessionID == "" {
		sessionID = "session"
	}
	return filepath.Join(s.sessionCopyRoot, agent, date, sessionID)
}

func (s *Server) collectSessionFamily(
	ctx context.Context, sessionID string,
) (*db.Session, []db.Session, error) {
	root, err := s.findSessionRoot(ctx, sessionID)
	if err != nil || root == nil {
		return root, nil, err
	}

	seen := map[string]bool{}
	var out []db.Session
	if err := s.collectSessionDescendants(ctx, *root, seen, &out); err != nil {
		return nil, nil, err
	}
	return root, out, nil
}

func (s *Server) findSessionRoot(
	ctx context.Context, sessionID string,
) (*db.Session, error) {
	seen := map[string]bool{}
	currentID := sessionID
	var current *db.Session
	for {
		if seen[currentID] {
			return nil, fmt.Errorf("cycle while resolving root for %s", sessionID)
		}
		seen[currentID] = true

		sess, err := s.db.GetSessionFull(ctx, currentID)
		if err != nil {
			return nil, err
		}
		if sess == nil {
			return nil, nil
		}
		current = sess
		if sess.ParentSessionID == nil || *sess.ParentSessionID == "" {
			return current, nil
		}
		currentID = *sess.ParentSessionID
	}
}

func (s *Server) collectSessionDescendants(
	ctx context.Context,
	session db.Session,
	seen map[string]bool,
	out *[]db.Session,
) error {
	if seen[session.ID] {
		return nil
	}
	seen[session.ID] = true
	*out = append(*out, session)

	children, err := s.db.GetChildSessions(ctx, session.ID)
	if err != nil {
		return err
	}
	for _, child := range children {
		full, err := s.db.GetSessionFull(ctx, child.ID)
		if err != nil {
			return err
		}
		if full == nil || full.DeletedAt != nil {
			continue
		}
		if err := s.collectSessionDescendants(ctx, *full, seen, out); err != nil {
			return err
		}
	}
	return nil
}

func copySessionSourceFiles(
	sessions []db.Session,
	target string,
) (int, []string, error) {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return 0, nil, fmt.Errorf("creating target directory: %w", err)
	}

	sourceSeen := map[string]bool{}
	baseCounts := map[string]int{}
	for _, session := range sessions {
		if session.FilePath == nil || *session.FilePath == "" {
			continue
		}
		path := filepath.Clean(*session.FilePath)
		if sourceSeen[path] {
			continue
		}
		sourceSeen[path] = true
		baseCounts[filepath.Base(path)]++
	}

	nameSeen := map[string]bool{}
	missing := []string{}
	copied := 0
	sourceSeen = map[string]bool{}
	for _, session := range sessions {
		if session.FilePath == nil || *session.FilePath == "" {
			continue
		}
		src := filepath.Clean(*session.FilePath)
		if sourceSeen[src] {
			continue
		}
		sourceSeen[src] = true

		info, err := os.Stat(src)
		if err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, src)
				continue
			}
			return copied, missing, fmt.Errorf("stat %s: %w", src, err)
		}
		if info.IsDir() {
			return copied, missing, fmt.Errorf("source is a directory: %s", src)
		}

		name := filepath.Base(src)
		if baseCounts[name] > 1 {
			name = sanitizePathSegment(session.ID) + "_" + name
		}
		name = uniqueFileName(name, nameSeen)
		dst := filepath.Join(target, name)
		if err := copyFile(src, dst, info.Mode()); err != nil {
			return copied, missing, err
		}
		copied++
	}
	return copied, missing, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("open target %s: %w", dst, err)
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close target %s: %w", dst, err)
	}
	return nil
}

func uniqueFileName(name string, seen map[string]bool) string {
	if !seen[name] {
		seen[name] = true
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if !seen[candidate] {
			seen[candidate] = true
			return candidate
		}
	}
}

func sanitizePathSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return strings.Trim(b.String(), ". ")
}

func openDirectoryInFileManager(dir string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
