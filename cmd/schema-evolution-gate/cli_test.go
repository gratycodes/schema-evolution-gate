package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gratycodes/schema-evolution-gate/internal/report"
)

var update = flag.Bool("update", false, "rewrite golden files")

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// fixtureRepo builds the same repo as scripts/make-fixture-repo.sh: base
// schemas committed on main, the pull request's changes on branch pr.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copyTree(t, "../../testdata/fixture/base", dir)
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "base schemas")
	git(t, dir, "checkout", "-q", "-b", "pr")
	copyTree(t, "../../testdata/fixture/head", dir)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "pr: evolve schemas")
	return dir
}

var sha = regexp.MustCompile(`\(([0-9a-f]{12})\)`)

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	got = sha.ReplaceAll(got, []byte("(<base-sha>)"))
	path := filepath.Join("../../testdata/golden", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./cmd/... -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from golden file; run go test ./cmd/... -update and review the diff\n--- got ---\n%s", name, got)
	}
}

func TestCheckFixtureGolden(t *testing.T) {
	repo := fixtureRepo(t)
	cfg := filepath.Join(repo, ".schema-gate.yaml")
	mdPath := filepath.Join(t.TempDir(), "comment.md")
	jsonPath := filepath.Join(t.TempDir(), "report.json")

	for _, tc := range []struct{ output, golden string }{
		{"table", "fixture.txt"},
		{"markdown", "fixture.md"},
		{"json", "fixture.json"},
	} {
		t.Run(tc.output, func(t *testing.T) {
			args := []string{"check", "--config", cfg, "--base", "main", "--output", tc.output}
			if tc.output == "table" {
				args = append(args, "--markdown-out", mdPath, "--json-out", jsonPath)
			}
			code, stdout, stderr := runCLIWithVersion(t, args...)
			if code != exitBreaking {
				t.Fatalf("exit = %d, want %d; stderr: %s", code, exitBreaking, stderr)
			}
			golden(t, tc.golden, []byte(stdout))
		})
	}
	md, err := os.ReadFile(mdPath)
	if err != nil || !strings.HasPrefix(string(md), report.Marker) {
		t.Fatalf("--markdown-out: %v %q", err, md)
	}
	var parsed struct {
		Breaking     int  `json:"breaking"`
		FailedTopics int  `json:"failedTopics"`
		Passed       bool `json:"passed"`
	}
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Breaking != 3 || parsed.FailedTopics != 2 || parsed.Passed {
		t.Fatalf("json summary = %+v", parsed)
	}
}

