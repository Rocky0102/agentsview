package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestWorktreeProjectMappingsCRUDNormalizesAndScopesByMachine(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()

	prefix := filepath.Join(t.TempDir(), "my-app.worktrees")
	m, err := d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine:    "laptop",
		PathPrefix: prefix + string(filepath.Separator),
		Project:    "my-app",
		Enabled:    true,
	})
	requireNoError(t, err, "create mapping")
	if m.Machine != "laptop" {
		t.Fatalf("machine = %q, want laptop", m.Machine)
	}
	if m.PathPrefix != prefix {
		t.Fatalf("path_prefix = %q, want %q", m.PathPrefix, prefix)
	}
	if m.Project != "my_app" {
		t.Fatalf("project = %q, want my_app", m.Project)
	}

	got, err := d.ListWorktreeProjectMappings(ctx, "laptop")
	requireNoError(t, err, "list laptop mappings")
	if len(got) != 1 || got[0].ID != m.ID {
		t.Fatalf("laptop mappings = %+v, want created mapping", got)
	}

	other, err := d.ListWorktreeProjectMappings(ctx, "server")
	requireNoError(t, err, "list server mappings")
	if len(other) != 0 {
		t.Fatalf("server mappings = %+v, want none", other)
	}
}

func TestWorktreeProjectMappingsRejectInvalidAndDuplicateRows(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	prefix := filepath.Join(t.TempDir(), "repo.worktrees")

	_, err := d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine: "laptop", PathPrefix: " ", Project: "repo", Enabled: true,
	})
	if err == nil {
		t.Fatal("empty path prefix accepted")
	}
	_, err = d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine: "laptop", PathPrefix: prefix, Project: " ", Enabled: true,
	})
	if err == nil {
		t.Fatal("empty project accepted")
	}

	_, err = d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine: "laptop", PathPrefix: prefix, Project: "repo", Enabled: true,
	})
	requireNoError(t, err, "create first mapping")
	_, err = d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine: "laptop", PathPrefix: prefix, Project: "repo2", Enabled: true,
	})
	if !errors.Is(err, ErrWorktreeMappingDuplicate) {
		t.Fatalf("duplicate error = %v, want ErrWorktreeMappingDuplicate", err)
	}
}

func TestResolveWorktreeProjectMappingUsesLongestPrefixAndBoundaries(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	root := t.TempDir()
	broad := filepath.Join(root, "repo.worktrees")
	nested := filepath.Join(broad, "special")

	_, err := d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine: "laptop", PathPrefix: broad, Project: "repo", Enabled: true,
	})
	requireNoError(t, err, "create broad mapping")
	_, err = d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine: "laptop", PathPrefix: nested, Project: "special-repo", Enabled: true,
	})
	requireNoError(t, err, "create nested mapping")

	project, ok, err := d.ResolveWorktreeProjectMapping(ctx, "laptop",
		filepath.Join(nested, "feat", "thing"), "leaf")
	requireNoError(t, err, "resolve nested")
	if !ok || project != "special_repo" {
		t.Fatalf("nested resolve = (%q,%v), want (special_repo,true)", project, ok)
	}

	project, ok, err = d.ResolveWorktreeProjectMapping(ctx, "laptop",
		filepath.Join(broad, "feat", "thing"), "leaf")
	requireNoError(t, err, "resolve broad")
	if !ok || project != "repo" {
		t.Fatalf("broad resolve = (%q,%v), want (repo,true)", project, ok)
	}

	_, ok, err = d.ResolveWorktreeProjectMapping(ctx, "laptop", broad+"-other", "leaf")
	requireNoError(t, err, "resolve boundary miss")
	if ok {
		t.Fatal("path with shared string prefix matched across component boundary")
	}

	project, ok = ResolveWorktreeProjectFromMappings(
		[]WorktreeProjectMapping{
			{PathPrefix: broad, Project: "repo"},
			{PathPrefix: nested, Project: "special_repo"},
		},
		filepath.Join(nested, "feat", "thing"),
		"leaf",
	)
	if !ok || project != "special_repo" {
		t.Fatalf("unsorted resolve = (%q,%v), want (special_repo,true)", project, ok)
	}
}

