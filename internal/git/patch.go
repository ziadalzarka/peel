package git

import (
	"fmt"
	"strings"
)

// Patch renders one hunk as a patch of its own — the input to `git apply
// --cached`, and the whole of the patch generation peel does.
//
// The new side's start is recomputed rather than carried over. A hunk's header
// numbers its new side as though every hunk above it in the file had already
// been applied, and staging one hunk applies none of them, so the number the
// diff arrives with is wrong by however much the hunks above it grew the file.
// The declared counts are checked against the body for the same reason: a patch
// that disagrees with itself is how staging part of a file writes the wrong
// lines into the index.
func Patch(f FileDiff, h Hunk) (string, error) {
	if f.IsBinary {
		return "", fmt.Errorf("%s: a binary file has no hunks to stage", f.Path())
	}

	oldCount, newCount := countSides(h.Lines)
	if oldCount != h.OldCount || newCount != h.NewCount {
		return "", fmt.Errorf("%s: hunk %s declares (-%d +%d) and holds (-%d +%d)",
			f.Path(), h.Header(), h.OldCount, h.NewCount, oldCount, newCount)
	}

	out := h
	out.NewStart = rebase(h.OldStart, oldCount, newCount)
	// The section is git naming the declaration the hunk sits inside, for
	// somebody reading the diff. A patch is read by git apply, which has the
	// file itself.
	out.Section = ""

	var b strings.Builder
	b.WriteString(fileHeader(f))
	b.WriteString(out.Header())
	b.WriteByte('\n')
	for _, l := range out.Lines {
		b.WriteString(l.Render())
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// rebase translates the old side's start line into the new side's, for a patch
// that applies this hunk and nothing above it: with nothing before it moved, the
// two sides start on the same line. A side with no lines at all is written as the
// line it comes after, so both ends go through the line they start at.
func rebase(oldStart, oldCount, newCount int) int {
	return lineStart(firstLine(oldStart, oldCount), newCount)
}

// countSides counts how many of a body's lines belong to each side.
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

// fileHeader renders the `diff --git` and ---/+++ lines a patch opens with.
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
		fmt.Fprintf(&b, "new file mode %s\n", mode(f.NewMode))
		b.WriteString("--- " + devNull + "\n")
		fmt.Fprintf(&b, "+++ %s\n", quotePath("b/"+newPath))
	case StatusDeleted:
		fmt.Fprintf(&b, "deleted file mode %s\n", mode(f.OldMode))
		fmt.Fprintf(&b, "--- %s\n", quotePath("a/"+oldPath))
		b.WriteString("+++ " + devNull + "\n")
	default:
		fmt.Fprintf(&b, "--- %s\n", quotePath("a/"+oldPath))
		fmt.Fprintf(&b, "+++ %s\n", quotePath("b/"+newPath))
	}
	return b.String()
}

// mode is the file mode a header states, falling back to a plain file for the
// diffs that carry no mode line.
func mode(m string) string {
	if m == "" {
		return "100644"
	}
	return m
}

// quotePath renders a path for a diff header, in git's own C-style quoting and
// only when the path holds something that would otherwise be read as the end of
// it.
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
