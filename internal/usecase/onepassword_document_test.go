package usecase

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
	"github.com/kazuhideoki/veil/internal/infra"
)

func TestAddTargetCreatesOnePasswordDocumentAndRemovesWorkspaceFile(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, "targets = []")
	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	var stdout bytes.Buffer
	uc := AddTarget{
		FileSystem:      infra.OSFileSystem{},
		TrackedChecker:  stubTrackedChecker{},
		DocumentRuntime: runtime,
		Stdout:          &stdout,
		TargetPath:      ".env",
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("workspace target still exists, stat error = %v", err)
	}
	if got := string(runtime.documents["item-1"]); got != "TOKEN=test\n" {
		t.Fatalf("document data = %q", got)
	}
	if runtime.createdTitle != "Veil: myapp: .env" {
		t.Fatalf("title = %q", runtime.createdTitle)
	}
	if got := strings.Join(runtime.createdTags, ","); got != "veil,veil/workspace/myapp" {
		t.Fatalf("tags = %q", got)
	}
	configData, err := os.ReadFile(filepath.Join(tempHome, ".veil", "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	for _, want := range []string{`backend = "1password_document"`, `target = ".env"`, `item_id = "item-1"`, `vault = "Private"`} {
		if !strings.Contains(string(configData), want) {
			t.Fatalf("config = %q, want %q", string(configData), want)
		}
	}
}

func TestEmergeTargetsMaterializesOnePasswordDocumentAndRecordsHash(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=test\n")))
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=test\n")
	uc := EmergeTargets{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		Now:             func() time.Time { return time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC) },
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	workspaceData, err := os.ReadFile(filepath.Join(workspaceRoot, ".env"))
	if err != nil {
		t.Fatalf("ReadFile(workspace) returned error: %v", err)
	}
	if string(workspaceData) != "TOKEN=test\n" {
		t.Fatalf("workspace data = %q", string(workspaceData))
	}
	stateData, err := os.ReadFile(filepath.Join(tempHome, ".veil", "state.toml"))
	if err != nil {
		t.Fatalf("ReadFile(state) returned error: %v", err)
	}
	if !strings.Contains(string(stateData), `plaintext_sha256 = "`+sha256Hex([]byte("TOKEN=test\n"))+`"`) {
		t.Fatalf("state = %q", string(stateData))
	}
}

func TestEmergeTargetsBackfillsMissingPlaintextHashWhenFileMatchesDocument(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	documentData := []byte("TOKEN=test\n")
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex(documentData))
	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, documentData, 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	resolvedTargetPath, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}
	now := time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC)
	state := domain.DefaultState()
	if err := state.UpsertLeaseForStore("myapp", ".env", now.Add(-time.Hour), now.Add(time.Hour), onePasswordStoreID, resolvedTargetPath, "item-1"); err != nil {
		t.Fatalf("UpsertLeaseForStore() returned error: %v", err)
	}
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = documentData
	uc := EmergeTargets{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		Now:             func() time.Time { return now },
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	refreshed := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	lease, ok, err := refreshed.FindLease("myapp", ".env")
	if err != nil {
		t.Fatalf("FindLease() returned error: %v", err)
	}
	if !ok {
		t.Fatal("FindLease() returned ok=false")
	}
	if lease.PlaintextHash != sha256Hex(documentData) {
		t.Fatalf("PlaintextHash = %q", lease.PlaintextHash)
	}
}

func TestEmergeTargetsAllWorkspacesReadsOnePasswordDocumentsInParallel(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env", "config/app.json"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=test\n")))
	appendDocumentConfig(t, tempHome, "config/app.json", "item-2", sha256Hex([]byte("{\"key\":\"value\"}\n")))
	restoreWD := chdirForTest(t, tempHome)
	defer restoreWD()

	runtime := newBlockingOnePasswordRuntime(map[string][]byte{
		"item-1": []byte("TOKEN=test\n"),
		"item-2": []byte("{\"key\":\"value\"}\n"),
	})
	uc := EmergeTargets{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		Now:             func() time.Time { return time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC) },
		AllWorkspaces:   true,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if got := runtime.authenticateCalls(); got != 1 {
		t.Fatalf("authenticate calls = %d, want 1", got)
	}
	if got := runtime.maxConcurrentReads(); got < 2 {
		t.Fatalf("max concurrent reads = %d, want at least 2", got)
	}
	for _, target := range []string{".env", "config/app.json"} {
		if _, err := os.Stat(filepath.Join(workspaceRoot, target)); err != nil {
			t.Fatalf("Stat(%q) returned error: %v", target, err)
		}
	}
}

