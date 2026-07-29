package git

import (
	"fmt"
	"strings"
)

// Direction states which side of a patch describes the tree it will be applied
// to. It determines both how line-level selections are transformed and which
// side's line numbers are authoritative.
type Direction int

const (
	// Forward applies a patch as written: its old side describes the current
	// tree. Staging uses this, because `git diff`'s old side is the index.
	Forward Direction = iota
	// Reverse applies a patch with `git apply --reverse`: its new side
	// describes the current tree. Unstaging uses this, because
	// `git diff --cached`'s new side is the index.
	Reverse
)

func (d Direction) String() string {
	if d == Reverse {
		return "reverse"
	}
	return "forward"
}

// BuildPatch renders a minimal unified diff containing only the given hunks of
// one file — the input to `git apply --cached`.
//
// Hunks must belong to f and be in file order. Line numbers on the result side
// are recomputed from a running delta rather than copied, because a patch that
// omits earlier hunks shifts everything that follows. Getting this wrong is how
// a partial stage silently writes the wrong lines to the index.
func BuildPatch(f FileDiff, hunks []Hunk, dir Direction) (string, error) {
	if f.IsBinary {
		return "", fmt.Errorf("%s: binary files cannot be hunk-staged", f.Path())
	}
	if len(hunks) == 0 {
		return "", fmt.Errorf("%s: no hunks selected", f.Path())
	}

	var b strings.Builder
	b.WriteString(fileHeader(f))

	delta := 0
	for _, h := range hunks {
		adjusted, err := anchorHunk(h, dir, &delta)
		if err != nil {
			return "", fmt.Errorf("%s: %w", f.Path(), err)
		}
		b.WriteString(adjusted.Header())
		b.WriteByte('\n')
		for _, l := range adjusted.Lines {
			b.WriteString(l.Render())
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

// anchorHunk rewrites the non-authoritative side's start line using the running
// delta, and verifies the declared counts match the body.
func anchorHunk(h Hunk, dir Direction, delta *int) (Hunk, error) {
	oldCount, newCount := countSides(h.Lines)
	if oldCount != h.OldCount || newCount != h.NewCount {
		return Hunk{}, fmt.Errorf(
			"hunk %s: declared counts (-%d +%d) disagree with body (-%d +%d)",
			h.Header(), h.OldCount, h.NewCount, oldCount, newCount)
	}

	out := h
	switch dir {
	case Forward:
		out.NewStart = rebase(h.OldStart, oldCount, newCount, *delta)
		*delta += newCount - oldCount
	case Reverse:
		out.OldStart = rebase(h.NewStart, newCount, oldCount, *delta)
		*delta += oldCount - newCount
	}

	// The section suffix is cosmetic and can contain text that confuses
	// `git apply` when a path is unusual; drop it from generated patches.
	out.Section = ""
	return out, nil
}

// rebase translates the authoritative side's start line into the other side's.
//
// A side with no lines is addressed by the line it comes *after* rather than
// the line it starts at — that is why a file creation reads `@@ -0,0 +1,2 @@`
// and a file deletion reads `@@ -1,2 +0,0 @@`. Both ends need converting, so
// this shifts into "first line" space and back out again.
func rebase(anchorStart, anchorCount, otherCount, delta int) int {
	pos := anchorStart
	if anchorCount == 0 {
		pos++
	}
	pos += delta
	if otherCount == 0 {
		pos--
	}
	return pos
}

// countSides counts how many lines belong to the old and new sides.
func countSides(lines []Line) (old, new int) {
	for _, l := range lines {
		switch l.Kind {
		case LineContext:
			old++
			new++
		case LineRemoved:
			old++
		case LineAdded:
			new++
		}
	}
	return old, new
}

// fileHeader renders the `diff --git` and ---/+++ lines for a minimal patch.
func fileHeader(f FileDiff) string {
	oldPath, newPath := f.OldPath, f.NewPath
	if oldPath == "" {
		oldPath = newPath
	}
	if newPath == "" {
		newPath = oldPath
	}

	var b strings.Builder
	fmt.Fprintf(&b, "diff --git %s %s\n", quotePath("a/"+oldPath), quotePath("b/"+newPath))

	switch f.Status {
	case StatusAdded:
		mode := f.NewMode
		if mode == "" {
			mode = "100644"
		}
		fmt.Fprintf(&b, "new file mode %s\n", mode)
		b.WriteString("--- " + devNull + "\n")
		fmt.Fprintf(&b, "+++ %s\n", quotePath("b/"+newPath))
	case StatusDeleted:
		mode := f.OldMode
		if mode == "" {
			mode = "100644"
		}
		fmt.Fprintf(&b, "deleted file mode %s\n", mode)
		fmt.Fprintf(&b, "--- %s\n", quotePath("a/"+oldPath))
		b.WriteString("+++ " + devNull + "\n")
	default:
		fmt.Fprintf(&b, "--- %s\n", quotePath("a/"+oldPath))
		fmt.Fprintf(&b, "+++ %s\n", quotePath("b/"+newPath))
	}
	return b.String()
}

// SelectLines rewrites a hunk to contain only the changes at the given indexes
// into h.Lines, so a user can stage part of a hunk.
//
// The transformation depends on which side describes the current tree:
//
//	Forward (staging, current tree is the old side)
//	  unselected +  → dropped   (not in the index yet, so it must not appear)
//	  unselected -  → context   (still in the index, so it must appear on both)
//
//	Reverse (unstaging, current tree is the new side)
//	  unselected -  → dropped   (already absent from the index)
//	  unselected +  → context   (still in the index)
//
// The rule underneath both: a line absent from the current tree is dropped when
// unselected; a line present in it becomes context.
//
// ok is false when nothing selected survives, meaning the hunk would be a no-op
// and must be left out of the patch entirely.
func SelectLines(h Hunk, selected map[int]bool, dir Direction) (out Hunk, ok bool) {
	dropKind, keepKind := LineAdded, LineRemoved
	if dir == Reverse {
		dropKind, keepKind = LineRemoved, LineAdded
	}

	out = Hunk{OldStart: h.OldStart, NewStart: h.NewStart, Section: h.Section}
	changed := false
	lastKept := -1

	for i, l := range h.Lines {
		switch l.Kind {
		case LineContext:
			out.Lines = append(out.Lines, l)
			lastKept = i

		case LineNoNewline:
			// The marker qualifies the line before it; it is meaningless if
			// that line was dropped.
			if lastKept == i-1 {
				out.Lines = append(out.Lines, l)
				lastKept = i
			}

		case dropKind:
			if selected[i] {
				out.Lines = append(out.Lines, l)
				changed = true
				lastKept = i
			}

		case keepKind:
			if selected[i] {
				out.Lines = append(out.Lines, l)
				changed = true
			} else {
				out.Lines = append(out.Lines, Line{Kind: LineContext, Text: l.Text, OldLine: l.OldLine, NewLine: l.NewLine})
			}
			lastKept = i
		}
	}

	if !changed {
		return Hunk{}, false
	}
	out.OldCount, out.NewCount = countSides(out.Lines)
	return out, true
}

// quotePath renders a path for a diff header, applying git's C-style quoting
// only when the path contains characters that would otherwise be ambiguous.
func quotePath(path string) string {
	if !needsQuoting(path) {
		return path
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(path); i++ {
		switch c := path[i]; c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\%03o`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func needsQuoting(path string) bool {
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c == '"' || c == '\\' || c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}
