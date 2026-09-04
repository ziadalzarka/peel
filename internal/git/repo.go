package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ziadalzarka/peel/internal/exec"
)

// diffFlags pin every option that affects diff output, so what peel parses does
// not depend on the user's git config.
//
//	--no-renames  a rename reads as a delete and an add, which is the diff
//	              there is to review
//	--unified=3   fixed context, so a hunk reads the same in every repository
//	              and a patch generated from one never needs --unidiff-zero
//	--no-textconv textconv output is a rendering, not the change itself
var diffFlags = []string{
	"--no-color",
	"--no-ext-diff",
	"--no-textconv",
	"--no-renames",
	"--unified=3",
}

// Repo runs git commands against one working tree.
type Repo struct {
	dir    string
	runner exec.Runner
}

// NewRepo returns a Repo rooted at dir, using runner to execute git.
func NewRepo(dir string, runner exec.Runner) *Repo {
	return &Repo{dir: dir, runner: runner}
}

// Dir returns the directory git commands run in.
func (r *Repo) Dir() string { return r.dir }

// git runs a git subcommand and returns stdout.
func (r *Repo) git(ctx context.Context, args ...string) (string, error) {
	res, err := r.runner.Run(ctx, exec.Command{Name: "git", Args: args, Dir: r.dir})
	if err != nil {
		return "", err
	}
	return string(res.Stdout), nil
}

// Root returns the absolute path of the working tree root.
func (r *Repo) Root(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// GitDir returns the absolute .git directory for this working tree. In a
// linked worktree this is the worktree's own git dir, which keeps peel's state
// per-worktree rather than shared across all of them.
func (r *Repo) GitDir(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// ConfigSection returns every git config setting in one section, keyed by its
// full key. The usual files are read in the usual order — system, then global,
// then this repository — so a setting made once covers every repository and a
// repository can still narrow it. A section nobody has set is not an error: it
// comes back empty.
func (r *Repo) ConfigSection(ctx context.Context, section string) (map[string]string, error) {
	// --null so a value with a space or a newline in it survives the read: git
	// writes the key, a newline, the value, then a NUL.
	args := []string{"config", "--null", "--get-regexp", "^" + regexp.QuoteMeta(section) + "\\."}
	res, err := r.runner.Run(ctx, exec.Command{Name: "git", Args: args, Dir: r.dir})
	// Exit 1 is "nothing matched", which is the common case rather than a fault.
	if err != nil && !isExitCode(err, 1) {
		return nil, fmt.Errorf("git config %s: %w", section, err)
	}

	out := map[string]string{}
	for _, record := range strings.Split(string(res.Stdout), "\x00") {
		if record == "" {
			continue
		}
		key, value, _ := strings.Cut(record, "\n")
		out[key] = value
	}
	return out, nil
}

// Unstaged returns changes between the index and the working tree — what
// staging moves into the index.
func (r *Repo) Unstaged(ctx context.Context) (Diff, error) {
	out, err := r.git(ctx, append([]string{"diff"}, diffFlags...)...)
	if err != nil {
		return Diff{}, fmt.Errorf("git diff: %w", err)
	}
	return ParseDiff(out)
}

// Staged returns changes between HEAD and the index — what unstaging removes.
func (r *Repo) Staged(ctx context.Context) (Diff, error) {
	args := append([]string{"diff", "--cached"}, diffFlags...)
	out, err := r.git(ctx, args...)
	if err != nil {
		return Diff{}, fmt.Errorf("git diff --cached: %w", err)
	}
	return ParseDiff(out)
}

// ResolveCommit turns a revision — a hash, a branch, HEAD~2 — into the commit
// it names. Callers resolve once and work with the hash afterwards, so a
// session stays on the commit it opened on even when the ref moves underneath
// it.
func (r *Repo) ResolveCommit(ctx context.Context, ref string) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("unknown revision %q", ref)
	}
	sha := strings.TrimSpace(out)
	if sha == "" {
		return "", fmt.Errorf("unknown revision %q", ref)
	}
	return sha, nil
}

// ChangesSince returns everything that changed between a commit and the working
// tree: commits made since then and uncommitted work alike.
func (r *Repo) ChangesSince(ctx context.Context, commit string) (Diff, error) {
	out, err := r.ChangesSinceText(ctx, commit)
	if err != nil {
		return Diff{}, err
	}
	return ParseDiff(out)
}

// ChangesSinceText is ChangesSince as raw text, for a walkthrough.
func (r *Repo) ChangesSinceText(ctx context.Context, commit string) (string, error) {
	args := append([]string{"diff"}, diffFlags...)
	args = append(args, commit)
	out, err := r.git(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("git diff %s: %w", commit, err)
	}
	return out, nil
}

// AllChangesText returns the raw diff between HEAD and the working tree —
// everything uncommitted, staged or not. It is the input to a walkthrough,
// where the split between index and working tree is not interesting.
func (r *Repo) AllChangesText(ctx context.Context) (string, error) {
	args := append([]string{"diff"}, diffFlags...)
	if r.HasHead(ctx) {
		args = append(args, "HEAD")
	} else {
		args = append(args, emptyTree)
	}

	out, err := r.git(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("git diff HEAD: %w", err)
	}
	return out, nil
}

