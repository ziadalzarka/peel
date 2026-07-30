// Package git parses unified diffs and stages them into the git index.
//
// It is deliberately UI-agnostic: nothing here imports a rendering package, so
// the same model backs the TUI today and any other frontend later.
package git

import (
	"fmt"
	"strconv"
	"strings"
)

// FileStatus classifies how a file changed between two trees.
type FileStatus string

const (
	StatusAdded    FileStatus = "added"
	StatusModified FileStatus = "modified"
	StatusDeleted  FileStatus = "deleted"
	StatusRenamed  FileStatus = "renamed"
	StatusCopied   FileStatus = "copied"
)

// LineKind classifies a single line inside a hunk.
type LineKind uint8

const (
	// LineContext is unchanged and present on both sides.
	LineContext LineKind = iota
	// LineAdded exists only in the new file.
	LineAdded
	// LineRemoved exists only in the old file.
	LineRemoved
	// LineNoNewline is the "\ No newline at end of file" marker. It carries no
	// content of its own but must be reproduced verbatim in generated patches.
	LineNoNewline
)

// Origin returns the leading character this kind occupies in a unified diff.
func (k LineKind) Origin() byte {
	switch k {
	case LineAdded:
		return '+'
	case LineRemoved:
		return '-'
	case LineNoNewline:
		return '\\'
	default:
		return ' '
	}
}

// Line is one line of a hunk body.
type Line struct {
	Kind LineKind
	// Text excludes the leading origin character and the trailing newline.
	Text string
	// OldLine is the 1-based line number in the old file, or 0 if absent there.
	OldLine int
	// NewLine is the 1-based line number in the new file, or 0 if absent there.
	NewLine int
}

// IsChange reports whether the line is an addition or a removal.
func (l Line) IsChange() bool { return l.Kind == LineAdded || l.Kind == LineRemoved }

// Render reproduces the line exactly as it appeared in the diff, without a
// trailing newline.
func (l Line) Render() string {
	return string(l.Kind.Origin()) + l.Text
}

// Hunk is a contiguous run of changes within one file.
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	// Section is the trailing context git prints after the second @@, such as
	// the enclosing function signature. Purely informational.
	Section string
	Lines   []Line
}

// Header renders the hunk's @@ line, including the section suffix.
func (h Hunk) Header() string {
	head := "@@ -" + formatRange(h.OldStart, h.OldCount) + " +" + formatRange(h.NewStart, h.NewCount) + " @@"
	if h.Section != "" {
		head += " " + h.Section
	}
	return head
}

// Stats counts added and removed lines in the hunk.
func (h Hunk) Stats() (added, removed int) {
	for _, l := range h.Lines {
		switch l.Kind {
		case LineAdded:
			added++
		case LineRemoved:
			removed++
		}
	}
	return added, removed
}

// FileDiff is the complete set of changes to a single file.
type FileDiff struct {
	// OldPath is the path before the change; equal to NewPath unless renamed.
	OldPath string
	// NewPath is the path after the change. For deletions this is the old path.
	NewPath string
	Status  FileStatus
	// IsBinary marks files git could not diff textually. They have no hunks and
	// can only be staged whole.
	IsBinary bool
	// OldMode and NewMode are the file modes when git reported a mode change.
	OldMode string
	NewMode string
	Hunks   []Hunk
}

// Path returns the path to display and to address the file by.
func (f FileDiff) Path() string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// Stats sums added and removed lines across all hunks.
func (f FileDiff) Stats() (added, removed int) {
	for _, h := range f.Hunks {
		a, r := h.Stats()
		added += a
		removed += r
	}
	return added, removed
}

// Diff is a parsed set of file changes — the output of one `git diff`.
type Diff struct {
	Files []FileDiff
}

// File returns the FileDiff for path, matching on either side of a rename.
func (d Diff) File(path string) (FileDiff, bool) {
	for _, f := range d.Files {
		if f.Path() == path || f.OldPath == path {
			return f, true
		}
	}
	return FileDiff{}, false
}

// HunkID addresses one hunk stably enough to pass across a CLI boundary.
//
// The wire format matches critique's, so the IDs are scriptable:
//
//	src/main.go:@-10,6+10,7
//
// IDs are derived from line offsets, so they shift whenever the tree changes.
// Callers must recompute them after every apply and never cache them across
// operations.
type HunkID struct {
	Path     string
	OldStart int
	OldCount int
	NewStart int
	NewCount int
}

// String renders the ID in wire format.
func (id HunkID) String() string {
	return fmt.Sprintf("%s:@-%d,%d+%d,%d", id.Path, id.OldStart, id.OldCount, id.NewStart, id.NewCount)
}

// ID returns the addressable identifier for a hunk within the given file.
func (f FileDiff) ID(h Hunk) HunkID {
	return HunkID{
		Path:     f.Path(),
		OldStart: h.OldStart,
		OldCount: h.OldCount,
		NewStart: h.NewStart,
		NewCount: h.NewCount,
	}
}

// ParseHunkID parses the wire format produced by HunkID.String.
func ParseHunkID(s string) (HunkID, error) {
	at := strings.LastIndex(s, ":@")
	if at < 0 {
		return HunkID{}, fmt.Errorf("hunk id %q: missing \":@\" separator", s)
	}
	path, spec := s[:at], s[at+2:]
	if path == "" {
		return HunkID{}, fmt.Errorf("hunk id %q: empty path", s)
	}

	plus := strings.Index(spec, "+")
	if plus < 0 || !strings.HasPrefix(spec, "-") {
		return HunkID{}, fmt.Errorf("hunk id %q: want form path:@-old,count+new,count", s)
	}

	oldStart, oldCount, err := parseRange(spec[1:plus])
	if err != nil {
		return HunkID{}, fmt.Errorf("hunk id %q: old range: %w", s, err)
	}
	newStart, newCount, err := parseRange(spec[plus+1:])
	if err != nil {
		return HunkID{}, fmt.Errorf("hunk id %q: new range: %w", s, err)
	}

	return HunkID{
		Path:     path,
		OldStart: oldStart,
		OldCount: oldCount,
		NewStart: newStart,
		NewCount: newCount,
	}, nil
}

// Matches reports whether the hunk within file is the one this ID addresses.
func (id HunkID) Matches(f FileDiff, h Hunk) bool {
	return f.ID(h) == id
}

// formatRange renders a unified-diff range, omitting the count when it is 1
// exactly as git does.
func formatRange(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// parseRange parses "10,6" or the abbreviated "10" (count 1).
func parseRange(s string) (start, count int, err error) {
	if s == "" {
		return 0, 0, fmt.Errorf("empty range")
	}
	if comma := strings.Index(s, ","); comma >= 0 {
		start, err = strconv.Atoi(s[:comma])
		if err != nil {
			return 0, 0, fmt.Errorf("start: %w", err)
		}
		count, err = strconv.Atoi(s[comma+1:])
		if err != nil {
			return 0, 0, fmt.Errorf("count: %w", err)
		}
		return start, count, nil
	}
	start, err = strconv.Atoi(s)
	if err != nil {
		return 0, 0, fmt.Errorf("start: %w", err)
	}
	return start, 1, nil
}