func TestResolveWorktreeProjectMappingMatchesRootPrefix(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()

	_, err := d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine:    "laptop",
		PathPrefix: string(filepath.Separator),
		Project:    "root-project",
		Enabled:    true,
	})
	requireNoError(t, err, "create root mapping")

	project, ok, err := d.ResolveWorktreeProjectMapping(ctx, "laptop",
		filepath.Join(string(filepath.Separator), "tmp", "worktree"), "leaf")
	requireNoError(t, err, "resolve root")
	if !ok || project != "root_project" {
		t.Fatalf("root resolve = (%q,%v), want (root_project,true)", project, ok)
	}
}

func TestApplyWorktreeProjectMappingsUpdatesOnlyCurrentMachineAndEnabledRows(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	root := t.TempDir()
	prefix := filepath.Join(root, "repo.worktrees")
	disabledPrefix := filepath.Join(root, "disabled.worktrees")

	_, err := d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine: "laptop", PathPrefix: prefix, Project: "repo", Enabled: true,
	})
	requireNoError(t, err, "create enabled mapping")
	_, err = d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine: "laptop", PathPrefix: disabledPrefix, Project: "disabled", Enabled: false,
	})
	requireNoError(t, err, "create disabled mapping")

	insert := func(id, machine, project, cwd string) {
		t.Helper()
		err := d.UpsertSession(Session{
			ID: id, Project: project, Machine: machine, Agent: "claude", Cwd: cwd,
		})
		requireNoError(t, err, "insert "+id)
	}
	insert("match", "laptop", "leaf", filepath.Join(prefix, "feat", "thing"))
	insert("same-project", "laptop", "repo", filepath.Join(prefix, "bugfix"))
	insert("other-machine", "server", "leaf", filepath.Join(prefix, "feat", "thing"))
	insert("disabled", "laptop", "leaf", filepath.Join(disabledPrefix, "feat"))
	insert("trashed", "laptop", "leaf", filepath.Join(prefix, "trashed"))
	requireNoError(t, d.SoftDeleteSession("trashed"), "trash session")

	result, err := d.ApplyWorktreeProjectMappings(ctx, "laptop")
	requireNoError(t, err, "apply mappings")
	if result.MatchedSessions != 2 || result.UpdatedSessions != 1 {
		t.Fatalf("apply result = %+v, want matched=2 updated=1", result)
	}
	assertSessionProject(t, d, "match", "repo")
	assertSessionProject(t, d, "same-project", "repo")
	assertSessionProject(t, d, "other-machine", "leaf")
	assertSessionProject(t, d, "disabled", "leaf")
	assertFullSessionProject(t, d, "trashed", "leaf")
}

func TestApplyWorktreeProjectMappingsBumpsLocalModifiedAt(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	prefix := filepath.Join(t.TempDir(), "repo.worktrees")

	_, err := d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine: "laptop", PathPrefix: prefix, Project: "repo", Enabled: true,
	})
	requireNoError(t, err, "create mapping")
	requireNoError(t, d.UpsertSession(Session{
		ID: "match", Project: "leaf", Machine: "laptop", Agent: "claude",
		Cwd: filepath.Join(prefix, "feat"),
	}), "insert match")

	before, err := d.GetSessionFull(ctx, "match")
	requireNoError(t, err, "GetSessionFull before")
	if before.LocalModifiedAt != nil {
		t.Fatalf("local_modified_at before = %v, want nil", *before.LocalModifiedAt)
	}

	result, err := d.ApplyWorktreeProjectMappings(ctx, "laptop")
	requireNoError(t, err, "apply mappings")
	if result.UpdatedSessions != 1 {
		t.Fatalf("updated sessions = %d, want 1", result.UpdatedSessions)
	}

	after, err := d.GetSessionFull(ctx, "match")
	requireNoError(t, err, "GetSessionFull after")
	if after.Project != "repo" {
		t.Fatalf("project = %q, want repo", after.Project)
	}
	if after.LocalModifiedAt == nil || *after.LocalModifiedAt == "" {
		t.Fatalf("local_modified_at after = %v, want timestamp", after.LocalModifiedAt)
	}
}

