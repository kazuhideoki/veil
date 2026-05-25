package usecase

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kazuhideoki/veil/internal/infra"
)

func TestTTLAgentInstallWritesPlistAndBootstrapsLaunchAgent(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	runner := &recordingCommandRunner{}
	var stdout bytes.Buffer
	executablePath := writeExecutableForTest(t, homeDir, "veil&bin")

	uc := TTLAgent{
		FileSystem:      infra.OSFileSystem{},
		CommandRunner:   runner,
		Stdout:          &stdout,
		ExecutablePath:  executablePath,
		UserID:          "501",
		Label:           "com.example.veil.ttl-test",
		IntervalSeconds: 10,
	}

	if err := uc.Install(); err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.example.veil.ttl-test.plist")
	plistData, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("ReadFile() returned error: %v", err)
	}
	plist := string(plistData)
	for _, want := range []string{
		"<string>com.example.veil.ttl-test</string>",
		"<string>" + strings.ReplaceAll(executablePath, "&", "&amp;") + "</string>",
		"<string>ttl-cleaner</string>",
		"<key>HOME</key>",
		"<string>" + homeDir + "</string>",
		"<integer>10</integer>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist does not contain %q:\n%s", want, plist)
		}
	}
	if got, want := strings.Join(runner.calls, "\n"), strings.Join([]string{
		"launchctl bootout gui/501/com.example.veil.ttl-test",
		"launchctl bootstrap gui/501 " + plistPath,
		"launchctl kickstart -k gui/501/com.example.veil.ttl-test",
	}, "\n"); got != want {
		t.Fatalf("commands = %q, want %q", got, want)
	}
	if !strings.Contains(stdout.String(), "installed ttl agent: com.example.veil.ttl-test") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestTTLAgentUninstallRemovesPlist(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.example.veil.ttl-test.plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("plist"), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	runner := &recordingCommandRunner{errorsByPrefix: map[string]error{
		"launchctl bootout": errors.New("not loaded"),
	}}

	uc := TTLAgent{
		FileSystem:    infra.OSFileSystem{},
		CommandRunner: runner,
		Stdout:        &bytes.Buffer{},
		UserID:        "501",
		Label:         "com.example.veil.ttl-test",
	}

	if err := uc.Uninstall(); err != nil {
		t.Fatalf("Uninstall() returned error: %v", err)
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("plist still exists, err=%v", err)
	}
}

func TestTTLAgentEnsureInstalledSkipsWhenLoaded(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	executablePath := writeExecutableForTest(t, homeDir, "veil")
	uc := TTLAgent{
		FileSystem:      infra.OSFileSystem{},
		Stdout:          &bytes.Buffer{},
		ExecutablePath:  executablePath,
		UserID:          "501",
		Label:           "com.example.veil.ttl-test",
		IntervalSeconds: 10,
	}
	if err := uc.Install(); err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	runner := &recordingCommandRunner{}
	uc.CommandRunner = runner

	if err := uc.EnsureInstalled(); err != nil {
		t.Fatalf("EnsureInstalled() returned error: %v", err)
	}
	if got, want := strings.Join(runner.calls, "\n"), "launchctl print gui/501/com.example.veil.ttl-test"; got != want {
		t.Fatalf("commands = %q, want %q", got, want)
	}
}

func TestTTLAgentEnsureInstalledRepairsStaleLoadedPlist(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	executablePath := writeExecutableForTest(t, homeDir, "veil")
	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.example.veil.ttl-test.plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("stale plist"), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	runner := &recordingCommandRunner{}

	uc := TTLAgent{
		FileSystem:      infra.OSFileSystem{},
		CommandRunner:   runner,
		Stdout:          &bytes.Buffer{},
		ExecutablePath:  executablePath,
		UserID:          "501",
		Label:           "com.example.veil.ttl-test",
		IntervalSeconds: 10,
	}

	if err := uc.EnsureInstalled(); err != nil {
		t.Fatalf("EnsureInstalled() returned error: %v", err)
	}
	if got, want := strings.Join(runner.calls, "\n"), strings.Join([]string{
		"launchctl bootout gui/501/com.example.veil.ttl-test",
		"launchctl bootstrap gui/501 " + plistPath,
		"launchctl kickstart -k gui/501/com.example.veil.ttl-test",
	}, "\n"); got != want {
		t.Fatalf("commands = %q, want %q", got, want)
	}
	plistData, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("ReadFile() returned error: %v", err)
	}
	if strings.Contains(string(plistData), "stale plist") {
		t.Fatalf("plist was not repaired: %s", plistData)
	}
}

