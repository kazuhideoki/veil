package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunWithoutArgs(t *testing.T) {
	if err := run(nil, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("run(nil) returned error: %v", err)
	}
}

func TestRunWithArgsReturnsError(t *testing.T) {
	err := run([]string{"emerge"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("run(args) returned nil error")
	}
}

func TestRunInitCreatesConfig(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var stdout bytes.Buffer
	err := run([]string{"init"}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("run(init) returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "initialized config") {
		t.Fatalf("stdout = %q, want init logs", stdout.String())
	}
}

func TestRunInitRejectsExtraArgs(t *testing.T) {
	err := run([]string{"init", "unexpected"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("run(init, extra args) returned nil error")
	}

	if !strings.Contains(err.Error(), "init does not accept positional arguments") {
		t.Fatalf("error = %q", err)
	}
}
