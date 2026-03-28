package main

import "testing"

func TestMessage(t *testing.T) {
	got := Message()
	want := "Hello, world"

	if got != want {
		t.Fatalf("Message() = %q, want %q", got, want)
	}
}
