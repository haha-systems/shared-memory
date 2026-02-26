package config

import (
	"strings"
	"testing"
)

func TestExpandPath(t *testing.T) {
	t.Parallel()
	got := ExpandPath("~/memory.db")
	if got == "~/memory.db" {
		t.Fatalf("expected home-expanded path, got %q", got)
	}
	if !strings.Contains(got, "memory.db") {
		t.Fatalf("expected expanded path to contain file name, got %q", got)
	}
}
