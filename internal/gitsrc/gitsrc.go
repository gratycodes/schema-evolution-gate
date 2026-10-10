// Package gitsrc reads the base-branch version of files with the git CLI, so
// the gate compares exactly what a pull request changes without needing a
// Schema Registry.
package gitsrc

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is a git work tree.
type Repo struct {
	Root string
}

func run(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Open finds the work tree containing dir.
func Open(dir string) (*Repo, error) {
	out, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not inside a git work tree: %w", err)
	}
	root := strings.TrimSpace(string(out))
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return &Repo{Root: root}, nil
}

// Resolve turns a ref into a full commit SHA.
func (r *Repo) Resolve(ref string) (string, error) {
	out, err := run(r.Root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("unknown git ref %q (with actions/checkout, set fetch-depth: 0)", ref)
	}
	return strings.TrimSpace(string(out)), nil
}

// MergeBase returns the merge base of two commits; this is what GitHub diffs
// a pull request against.
func (r *Repo) MergeBase(a, b string) (string, error) {
	out, err := run(r.Root, "merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ErrNotFound means the file does not exist at that commit.
var ErrNotFound = errors.New("file not found at ref")

// Show returns the content of path (absolute, inside the work tree) at commit.
func (r *Repo) Show(commit, path string) ([]byte, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		abs = filepath.Join(resolved, filepath.Base(abs))
	}
	rel, err := filepath.Rel(r.Root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("%s is outside the repository", path)
	}
	// cat-file -e first so "missing at base" (a new subject) is not an error.
	if _, err := run(r.Root, "cat-file", "-e", commit+":"+filepath.ToSlash(rel)); err != nil {
		return nil, ErrNotFound
	}
	return run(r.Root, "show", commit+":"+filepath.ToSlash(rel))
}