func TestEmergeTargetsAllWorkspacesAuthenticatesOnePasswordBeforeParallelReads(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	prepareOnePasswordWorkspace(t, tempHome, `targets = [".env", "config/app.json"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=test\n")))
	appendDocumentConfig(t, tempHome, "config/app.json", "item-2", sha256Hex([]byte("{\"key\":\"value\"}\n")))
	restoreWD := chdirForTest(t, tempHome)
	defer restoreWD()

	runtime := newBlockingOnePasswordRuntime(map[string][]byte{
		"item-1": []byte("TOKEN=test\n"),
		"item-2": []byte("{\"key\":\"value\"}\n"),
	})
	runtime.authErr = errors.New("locked")
	uc := EmergeTargets{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		Now:             func() time.Time { return time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC) },
		AllWorkspaces:   true,
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "authenticate 1Password CLI: locked") {
		t.Fatalf("error = %q", err)
	}
	if got := runtime.authenticateCalls(); got != 1 {
		t.Fatalf("authenticate calls = %d, want 1", got)
	}
	if got := runtime.maxConcurrentReads(); got != 0 {
		t.Fatalf("max concurrent reads = %d, want 0", got)
	}
}

func TestEmergeTargetsRejectsExistingOnePasswordFileWithInvalidLease(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=remote\n")))
	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=local\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	resolvedTargetPath, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}
	now := time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC)
	state := domain.DefaultState()
	if err := state.UpsertLeaseForStore("myapp", ".env", now.Add(-time.Hour), now.Add(time.Hour), onePasswordStoreID, resolvedTargetPath, "item-1"); err != nil {
		t.Fatalf("UpsertLeaseForStore() returned error: %v", err)
	}
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=remote\n")
	uc := EmergeTargets{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		Now:             func() time.Time { return now },
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "no recorded plaintext hash") {
		t.Fatalf("error = %q", err)
	}
	workspaceData, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile(workspace) returned error: %v", err)
	}
	if string(workspaceData) != "TOKEN=local\n" {
		t.Fatalf("workspace data = %q", string(workspaceData))
	}
}

func TestUpdateTargetCommitsMaterializedOnePasswordDocument(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	oldHash := sha256Hex([]byte("TOKEN=old\n"))
	appendDocumentConfig(t, tempHome, ".env", "item-1", oldHash)
	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=new\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	state := domain.DefaultState()
	now := time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC)
	if err := state.UpsertLeaseWithHash("myapp", ".env", now.Add(-time.Hour), now.Add(time.Hour), onePasswordStoreID, "", "item-1", oldHash); err != nil {
		t.Fatalf("UpsertLeaseWithHash() returned error: %v", err)
	}
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=old\n")
	uc := UpdateTarget{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		TargetPath:      ".env",
		Now:             func() time.Time { return now },
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if got := string(runtime.documents["item-1"]); got != "TOKEN=new\n" {
		t.Fatalf("document data = %q", got)
	}
	configData, err := os.ReadFile(filepath.Join(tempHome, ".veil", "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	if !strings.Contains(string(configData), sha256Hex([]byte("TOKEN=new\n"))) {
		t.Fatalf("config = %q", string(configData))
	}
}

func TestUpdateTargetRequiresActiveOnePasswordLease(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=old\n")))
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".env"), []byte("TOKEN=new\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=old\n")
	uc := UpdateTarget{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		TargetPath:      ".env",
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "not emerged") {
		t.Fatalf("error = %q", err)
	}
	if got := string(runtime.documents["item-1"]); got != "TOKEN=old\n" {
		t.Fatalf("document data = %q", got)
	}
}

func TestStatusTargetsShowsRemainingTTLForMaterializedOnePasswordTarget(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	documentData := []byte("TOKEN=test\n")
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex(documentData))
	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, documentData, 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	resolvedTargetPath, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}
	state := domain.DefaultState()
	now := time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC)
	if err := state.UpsertLeaseWithHash("myapp", ".env", now.Add(-time.Hour), now.Add(2*time.Hour+30*time.Minute), onePasswordStoreID, resolvedTargetPath, "item-1", sha256Hex(documentData)); err != nil {
		t.Fatalf("UpsertLeaseWithHash() returned error: %v", err)
	}
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	var stdout bytes.Buffer
	uc := StatusTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
		Now:        func() time.Time { return now },
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	for _, want := range []string{"materialized", "ttl=02h30m00s", ".env"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestStatusTargetsShowsRemainingTTLForModifiedOnePasswordTarget(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	oldData := []byte("TOKEN=old\n")
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex(oldData))
	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=new\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	resolvedTargetPath, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}
	state := domain.DefaultState()
	now := time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC)
	if err := state.UpsertLeaseWithHash("myapp", ".env", now.Add(-time.Hour), now.Add(45*time.Minute), onePasswordStoreID, resolvedTargetPath, "item-1", sha256Hex(oldData)); err != nil {
		t.Fatalf("UpsertLeaseWithHash() returned error: %v", err)
	}
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	var stdout bytes.Buffer
	uc := StatusTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
		Now:        func() time.Time { return now },
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	for _, want := range []string{"modified", "ttl=00h45m00s", ".env"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestUpdateTargetRejectsSymlinkWorkspaceTarget(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	oldHash := sha256Hex([]byte("TOKEN=old\n"))
	appendDocumentConfig(t, tempHome, ".env", "item-1", oldHash)
	targetPath := filepath.Join(workspaceRoot, ".env")
	linkedPath := filepath.Join(tempHome, "linked.env")
	if err := os.WriteFile(linkedPath, []byte("TOKEN=new\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	if err := os.Symlink(linkedPath, targetPath); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}
	state := domain.DefaultState()
	now := time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC)
	if err := state.UpsertLeaseWithHash("myapp", ".env", now.Add(-time.Hour), now.Add(time.Hour), onePasswordStoreID, "", "item-1", oldHash); err != nil {
		t.Fatalf("UpsertLeaseWithHash() returned error: %v", err)
	}
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=old\n")
	uc := UpdateTarget{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		TargetPath:      ".env",
		Now:             func() time.Time { return now },
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "materialized regular file") {
		t.Fatalf("error = %q", err)
	}
	if got := string(runtime.documents["item-1"]); got != "TOKEN=old\n" {
		t.Fatalf("document data = %q", got)
	}
}

func TestEditTargetRefusesWhenEmergedWorkspaceTargetHasUncommittedChanges(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	oldHash := sha256Hex([]byte("TOKEN=old\n"))
	appendDocumentConfig(t, tempHome, ".env", "item-1", oldHash)
	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=workspace\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	state := domain.DefaultState()
	now := time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC)
	if err := state.UpsertLeaseWithHash("myapp", ".env", now.Add(-time.Hour), now.Add(time.Hour), onePasswordStoreID, "", "item-1", oldHash); err != nil {
		t.Fatalf("UpsertLeaseWithHash() returned error: %v", err)
	}
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=old\n")
	uc := EditTarget{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		EditorRunner:    mutatingEditorRunner{data: []byte("TOKEN=edited\n")},
		EditorPath:      "vim",
		TargetPath:      ".env",
		Now:             func() time.Time { return now },
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	for _, want := range []string{"uncommitted changes", "veil update .env"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
	if got := string(runtime.documents["item-1"]); got != "TOKEN=old\n" {
		t.Fatalf("document data = %q", got)
	}
}

func TestEditTargetRejectsSymlinkWorkspaceTargetBeforeUpdatingDocument(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	oldHash := sha256Hex([]byte("TOKEN=old\n"))
	appendDocumentConfig(t, tempHome, ".env", "item-1", oldHash)
	targetPath := filepath.Join(workspaceRoot, ".env")
	linkedPath := filepath.Join(tempHome, "linked.env")
	if err := os.WriteFile(linkedPath, []byte("TOKEN=old\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	if err := os.Symlink(linkedPath, targetPath); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}
	state := domain.DefaultState()
	now := time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC)
	if err := state.UpsertLeaseWithHash("myapp", ".env", now.Add(-time.Hour), now.Add(time.Hour), onePasswordStoreID, "", "item-1", oldHash); err != nil {
		t.Fatalf("UpsertLeaseWithHash() returned error: %v", err)
	}
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=old\n")
	uc := EditTarget{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		EditorRunner:    mutatingEditorRunner{data: []byte("TOKEN=edited\n")},
		EditorPath:      "vim",
		TargetPath:      ".env",
		Now:             func() time.Time { return now },
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "materialized regular file") {
		t.Fatalf("error = %q", err)
	}
	if got := string(runtime.documents["item-1"]); got != "TOKEN=old\n" {
		t.Fatalf("document data = %q", got)
	}
}

func TestEditTargetRejectsWrongStoreLeaseBeforeUpdatingDocument(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	oldHash := sha256Hex([]byte("TOKEN=old\n"))
	appendDocumentConfig(t, tempHome, ".env", "item-1", oldHash)
	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=old\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	state := domain.DefaultState()
	now := time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC)
	if err := state.UpsertLeaseWithHash("myapp", ".env", now.Add(-time.Hour), now.Add(time.Hour), domain.DefaultStoreID, targetPath, "/tmp/store/.env", oldHash); err != nil {
		t.Fatalf("UpsertLeaseWithHash() returned error: %v", err)
	}
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=old\n")
	uc := EditTarget{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		EditorRunner:    mutatingEditorRunner{data: []byte("TOKEN=edited\n")},
		EditorPath:      "vim",
		TargetPath:      ".env",
		Now:             func() time.Time { return now },
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "not emerged from 1Password") {
		t.Fatalf("error = %q", err)
	}
	if got := string(runtime.documents["item-1"]); got != "TOKEN=old\n" {
		t.Fatalf("document data = %q", got)
	}
}

func TestEmergeTargetsRollsBackCreatedOnePasswordFilesWhenStateWriteFails(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=test\n")))
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=test\n")
	uc := EmergeTargets{
		FileSystem: failingStateWriteFS{
			homeDir:       tempHome,
			stateWriteErr: errors.New("state write failed"),
		},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		Now:             func() time.Time { return time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC) },
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if _, statErr := os.Stat(filepath.Join(workspaceRoot, ".env")); !os.IsNotExist(statErr) {
		t.Fatalf("workspace target still exists after rollback, err=%v", statErr)
	}
}

func TestVanishOnePasswordDocumentRefusesUncommittedChanges(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=old\n")))
	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=new\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), "version = 2\n\n[[leases]]\nworkspace_id = \"myapp\"\ntarget = \".env\"\nmounted_at = 2026-05-21T01:02:03Z\nexpires_at = 2026-05-22T01:02:03Z\nstore_id = \"1password\"\nworkspace_path = "+workspaceRootQuoted(targetPath)+"\nstore_path = \"item-1\"\nplaintext_sha256 = \""+sha256Hex([]byte("TOKEN=old\n"))+"\"\n")
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	uc := VanishTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &bytes.Buffer{},
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("error = %q", err)
	}
	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("workspace target was removed: %v", err)
	}
}

func TestRemoveTargetRestoresWorkspaceFileAndKeepsOnePasswordDocument(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=test\n")))
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=test\n")
	var stdout bytes.Buffer
	uc := RemoveTarget{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &stdout,
		TargetPath:      ".env",
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	workspaceData, err := os.ReadFile(filepath.Join(workspaceRoot, ".env"))
	if err != nil {
		t.Fatalf("ReadFile(workspace target) returned error: %v", err)
	}
	if string(workspaceData) != "TOKEN=test\n" {
		t.Fatalf("workspace data = %q", string(workspaceData))
	}
	if got := string(runtime.documents["item-1"]); got != "TOKEN=test\n" {
		t.Fatalf("1Password document was changed, data = %q", got)
	}
	configData, err := os.ReadFile(filepath.Join(tempHome, ".veil", "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	for _, unwanted := range []string{`target = ".env"`, `item_id = "item-1"`, `targets = [".env"]`} {
		if strings.Contains(string(configData), unwanted) {
			t.Fatalf("config = %q, unwanted %q", string(configData), unwanted)
		}
	}
}

func TestRemoveTargetRefusesWorkspaceFileThatDiffersFromOnePasswordDocument(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=remote\n")))
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".env"), []byte("TOKEN=local\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=remote\n")
	uc := RemoveTarget{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		TargetPath:      ".env",
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "differs from 1Password document") {
		t.Fatalf("error = %q", err)
	}
	configData, readErr := os.ReadFile(filepath.Join(tempHome, ".veil", "config.toml"))
	if readErr != nil {
		t.Fatalf("ReadFile(config) returned error: %v", readErr)
	}
	if !strings.Contains(string(configData), `target = ".env"`) {
		t.Fatalf("config = %q, want target to remain registered", string(configData))
	}
}

func TestPurgeTargetDeletesOnePasswordDocumentAndWorkspaceFile(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=test\n")))
	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=test\n")
	uc := PurgeTarget{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		AssumeYes:       true,
		TargetPath:      ".env",
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if _, ok := runtime.documents["item-1"]; ok {
		t.Fatal("1Password document still exists")
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("workspace target still exists, err=%v", err)
	}
	configData, err := os.ReadFile(filepath.Join(tempHome, ".veil", "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	if strings.Contains(string(configData), `target = ".env"`) {
		t.Fatalf("config = %q, target still registered", string(configData))
	}
}

func TestPurgeTargetRequiresConfirmationWhenNonInteractive(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=test\n")))
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=test\n")
	uc := PurgeTarget{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		TargetPath:      ".env",
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "target purge requires --yes") {
		t.Fatalf("error = %q", err)
	}
	if _, ok := runtime.documents["item-1"]; !ok {
		t.Fatal("1Password document was deleted")
	}
}

func TestPurgeTargetDoesNotDeleteOnePasswordDocumentWhenConfigWriteFails(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=test\n")))
	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=test\n")
	uc := PurgeTarget{
		FileSystem: failingStateWriteFS{
			homeDir:        tempHome,
			configWriteErr: errors.New("config write failed"),
		},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		AssumeYes:       true,
		TargetPath:      ".env",
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "config write failed") {
		t.Fatalf("error = %q", err)
	}
	if _, ok := runtime.documents["item-1"]; !ok {
		t.Fatal("1Password document was deleted")
	}
}

func TestWorkspaceRemoveRestoresAllWorkspaceFilesAndKeepsOnePasswordDocuments(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env", "config/app.json"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=test\n")))
	appendDocumentConfig(t, tempHome, "config/app.json", "item-2", sha256Hex([]byte("{\"key\":\"value\"}\n")))
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=test\n")
	runtime.documents["item-2"] = []byte("{\"key\":\"value\"}\n")
	uc := RemoveWorkspace{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	for target, want := range map[string]string{
		".env":            "TOKEN=test\n",
		"config/app.json": "{\"key\":\"value\"}\n",
	} {
		data, err := os.ReadFile(filepath.Join(workspaceRoot, target))
		if err != nil {
			t.Fatalf("ReadFile(%q) returned error: %v", target, err)
		}
		if string(data) != want {
			t.Fatalf("%s data = %q", target, string(data))
		}
	}
	if len(runtime.deleted) != 0 {
		t.Fatalf("deleted documents = %v, want none", runtime.deleted)
	}
	configData, err := os.ReadFile(filepath.Join(tempHome, ".veil", "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	if strings.Contains(string(configData), `[workspaces.myapp]`) || strings.Contains(string(configData), `[[documents]]`) {
		t.Fatalf("config = %q, workspace or documents still registered", string(configData))
	}
}

func TestWorkspacePurgeDeletesOnePasswordDocumentsAndWorkspaceFiles(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env", "config/app.json"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=test\n")))
	appendDocumentConfig(t, tempHome, "config/app.json", "item-2", sha256Hex([]byte("{\"key\":\"value\"}\n")))
	for target, data := range map[string][]byte{
		".env":            []byte("TOKEN=test\n"),
		"config/app.json": []byte("{\"key\":\"value\"}\n"),
	} {
		path := filepath.Join(workspaceRoot, target)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() returned error: %v", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("WriteFile() returned error: %v", err)
		}
	}
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=test\n")
	runtime.documents["item-2"] = []byte("{\"key\":\"value\"}\n")
	uc := PurgeWorkspace{
		FileSystem:      infra.OSFileSystem{},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		AssumeYes:       true,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if len(runtime.documents) != 0 {
		t.Fatalf("documents = %v, want none", runtime.documents)
	}
	for _, target := range []string{".env", "config/app.json"} {
		if _, err := os.Stat(filepath.Join(workspaceRoot, target)); !os.IsNotExist(err) {
			t.Fatalf("workspace target %q still exists, err=%v", target, err)
		}
	}
}

func TestWorkspacePurgeDoesNotDeleteOnePasswordDocumentsWhenConfigWriteFails(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env", "config/app.json"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=test\n")))
	appendDocumentConfig(t, tempHome, "config/app.json", "item-2", sha256Hex([]byte("{\"key\":\"value\"}\n")))
	for target, data := range map[string][]byte{
		".env":            []byte("TOKEN=test\n"),
		"config/app.json": []byte("{\"key\":\"value\"}\n"),
	} {
		path := filepath.Join(workspaceRoot, target)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() returned error: %v", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("WriteFile() returned error: %v", err)
		}
	}
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["item-1"] = []byte("TOKEN=test\n")
	runtime.documents["item-2"] = []byte("{\"key\":\"value\"}\n")
	uc := PurgeWorkspace{
		FileSystem: failingStateWriteFS{
			homeDir:        tempHome,
			configWriteErr: errors.New("config write failed"),
		},
		DocumentRuntime: runtime,
		Stdout:          &bytes.Buffer{},
		AssumeYes:       true,
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "config write failed") {
		t.Fatalf("error = %q", err)
	}
	for _, itemID := range []string{"item-1", "item-2"} {
		if _, ok := runtime.documents[itemID]; !ok {
			t.Fatalf("1Password document %q was deleted", itemID)
		}
	}
}

func TestRunTTLCleanerRemovesExpiredCleanOnePasswordMaterializedFile(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=test\n")))
	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	state := domain.DefaultState()
	now := time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC)
	if err := state.UpsertLeaseWithHash("myapp", ".env", now.Add(-2*time.Hour), now.Add(-time.Hour), onePasswordStoreID, targetPath, "item-1", sha256Hex([]byte("TOKEN=test\n"))); err != nil {
		t.Fatalf("UpsertLeaseWithHash() returned error: %v", err)
	}
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	var stdout bytes.Buffer
	uc := RunTTLCleaner{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
		Now:        func() time.Time { return now },
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("workspace target still exists, err=%v", err)
	}
	refreshed := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	if got := len(refreshed.Leases); got != 0 {
		t.Fatalf("lease count = %d, want 0", got)
	}
	if !strings.Contains(stdout.String(), "expired vanished target: myapp/.env") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunTTLCleanerKeepsExpiredModifiedOnePasswordMaterializedFile(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
	appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=old\n")))
	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=new\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	state := domain.DefaultState()
	now := time.Date(2026, 5, 21, 1, 2, 3, 0, time.UTC)
	if err := state.UpsertLeaseWithHash("myapp", ".env", now.Add(-2*time.Hour), now.Add(-time.Hour), onePasswordStoreID, targetPath, "item-1", sha256Hex([]byte("TOKEN=old\n"))); err != nil {
		t.Fatalf("UpsertLeaseWithHash() returned error: %v", err)
	}
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)
	restoreWD := chdirForTest(t, workspaceRoot)
	defer restoreWD()

	var stdout bytes.Buffer
	uc := RunTTLCleaner{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
		Now:        func() time.Time { return now },
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("workspace target was removed: %v", err)
	}
	refreshed := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	if got := len(refreshed.Leases); got != 1 {
		t.Fatalf("lease count = %d, want 1", got)
	}
	if !strings.Contains(stdout.String(), "expired modified target: myapp/.env") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func prepareOnePasswordWorkspace(t *testing.T, tempHome, targetsLine string) string {
	t.Helper()
	workspaceRoot := filepath.Join(tempHome, "myapp")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}
	config := "version = 2\ndefault_ttl = \"24h\"\n\n[store]\nbackend = \"1password_document\"\nvault = \"Private\"\n\n[workspaces.myapp]\nroot = " + workspaceRootQuoted(resolvedWorkspaceRoot) + "\n" + targetsLine + "\n"
	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), config)
	return workspaceRoot
}

func appendDocumentConfig(t *testing.T, tempHome, target, itemID, contentHash string) {
	t.Helper()
	configPath := filepath.Join(tempHome, ".veil", "config.toml")
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(config) returned error: %v", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Fatalf("Close(config) returned error: %v", err)
		}
	}()
	_, err = f.WriteString("\n[[documents]]\nworkspace_id = \"myapp\"\ntarget = \"" + target + "\"\nitem_id = \"" + itemID + "\"\nvault = \"Private\"\ntitle = \"Veil: myapp: " + target + "\"\ncontent_sha256 = \"" + contentHash + "\"\n")
	if err != nil {
		t.Fatalf("WriteString(config) returned error: %v", err)
	}
}

func chdirForTest(t *testing.T, path string) func() {
	t.Helper()
	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() returned error: %v", err)
	}
	if err := os.Chdir(path); err != nil {
		t.Fatalf("Chdir() returned error: %v", err)
	}
	return func() {
		if err := os.Chdir(previousWD); err != nil {
			t.Fatalf("restore Chdir() returned error: %v", err)
		}
	}
}

type fakeOnePasswordRuntime struct {
	documents    map[string][]byte
	deleted      []string
	nextID       int
	createdTitle string
	createdTags  []string
}

type blockingOnePasswordRuntime struct {
	documents     map[string][]byte
	release       chan struct{}
	mu            sync.Mutex
	currentReads  int
	maxConcurrent int
	authCalls     int
	authErr       error
}

type mutatingEditorRunner struct {
	data []byte
}

func (m mutatingEditorRunner) Run(editorPath string, editorArgs []string, targetPath string) error {
	return os.WriteFile(targetPath, m.data, 0o600)
}

func newFakeOnePasswordRuntime() *fakeOnePasswordRuntime {
	return &fakeOnePasswordRuntime{
		documents: map[string][]byte{},
		nextID:    1,
	}
}

func newBlockingOnePasswordRuntime(documents map[string][]byte) *blockingOnePasswordRuntime {
	copied := make(map[string][]byte, len(documents))
	for itemID, data := range documents {
		copied[itemID] = append([]byte(nil), data...)
	}
	return &blockingOnePasswordRuntime{
		documents: copied,
		release:   make(chan struct{}),
	}
}

func (f *fakeOnePasswordRuntime) CreateDocument(vault, title string, tags []string, data []byte) (string, error) {
	itemID := "item-" + string(rune('0'+f.nextID))
	f.nextID++
	f.createdTitle = title
	f.createdTags = append([]string(nil), tags...)
	f.documents[itemID] = append([]byte(nil), data...)
	return itemID, nil
}

func (f *fakeOnePasswordRuntime) ReadDocument(vault, itemID string) ([]byte, error) {
	data := f.documents[itemID]
	return append([]byte(nil), data...), nil
}

func (f *fakeOnePasswordRuntime) UpdateDocument(vault, itemID string, data []byte) error {
	f.documents[itemID] = append([]byte(nil), data...)
	return nil
}

func (f *fakeOnePasswordRuntime) DeleteDocument(vault, itemID string) error {
	delete(f.documents, itemID)
	f.deleted = append(f.deleted, itemID)
	return nil
}

func (b *blockingOnePasswordRuntime) CreateDocument(vault, title string, tags []string, data []byte) (string, error) {
	return "", errors.New("unexpected create document")
}

func (b *blockingOnePasswordRuntime) Authenticate() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.authCalls++
	return b.authErr
}

func (b *blockingOnePasswordRuntime) ReadDocument(vault, itemID string) ([]byte, error) {
	b.mu.Lock()
	b.currentReads++
	if b.currentReads > b.maxConcurrent {
		b.maxConcurrent = b.currentReads
	}
	if b.maxConcurrent >= 2 {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
	}
	b.mu.Unlock()

	select {
	case <-b.release:
	case <-time.After(time.Second):
		return nil, errors.New("timed out waiting for parallel read")
	}
	defer func() {
		b.mu.Lock()
		b.currentReads--
		b.mu.Unlock()
	}()

	b.mu.Lock()
	data := append([]byte(nil), b.documents[itemID]...)
	b.mu.Unlock()
	return data, nil
}

func (b *blockingOnePasswordRuntime) UpdateDocument(vault, itemID string, data []byte) error {
	return errors.New("unexpected update document")
}

func (b *blockingOnePasswordRuntime) DeleteDocument(vault, itemID string) error {
	return errors.New("unexpected delete document")
}

func (b *blockingOnePasswordRuntime) maxConcurrentReads() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.maxConcurrent
}

func (b *blockingOnePasswordRuntime) authenticateCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.authCalls
}