// runCLIWithVersion pins the version string before rendering.
func runCLIWithVersion(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	versionOverride = "test"
	defer func() { versionOverride = "" }()
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestCheckCompatibleChangePasses(t *testing.T) {
	repo := fixtureRepo(t)
	// Revert the breaking changes; only the compatible customers change stays.
	git(t, repo, "checkout", "-q", "main", "--", "schemas/orders", "schemas/payments")
	code, stdout, stderr := runCLIWithVersion(t, "check", "--config", filepath.Join(repo, ".schema-gate.yaml"), "--base", "main")
	if code != exitOK {
		t.Fatalf("exit = %d\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "passed: 1 changed topic checked") {
		t.Fatalf("stdout = %s", stdout)
	}
	// --all re-checks unchanged topics too.
	code, stdout, _ = runCLIWithVersion(t, "check", "--config", filepath.Join(repo, ".schema-gate.yaml"), "--base", "main", "--all")
	if code != exitOK || strings.Contains(stdout, "UNCHANGED") {
		t.Fatalf("--all: exit %d\n%s", code, stdout)
	}
}

func TestCheckWithoutGitUsesHistoryOnly(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, "../../testdata/fixture/base", dir)
	copyTree(t, "../../testdata/fixture/head", dir)
	t.Setenv("GITHUB_BASE_REF", "")
	code, stdout, _ := runCLIWithVersion(t, "check", "--config", filepath.Join(dir, ".schema-gate.yaml"), "--output", "json")
	if code != exitBreaking {
		t.Fatalf("exit = %d\n%s", code, stdout)
	}
	// orders has a history file, so it is still checked; the others are new.
	if !strings.Contains(stdout, `"status": "new"`) || !strings.Contains(stdout, "order-placed.v1.avsc") {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestCompareAndReplayCommands(t *testing.T) {
	fx := "../../testdata/fixture"
	code, stdout, _ := runCLIWithVersion(t, "compare",
		"--old", fx+"/base/schemas/orders/history/order-placed.v1.avsc",
		"--old", fx+"/base/schemas/orders/order-placed.avsc",
		"--new", fx+"/head/schemas/orders/order-placed.avsc", "--mode", "BACKWARD")
	if code != exitBreaking || !strings.Contains(stdout, "READER_FIELD_MISSING_DEFAULT_VALUE") || strings.Contains(stdout, "forward") {
		t.Fatalf("compare: %d\n%s", code, stdout)
	}
	code, stdout, _ = runCLIWithVersion(t, "compare",
		"--old", fx+"/base/schemas/customers/customer-updated.schema.json",
		"--new", fx+"/head/schemas/customers/customer-updated.schema.json", "--mode", "backward", "--output", "json")
	if code != exitOK || !strings.Contains(stdout, `"passed": true`) {
		t.Fatalf("compare json: %d\n%s", code, stdout)
	}
	// Same change under FULL: an old consumer with additionalProperties: false
	// rejects the new property, exactly as in Confluent's closed-content-model table.
	code, stdout, _ = runCLIWithVersion(t, "compare",
		"--old", fx+"/base/schemas/customers/customer-updated.schema.json",
		"--new", fx+"/head/schemas/customers/customer-updated.schema.json", "--mode", "full")
	if code != exitBreaking || !strings.Contains(stdout, "PROPERTY_REMOVED_FROM_CLOSED_CONTENT_MODEL") || !strings.Contains(stdout, "forward") {
		t.Fatalf("compare full: %d\n%s", code, stdout)
	}
	code, stdout, _ = runCLIWithVersion(t, "replay",
		"--schema", fx+"/head/schemas/payments/payment-captured.schema.json",
		"--base", fx+"/base/schemas/payments/payment-captured.schema.json",
		"--samples", fx+"/base/samples/payments", "--output", "markdown")
	if code != exitBreaking || !strings.Contains(stdout, "`/risk_score`") {
		t.Fatalf("replay: %d\n%s", code, stdout)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"nope"},
		{"check", "--config", "/does/not/exist.yaml"},
		{"check", "--config", "../../testdata/fixture/base/.schema-gate.yaml", "--base", "no-such-ref"},
		{"check", "--config", "../../testdata/fixture/base/.schema-gate.yaml", "--output", "xml"},
		{"compare", "--new", "x.avsc"},
		{"compare", "--old", "a.avsc", "--new", "b.avsc", "--mode", "SIDEWAYS"},
		{"compare", "--old", "a.proto", "--new", "b.proto"},
		{"replay", "--schema", "x.avsc"},
		{"replay", "--schema", "x.avsc", "--samples", ".", "--encoding", "xml"},
		{"check", "--bogus-flag"},
	} {
		if code, _, _ := runCLIWithVersion(t, args...); code != exitUsage {
			t.Errorf("%v: exit = %d, want %d", args, code, exitUsage)
		}
	}
	if code, out, _ := runCLIWithVersion(t, "version"); code != exitOK || strings.TrimSpace(out) != "test" {
		t.Errorf("version: %d %q", code, out)
	}
	if code, out, _ := runCLIWithVersion(t, "help"); code != exitOK || !strings.Contains(out, "Usage:") {
		t.Errorf("help: %d", code)
	}
}