func TestTTLAgentEnsureInstalledInstallsWhenNotLoaded(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	executablePath := writeExecutableForTest(t, homeDir, "veil")
	uc := TTLAgent{
		FileSystem:      infra.OSFileSystem{},
		Stdout:          &bytes.Buffer{},
		ExecutablePath:  executablePath,
		UserID:          "501",
		Label:           "com.example.veil.ttl-test",
		IntervalSeconds: 10,
	}
	if err := uc.Install(); err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	runner := &recordingCommandRunner{errorsByExactCall: map[string]error{
		"launchctl print gui/501/com.example.veil.ttl-test": errors.New("not loaded"),
	}}
	uc.CommandRunner = runner

	if err := uc.EnsureInstalled(); err != nil {
		t.Fatalf("EnsureInstalled() returned error: %v", err)
	}
	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.example.veil.ttl-test.plist")
	if _, err := os.Stat(plistPath); err != nil {
		t.Fatalf("plist was not written: %v", err)
	}
	if got, want := strings.Join(runner.calls, "\n"), strings.Join([]string{
		"launchctl print gui/501/com.example.veil.ttl-test",
		"launchctl bootout gui/501/com.example.veil.ttl-test",
		"launchctl bootstrap gui/501 " + plistPath,
		"launchctl kickstart -k gui/501/com.example.veil.ttl-test",
	}, "\n"); got != want {
		t.Fatalf("commands = %q, want %q", got, want)
	}
}

func TestTTLAgentEnsureInstalledRejectsMissingExecutable(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	uc := TTLAgent{
		FileSystem:      infra.OSFileSystem{},
		CommandRunner:   &recordingCommandRunner{},
		Stdout:          &bytes.Buffer{},
		ExecutablePath:  filepath.Join(homeDir, "missing-veil"),
		UserID:          "501",
		Label:           "com.example.veil.ttl-test",
		IntervalSeconds: 10,
	}

	err := uc.EnsureInstalled()
	if err == nil {
		t.Fatal("EnsureInstalled() returned nil error")
	}
	if !strings.Contains(err.Error(), "stat ttl-agent executable") {
		t.Fatalf("error = %q", err)
	}
}

func TestTTLAgentStatusReportsInstalledAndLoaded(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.example.veil.ttl-test.plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("plist"), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	var stdout bytes.Buffer

	uc := TTLAgent{
		FileSystem:    infra.OSFileSystem{},
		CommandRunner: &recordingCommandRunner{},
		Stdout:        &stdout,
		UserID:        "501",
		Label:         "com.example.veil.ttl-test",
	}

	if err := uc.Status(); err != nil {
		t.Fatalf("Status() returned error: %v", err)
	}
	for _, want := range []string{
		"ttl agent: com.example.veil.ttl-test",
		"installed: yes",
		"loaded: yes",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout does not contain %q: %q", want, stdout.String())
		}
	}
}

type recordingCommandRunner struct {
	calls             []string
	errorsByPrefix    map[string]error
	errorsByExactCall map[string]error
}

func (r *recordingCommandRunner) Run(command string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{command}, args...), " ")
	r.calls = append(r.calls, call)
	if err, ok := r.errorsByExactCall[call]; ok {
		return []byte(err.Error()), err
	}
	for prefix, err := range r.errorsByPrefix {
		if strings.HasPrefix(call, prefix) {
			return []byte(err.Error()), err
		}
	}
	return []byte("ok"), nil
}

func writeExecutableForTest(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	return path
}
