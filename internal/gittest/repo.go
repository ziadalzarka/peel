// Package gittest builds throwaway git repositories for tests.
//
// Staging is verified against real git rather than a mock: the failure modes
// that matter — corrupt patches, rejected applies, mangled line endings — only
// appear when git itself reads the patch.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Repo is a temporary git repository that is removed when the test ends.
type Repo struct {
	t   *testing.T
	Dir string
}

// New creates an initialised repository with an identity configured, so
// commits work without depending on the developer's global git config.
func New(t *testing.T) *Repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	r := &Repo{t: t, Dir: t.TempDir()}
	r.Git("init", "--quiet", "--initial-branch=main")
	r.Git("config", "user.email", "peel@example.com")
	r.Git("config", "user.name", "peel test")
	// Pin settings the parser and patch builder depend on so a developer's
	// global config cannot change what the tests exercise.
	r.Git("config", "core.autocrlf", "false")
	r.Git("config", "diff.noprefix", "false")
	r.Git("config", "commit.gpgsign", "false")
	return r
}

// Git runs a git command in the repository and returns trimmed stdout, failing
// the test on error.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	out, err := r.git(args...)
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// TryGit runs a git command and returns its output and error without failing.
func (r *Repo) TryGit(args ...string) (string, error) {
	r.t.Helper()
	return r.git(args...)
}

func (r *Repo) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

// Write creates or replaces a file, making parent directories as needed.
func (r *Repo) Write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.Dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatalf("write %s: %v", path, err)
	}
}

// WriteBytes writes raw bytes, for binary fixtures.
func (r *Repo) WriteBytes(path string, content []byte) {
	r.t.Helper()
	full := filepath.Join(r.Dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		r.t.Fatalf("write %s: %v", path, err)
	}
}

// Read returns a file's contents from the working tree.
func (r *Repo) Read(path string) string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.Dir, path))
	if err != nil {
		r.t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// Remove deletes a file from the working tree.
func (r *Repo) Remove(path string) {
	r.t.Helper()
	if err := os.Remove(filepath.Join(r.Dir, path)); err != nil {
		r.t.Fatalf("remove %s: %v", path, err)
	}
}

// Commit stages everything and commits it.
func (r *Repo) Commit(msg string) {
	r.t.Helper()
	r.Git("add", "--all")
	r.Git("commit", "--quiet", "-m", msg)
}

// Staged returns a path's contents as recorded in the index.
func (r *Repo) Staged(path string) string {
	r.t.Helper()
	return r.Git("show", ":"+path)
}

// Head returns a path's contents at HEAD.
func (r *Repo) Head(path string) string {
	r.t.Helper()
	return r.Git("show", "HEAD:"+path)
}

// StagedRaw returns index contents without trimming, for fixtures whose
// trailing newline is the thing under test.
func (r *Repo) StagedRaw(path string) string {
	r.t.Helper()
	cmd := exec.Command("git", "show", ":"+path)
	cmd.Dir = r.Dir
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("git show :%s: %v", path, err)
	}
	return string(out)
}

// StatusLines returns `git status --porcelain` as a sorted slice.
func (r *Repo) StatusLines() []string {
	r.t.Helper()
	out := r.Git("status", "--porcelain")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}