func TestApplyWorktreeProjectMappingsToSessionUsesCurrentSessionState(
	t *testing.T,
) {
	d := testDB(t)
	ctx := context.Background()
	root := t.TempDir()
	stalePrefix := filepath.Join(root, "stale.worktrees")
	currentPrefix := filepath.Join(root, "current.worktrees")

	_, err := d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine: "laptop", PathPrefix: stalePrefix, Project: "stale-repo", Enabled: true,
	})
	requireNoError(t, err, "create stale mapping")
	_, err = d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine: "laptop", PathPrefix: currentPrefix, Project: "current-repo", Enabled: true,
	})
	requireNoError(t, err, "create current mapping")

	staleCwd := filepath.Join(stalePrefix, "feat")
	currentCwd := filepath.Join(currentPrefix, "feat")
	requireNoError(t, d.UpsertSession(Session{
		ID: "match", Project: "leaf", Machine: "laptop", Agent: "claude",
		Cwd: staleCwd,
	}), "insert stale match")
	requireNoError(t, d.UpsertSession(Session{
		ID: "match", Project: "other_leaf", Machine: "laptop", Agent: "claude",
		Cwd: currentCwd,
	}), "move session before apply")

	updated, err := d.ApplyWorktreeProjectMappingToSession(
		ctx, "laptop", "match", staleCwd, "leaf",
	)
	requireNoError(t, err, "ApplyWorktreeProjectMappingToSession")
	if !updated {
		t.Fatal("updated = false, want true")
	}
	assertSessionProject(t, d, "match", "current_repo")
}

func TestApplyWorktreeProjectMappingToSessionFromSyncDoesNotBumpLocalModifiedAt(
	t *testing.T,
) {
	d := testDB(t)
	ctx := context.Background()
	prefix := filepath.Join(t.TempDir(), "repo.worktrees")

	_, err := d.CreateWorktreeProjectMapping(ctx, WorktreeProjectMapping{
		Machine: "laptop", PathPrefix: prefix, Project: "repo", Enabled: true,
	})
	requireNoError(t, err, "create mapping")
	requireNoError(t, d.UpsertSession(Session{
		ID: "match", Project: "leaf", Machine: "laptop", Agent: "claude",
		Cwd: filepath.Join(prefix, "feat"),
	}), "insert match")

	before, err := d.GetSessionFull(ctx, "match")
	requireNoError(t, err, "GetSessionFull before")
	if before.LocalModifiedAt != nil {
		t.Fatalf("local_modified_at before = %v, want nil", before.LocalModifiedAt)
	}

	updated, err := d.ApplyWorktreeProjectMappingToSessionFromSync(
		ctx, "laptop", "match", before.Cwd, before.Project,
	)
	requireNoError(t, err, "ApplyWorktreeProjectMappingToSessionFromSync")
	if !updated {
		t.Fatal("updated = false, want true")
	}

	after, err := d.GetSessionFull(ctx, "match")
	requireNoError(t, err, "GetSessionFull after")
	if after.Project != "repo" {
		t.Fatalf("project = %q, want repo", after.Project)
	}
	if after.LocalModifiedAt != nil {
		t.Fatalf("local_modified_at after = %v, want nil", after.LocalModifiedAt)
	}
}

func assertSessionProject(t *testing.T, d *DB, id, want string) {
	t.Helper()
	got, err := d.GetSession(context.Background(), id)
	requireNoError(t, err, "GetSession "+id)
	if got.Project != want {
		t.Fatalf("session %s project = %q, want %q", id, got.Project, want)
	}
}

