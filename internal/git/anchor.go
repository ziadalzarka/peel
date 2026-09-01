package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ziadalzarka/peel/internal/exec"
)

// AnchorRefPrefix is the ref namespace peel keeps comment snapshots alive in.
//
// A blob written with `git hash-object -w` that nothing points at is garbage the
// next `git gc` collects, so a snapshot held by no ref is a snapshot that
// quietly stops existing — and an anchor that rots is the bug it was added to
// fix, arriving later and harder to explain. One ref per blob is the whole
// mechanism, and it is also the whole cleanup story: the ref set is kept equal
// to the blobs the open comments name, so dropping a comment drops its ref and
// hands the blob back to git's own collector. peel never runs gc in someone
// else's repository; it only stops holding on.
const AnchorRefPrefix = "refs/peel/anchors/"

// anchorDiffFlags pin the diff peel reads a line mapping out of. Zero context
// because only the changed ranges matter here, and the text of the change never
// is: what is wanted is where line 42 went.
var anchorDiffFlags = []string{"--no-color", "--no-ext-diff", "--no-textconv", "--unified=0"}

// LineMap moves a line number from one version of a file to another.
//
// It is git's own diff, read as arithmetic: the ranges it reports are the only
// places numbering shifts, so a line before the first of them keeps its number,
// a line after one moves by however much that range grew or shrank, and a line
// *inside* one is a line that was rewritten and has nowhere to land.
type LineMap struct{ edits []edit }

// edit is one changed range: oldCount lines starting at oldStart became
// newCount lines. A pure insertion has oldCount 0, and oldStart is then the line
// it was inserted after.
type edit struct{ oldStart, oldCount, newCount int }

// Lookup returns where line sits in the new version, and whether it still
// exists there at all.
func (m LineMap) Lookup(line int) (int, bool) {
	if line <= 0 {
		return line, false
	}
	shift := 0
	for _, e := range m.edits {
		if e.oldCount == 0 {
			// Nothing was replaced, so the line the insertion follows keeps its
			// own number and only what comes after it moves.
			if line <= e.oldStart {
				return line + shift, true
			}
		} else {
			if line < e.oldStart {
				return line + shift, true
			}
			if line < e.oldStart+e.oldCount {
				return 0, false
			}
		}
		shift += e.newCount - e.oldCount
	}
	return line + shift, true
}

// Identity reports whether the mapping leaves every line where it is.
func (m LineMap) Identity() bool { return len(m.edits) == 0 }

// SnapshotFile writes the working tree's copy of path into the object store and
// returns the blob's hash, so a line number recorded against it has something
// that cannot change to be a number in.
func (r *Repo) SnapshotFile(ctx context.Context, path string) (string, error) {
	out, err := r.git(ctx, "hash-object", "-w", "--", path)
	if err != nil {
		return "", fmt.Errorf("snapshot %s: %w", path, err)
	}
	return strings.TrimSpace(out), nil
}

// IndexBlob returns the blob the index holds for path.
func (r *Repo) IndexBlob(ctx context.Context, path string) (string, error) {
	return r.revBlob(ctx, ":"+path)
}

// TreeBlob returns the blob rev holds for path. An empty rev means HEAD.
func (r *Repo) TreeBlob(ctx context.Context, rev, path string) (string, error) {
	if rev == "" {
		rev = "HEAD"
	}
	return r.revBlob(ctx, rev+":"+path)
}

func (r *Repo) revBlob(ctx context.Context, spec string) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--verify", "--quiet", spec)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", spec, err)
	}
	return strings.TrimSpace(out), nil
}

