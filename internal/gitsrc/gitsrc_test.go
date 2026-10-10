package gitsrc

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestShowAndMergeBase(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	path := filepath.Join(dir, "schemas", "a.avsc")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "base")
	git(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(path, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "commit", "-qam", "head")

	r, err := Open(filepath.Join(dir, "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	base, err := r.Resolve("main")
	if err != nil {
		t.Fatal(err)
	}
	mb, err := r.MergeBase(base, "HEAD")
	if err != nil || mb != base {
		t.Fatalf("merge base = %s, %v", mb, err)
	}
	got, err := r.Show(base, path)
	if err != nil || string(got) != "v1" {
		t.Fatalf("show = %q, %v", got, err)
	}
	if _, err := r.Show(base, filepath.Join(dir, "schemas", "new.avsc")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file err = %v", err)
	}
	if _, err := r.Show(base, "/etc/passwd"); err == nil {
		t.Fatal("outside repo should fail")
	}
	if _, err := r.Resolve("no-such-branch"); err == nil {
		t.Fatal("unknown ref should fail")
	}
	if _, err := Open(t.TempDir()); err == nil {
		t.Fatal("non-repo should fail")
	}
}