// Untracked lists files git does not track and .gitignore does not exclude.
func (r *Repo) Untracked(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// UntrackedDiff synthesizes an all-additions diff for an untracked file without
// touching the index, so its contents are reviewable before it is staged.
func (r *Repo) UntrackedDiff(ctx context.Context, path string) (FileDiff, error) {
	// --no-index compares two paths outside git's control and exits 1 when they
	// differ, which is the normal case here rather than an error.
	args := append([]string{"diff", "--no-index"}, diffFlags...)
	args = append(args, "--", "/dev/null", path)

	res, err := r.runner.Run(ctx, exec.Command{Name: "git", Args: args, Dir: r.dir})
	if err != nil && !isExitCode(err, 1) {
		return FileDiff{}, fmt.Errorf("git diff --no-index %s: %w", path, err)
	}

	d, err := ParseDiff(string(res.Stdout))
	if err != nil {
		return FileDiff{}, fmt.Errorf("%s: %w", path, err)
	}
	if len(d.Files) == 0 {
		return FileDiff{}, fmt.Errorf("%s: produced no diff", path)
	}

	f := d.Files[0]
	f.Status = StatusAdded
	f.OldPath = path
	f.NewPath = path
	return f, nil
}

// WorkingLines returns the working tree's copy of path, line by line.
//
// It is what a diff against the index is measured against, so the unchanged
// code a hunk's three lines of context leave out can be read straight out of it
// rather than asked of git a second time.
func (r *Repo) WorkingLines(path string) ([]string, error) {
	content, err := os.ReadFile(filepath.Join(r.dir, path))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return splitLines(string(content)), nil
}

// IndexLines returns the copy git holds staged for path, line by line — what
// the staged half of a part-staged file is measured against, and a different
// file from the one on disk.
func (r *Repo) IndexLines(ctx context.Context, path string) ([]string, error) {
	res, err := r.runner.Run(ctx, exec.Command{
		Name: "git",
		Args: []string{"cat-file", "blob", ":" + path},
		Dir:  r.dir,
	})
	if err != nil {
		return nil, fmt.Errorf("read staged %s: %w", path, err)
	}
	return splitLines(string(res.Stdout)), nil
}

// UnstagedFile returns one path's working-tree changes — `git diff` narrowed to
// the file — and false where it has none.
//
// It is what staging one hunk resolves that hunk against, so the read behind a
// keypress costs the file the reviewer is in rather than the whole tree.
func (r *Repo) UnstagedFile(ctx context.Context, path string) (FileDiff, bool, error) {
	args := append([]string{"diff"}, diffFlags...)
	args = append(args, "--", path)
	out, err := r.git(ctx, args...)
	if err != nil {
		return FileDiff{}, false, fmt.Errorf("git diff -- %s: %w", path, err)
	}
	d, err := ParseDiff(out)
	if err != nil {
		return FileDiff{}, false, fmt.Errorf("%s: %w", path, err)
	}
	if len(d.Files) == 0 {
		return FileDiff{}, false, nil
	}
	return d.Files[0], true, nil
}

// IsUntracked reports whether git is not tracking path.
//
// It is asked only where a path turns out to have no diff, to tell "there is
// nothing left to stage" from "git has never heard of this file" — two different
// things to say, and only one of them worth a second git call.
func (r *Repo) IsUntracked(ctx context.Context, path string) bool {
	out, err := r.git(ctx, "ls-files", "--others", "--exclude-standard", "--", path)
	return err == nil && strings.TrimSpace(out) != ""
}

// ApplyToIndex pipes a patch into the index and stops there, leaving the
// working tree the patch was read out of alone.
//
// --unidiff-zero is deliberately not passed: it turns off git's overlap checks,
// and it is only needed for patches with no context, which diffFlags rules out.
func (r *Repo) ApplyToIndex(ctx context.Context, patch string) error {
	_, err := r.runner.Run(ctx, exec.Command{
		Name:  "git",
		Args:  []string{"apply", "--cached", "--whitespace=nowarn", "-"},
		Dir:   r.dir,
		Stdin: strings.NewReader(patch),
	})
	if err != nil {
		return fmt.Errorf("apply to the index: %w", err)
	}
	return nil
}

// StageFile stages every change to one path, including deletions and untracked
// files.
func (r *Repo) StageFile(ctx context.Context, path string) error {
	if _, err := r.git(ctx, "add", "--", path); err != nil {
		return fmt.Errorf("stage %s: %w", path, err)
	}
	return nil
}

// UnstageFile removes every staged change to one path, leaving the working tree
// untouched.
func (r *Repo) UnstageFile(ctx context.Context, path string) error {
	if _, err := r.git(ctx, "restore", "--staged", "--", path); err != nil {
		return fmt.Errorf("unstage %s: %w", path, err)
	}
	return nil
}

// HasHead reports whether the repository has at least one commit. An unborn
// HEAD makes `git diff --cached` fail, so callers check this first.
func (r *Repo) HasHead(ctx context.Context) bool {
	_, err := r.git(ctx, "rev-parse", "--verify", "HEAD")
	return err == nil
}

// isExitCode reports whether err is a command failure with the given status.
func isExitCode(err error, code int) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode == code
	}
	return false
}