// Blobs resolves many object names at once, keyed by the name asked for. A name
// this repository does not hold is left out rather than failing the rest.
//
// A review with notes on a dozen files asks a dozen of these on every re-read,
// and in follow mode that is every couple of seconds. One process answering all
// of them costs what one of them used to.
//
// A name with a newline in it is left out: the batch is line-delimited, so such
// a name would answer for the one after it.
func (r *Repo) Blobs(ctx context.Context, specs []string) (map[string]string, error) {
	ask := make([]string, 0, len(specs))
	for _, spec := range specs {
		if spec != "" && !strings.ContainsAny(spec, "\n\x00") {
			ask = append(ask, spec)
		}
	}
	if len(ask) == 0 {
		return nil, nil
	}

	res, err := r.runner.Run(ctx, exec.Command{
		Name:  "git",
		Args:  []string{"cat-file", "--batch-check"},
		Dir:   r.dir,
		Stdin: strings.NewReader(strings.Join(ask, "\n") + "\n"),
	})
	if err != nil {
		return nil, fmt.Errorf("resolve %d objects: %w", len(ask), err)
	}

	// git answers one line per line asked, in order, so the reply is paired with
	// the question by position. A name it does not hold answers "<name> missing"
	// and simply goes unanswered here.
	lines := strings.Split(strings.TrimSuffix(string(res.Stdout), "\n"), "\n")
	if len(lines) != len(ask) {
		return nil, fmt.Errorf("resolve %d objects: git answered %d", len(ask), len(lines))
	}
	out := make(map[string]string, len(ask))
	for i, line := range lines {
		name, kind, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !strings.HasPrefix(kind, "blob ") {
			continue
		}
		out[ask[i]] = name
	}
	return out, nil
}

// HashFiles returns the hash each path's working-tree copy would have as a git
// object, without writing any of them.
//
// It is how a note's snapshot is told apart from the file on disk without
// diffing them: equal hashes mean equal content, and content that has not moved
// needs no mapping at all. One process covers every path, which is the point —
// the diff it saves is two processes each.
//
// A path that cannot be read is left out; git stops at the first one it cannot
// hash, so they are dropped before it is asked.
func (r *Repo) HashFiles(ctx context.Context, paths []string) (map[string]string, error) {
	ask := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" || strings.ContainsAny(path, "\n\x00") {
			continue
		}
		if info, err := os.Stat(filepath.Join(r.dir, path)); err != nil || !info.Mode().IsRegular() {
			continue
		}
		ask = append(ask, path)
	}
	if len(ask) == 0 {
		return nil, nil
	}

	res, err := r.runner.Run(ctx, exec.Command{
		Name:  "git",
		Args:  []string{"hash-object", "--stdin-paths"},
		Dir:   r.dir,
		Stdin: strings.NewReader(strings.Join(ask, "\n") + "\n"),
	})
	if err != nil {
		return nil, fmt.Errorf("hash %d files: %w", len(ask), err)
	}

	lines := strings.Split(strings.TrimSuffix(string(res.Stdout), "\n"), "\n")
	if len(lines) != len(ask) {
		return nil, fmt.Errorf("hash %d files: git answered %d", len(ask), len(lines))
	}
	out := make(map[string]string, len(ask))
	for i, line := range lines {
		if hash := strings.TrimSpace(line); hash != "" {
			out[ask[i]] = hash
		}
	}
	return out, nil
}

// MapLinesBetween returns how numbering moves from one blob to another. Both are
// already in the object store, so git diffs them directly and nothing is written.
func (r *Repo) MapLinesBetween(ctx context.Context, from, to string) (LineMap, error) {
	if from == to {
		return LineMap{}, nil
	}
	args := append(append([]string{"diff"}, anchorDiffFlags...), from, to)
	out, err := r.git(ctx, args...)
	if err != nil {
		return LineMap{}, fmt.Errorf("diff %s %s: %w", from, to, err)
	}
	return parseLineMap(out)
}

