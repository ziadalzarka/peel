package git

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ziadalzarka/peel/internal/exec"
)

// diffFlags pin every option that affects diff output, so what peel parses does
// not depend on the user's git config.
//
//	--no-renames  rename headers are applied inconsistently by `git apply`
//	--unified=3   fixed context, so generated patches never need --unidiff-zero
//	--no-textconv textconv output is not a patch that can be applied back
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

// Apply pipes a patch into the index. Direction selects whether it is applied
// as written or reversed.
//
// --unidiff-zero is deliberately not passed: it disables git's overlap checks
// and is only required for zero-context patches, which diffFlags rules out.
func (r *Repo) Apply(ctx context.Context, patch string, dir Direction) error {
	args := []string{"apply", "--cached", "--whitespace=nowarn"}
	if dir == Reverse {
		args = append(args, "--reverse")
	}
	args = append(args, "-")

	_, err := r.runner.Run(ctx, exec.Command{
		Name:  "git",
		Args:  args,
		Dir:   r.dir,
		Stdin: strings.NewReader(patch),
	})
	if err != nil {
		return fmt.Errorf("apply patch (%s): %w", dir, err)
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

// IntentToAdd registers an untracked file with the index without staging its
// contents, which is what makes its hunks addressable by `git apply --cached`.
func (r *Repo) IntentToAdd(ctx context.Context, path string) error {
	if _, err := r.git(ctx, "add", "--intent-to-add", "--", path); err != nil {
		return fmt.Errorf("intent-to-add %s: %w", path, err)
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