func TestWorktreeProjectMappingsFinalMetadataCopyRefreshesStalePrecopy(
	t *testing.T,
) {
	dir := t.TempDir()
	ctx := context.Background()

	srcPath := filepath.Join(dir, "src.db")
	srcDB, err := Open(srcPath)
	requireNoError(t, err, "Open src")
	defer srcDB.Close()

	prefix := filepath.Join(dir, "app.worktrees")
	sourceMapping, err := srcDB.CreateWorktreeProjectMapping(
		ctx,
		WorktreeProjectMapping{
			Machine:    "laptop",
			PathPrefix: prefix,
			Project:    "old-project",
			Enabled:    true,
		},
	)
	requireNoError(t, err, "CreateWorktreeProjectMapping src")

	dstPath := filepath.Join(dir, "dst.db")
	dstDB, err := Open(dstPath)
	requireNoError(t, err, "Open dst")
	defer dstDB.Close()

	requireNoError(
		t,
		dstDB.CopyWorktreeProjectMappingsFrom(srcPath),
		"CopyWorktreeProjectMappingsFrom",
	)

	_, err = srcDB.UpdateWorktreeProjectMapping(
		ctx,
		"laptop",
		sourceMapping.ID,
		WorktreeProjectMapping{
			PathPrefix: prefix,
			Project:    "new-project",
			Enabled:    false,
		},
	)
	requireNoError(t, err, "UpdateWorktreeProjectMapping src")
	requireNoError(t, srcDB.CloseConnections(), "CloseConnections src")

	_, err = dstDB.getWriter().ExecContext(ctx, `
		UPDATE worktree_project_mappings
		SET updated_at = '9999-12-31T23:59:59.999Z'
		WHERE machine = ? AND path_prefix = ?`,
		"laptop",
		prefix,
	)
	requireNoError(t, err, "force dst updated_at ahead")

	requireNoError(
		t,
		dstDB.CopySessionMetadataFrom(srcPath),
		"CopySessionMetadataFrom",
	)

	got, err := dstDB.ListWorktreeProjectMappings(ctx, "laptop")
	requireNoError(t, err, "ListWorktreeProjectMappings")
	if len(got) != 1 {
		t.Fatalf("mapping count = %d, want 1: %+v", len(got), got)
	}
	if got[0].Project != "new_project" {
		t.Fatalf("project = %q, want new_project", got[0].Project)
	}
	if got[0].Enabled {
		t.Fatal("mapping should reflect disabled source row")
	}
}

func TestWorktreeProjectMappingsFinalMetadataCopyRemovesDeletedPrecopy(
	t *testing.T,
) {
	dir := t.TempDir()
	ctx := context.Background()

	srcPath := filepath.Join(dir, "src.db")
	srcDB, err := Open(srcPath)
	requireNoError(t, err, "Open src")
	defer srcDB.Close()

	prefix := filepath.Join(dir, "app.worktrees")
	sourceMapping, err := srcDB.CreateWorktreeProjectMapping(
		ctx,
		WorktreeProjectMapping{
			Machine:    "laptop",
			PathPrefix: prefix,
			Project:    "old-project",
			Enabled:    true,
		},
	)
	requireNoError(t, err, "CreateWorktreeProjectMapping src")

	dstPath := filepath.Join(dir, "dst.db")
	dstDB, err := Open(dstPath)
	requireNoError(t, err, "Open dst")
	defer dstDB.Close()

	requireNoError(
		t,
		dstDB.CopyWorktreeProjectMappingsFrom(srcPath),
		"CopyWorktreeProjectMappingsFrom",
	)

	requireNoError(
		t,
		srcDB.DeleteWorktreeProjectMapping(
			ctx, "laptop", sourceMapping.ID,
		),
		"DeleteWorktreeProjectMapping src",
	)
	requireNoError(t, srcDB.CloseConnections(), "CloseConnections src")

	requireNoError(
		t,
		dstDB.CopySessionMetadataFrom(srcPath),
		"CopySessionMetadataFrom",
	)

	got, err := dstDB.ListWorktreeProjectMappings(ctx, "laptop")
	requireNoError(t, err, "ListWorktreeProjectMappings")
	if len(got) != 0 {
		t.Fatalf("mapping count = %d, want 0: %+v", len(got), got)
	}
}

func assertFullSessionProject(t *testing.T, d *DB, id, want string) {
	t.Helper()
	got, err := d.GetSessionFull(context.Background(), id)
	requireNoError(t, err, "GetSessionFull "+id)
	if got.Project != want {
		t.Fatalf("session %s project = %q, want %q", id, got.Project, want)
	}
}