// MapLinesOnto returns how numbering moves from a blob to the working tree's
// copy of path.
//
// The blob's content goes to a temporary file and git diffs that against the
// file on disk, rather than the working tree being hashed into a second blob:
// this runs on every re-read, and a review left open in follow mode would
// otherwise write an object per keystroke — loose garbage in someone else's
// repository, for a mapping that is thrown away as soon as it is used.
func (r *Repo) MapLinesOnto(ctx context.Context, from, path string) (LineMap, error) {
	content, err := r.catBlob(ctx, from)
	if err != nil {
		return LineMap{}, err
	}

	tmp, err := os.CreateTemp("", "peel-anchor-*")
	if err != nil {
		return LineMap{}, fmt.Errorf("stage anchor %s: %w", from, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return LineMap{}, fmt.Errorf("stage anchor %s: %w", from, err)
	}
	if err := tmp.Close(); err != nil {
		return LineMap{}, fmt.Errorf("stage anchor %s: %w", from, err)
	}

	args := append(append([]string{"diff"}, anchorDiffFlags...), "--no-index", "--", tmp.Name(), filepath.Join(r.dir, path))
	// Run the command directly rather than through git(): --no-index reports a
	// difference as exit 1, which is the ordinary answer here and not a failure,
	// and git() drops stdout whenever the status is non-zero — which would leave
	// every moved line looking like a line that had not moved.
	res, err := r.runner.Run(ctx, exec.Command{Name: "git", Args: args, Dir: r.dir})
	if err != nil && !isExitCode(err, 1) {
		return LineMap{}, fmt.Errorf("diff %s against %s: %w", from, path, err)
	}
	return parseLineMap(string(res.Stdout))
}

// catBlob returns a blob's bytes.
func (r *Repo) catBlob(ctx context.Context, blob string) ([]byte, error) {
	res, err := r.runner.Run(ctx, exec.Command{Name: "git", Args: []string{"cat-file", "blob", blob}, Dir: r.dir})
	if err != nil {
		return nil, fmt.Errorf("read anchor %s: %w", blob, err)
	}
	return res.Stdout, nil
}

// parseLineMap reads the changed ranges out of a unified diff.
func parseLineMap(diff string) (LineMap, error) {
	var m LineMap
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "@@") {
			continue
		}
		h, err := parseHunkHeader(line)
		if err != nil {
			return LineMap{}, err
		}
		m.edits = append(m.edits, edit{oldStart: h.OldStart, oldCount: h.OldCount, newCount: h.NewCount})
	}
	sort.SliceStable(m.edits, func(i, j int) bool { return m.edits[i].oldStart < m.edits[j].oldStart })
	return m, nil
}

// KeepAnchors makes the refs holding snapshots alive exactly the given blobs:
// it adds the ones that are missing and deletes the ones nothing names any more.
//
// Called after anything that changes the set of comments, so peel's objects
// cannot outlive the notes they were taken for.
func (r *Repo) KeepAnchors(ctx context.Context, blobs []string) error {
	have, err := r.anchorRefs(ctx)
	if err != nil {
		return err
	}

	want := make(map[string]bool, len(blobs))
	for _, b := range blobs {
		if b != "" {
			want[b] = true
		}
	}

	var cmds []string
	for blob := range want {
		if !have[blob] {
			cmds = append(cmds, "update "+AnchorRefPrefix+blob+" "+blob)
		}
	}
	for blob := range have {
		if !want[blob] {
			cmds = append(cmds, "delete "+AnchorRefPrefix+blob)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	sort.Strings(cmds)

	_, err = r.runner.Run(ctx, exec.Command{
		Name:  "git",
		Args:  []string{"update-ref", "--stdin"},
		Dir:   r.dir,
		Stdin: strings.NewReader(strings.Join(cmds, "\n") + "\n"),
	})
	if err != nil {
		return fmt.Errorf("hold review anchors: %w", err)
	}
	return nil
}

// anchorRefs returns the blobs peel is currently holding.
func (r *Repo) anchorRefs(ctx context.Context) (map[string]bool, error) {
	out, err := r.git(ctx, "for-each-ref", "--format=%(objectname)", AnchorRefPrefix)
	if err != nil {
		return nil, fmt.Errorf("list review anchors: %w", err)
	}
	have := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			have[line] = true
		}
	}
	return have, nil
}
