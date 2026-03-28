package main

import "testing"

func TestRunWithoutArgs(t *testing.T) {
	if err := run(nil); err != nil {
		t.Fatalf("run(nil) returned error: %v", err)
	}
}

func TestRunWithArgsReturnsError(t *testing.T) {
	err := run([]string{"emerge"})
	if err == nil {
		t.Fatal("run(args) returned nil error")
	}
}
