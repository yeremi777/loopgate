package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckGHPassesWhenLoggedIn(t *testing.T) {
	gh := fakeGH(t, "exit 0")
	if err := CheckGH(context.Background(), gh); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestCheckGHNamesLoginWhenLoggedOut(t *testing.T) {
	gh := fakeGH(t, `echo "You are not logged into any GitHub hosts." >&2; exit 1`)
	err := CheckGH(context.Background(), gh)
	if err == nil || !strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("want error naming gh auth login, got %v", err)
	}
}

func TestCheckGHNamesLoginWhenMissing(t *testing.T) {
	err := CheckGH(context.Background(), filepath.Join(t.TempDir(), "gh"))
	if err == nil || !strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("want error naming gh auth login, got %v", err)
	}
}

func TestCheckGHAsksForAuthStatus(t *testing.T) {
	gh := fakeGH(t, `[ "$1 $2" = "auth status" ] || exit 1`)
	if err := CheckGH(context.Background(), gh); err != nil {
		t.Fatalf("want gh auth status call, got %v", err)
	}
}

func fakeGH(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
