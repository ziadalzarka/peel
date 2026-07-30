package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// lineNumWidth is the width of one line-number column in the gutter.
const lineNumWidth = 4

// tabWidth is how far a tab advances the cursor.
const tabWidth = 8

// RowState is how the cursor affects one row.
type RowState struct {
	// Cursor marks the row the cursor rests on.
	Cursor bool
}

// Renderer turns document rows into terminal lines.
type Renderer struct {
	theme  Theme
	syntax *Highlighter
	width  int
}

// NewRenderer returns a renderer. A nil syntax highlighter disables colouring
// by language.
func NewRenderer(theme Theme, syntax *Highlighter) *Renderer {
	return &Renderer{theme: theme, syntax: syntax, width: 80}
}

// SetWidth sets the width rows are fitted to.
func (r *Renderer) SetWidth(w int) {
	r.width = max(w, 20)
}

// Row renders one row of the document to a single line.
func (r *Renderer) Row(d Document, i int, st RowState) string {
	if i < 0 || i >= len(d.Rows) {
		return ""
	}
	row := d.Rows[i]
	switch row.Kind {
	case RowFile:
		return r.file(d, row, st)
	case RowHunk:
		return r.hunk(d, row, st)
	case RowLine:
		return r.line(d, row, st)
	case RowNote:
		return r.note(row, st)
	case RowComment:
		return r.comment(d, row, st)
	case RowStep:
		return r.step(d, row, st)
	case RowStepText:
		return r.stepText(row)
	default:
		// A separator still has to fill its width, or the pane beside it shows
		// through.
		return r.fit("")
	}
}

func (r *Renderer) file(d Document, row Row, st RowState) string {
	f := d.Files[row.File]
	entry := f.Entry
	added, removed := entry.Stats()

	arrow := "▾"
	if f.Collapsed {
		arrow = "▸"
	}
	name := entry.Path
	if st.Cursor {
		name = r.theme.Cursor.Render(name)
	} else {
		name = r.theme.FileHead.Render(name)
	}

	summary := fmt.Sprintf("%s +%d -%d", fileLabel(entry), added, removed)
	return r.fit(strings.Join([]string{
		" ",
		r.stateSymbol(entry.State()),
		r.theme.Dim.Render(arrow),
		name,
		r.theme.Dim.Render(summary),
	}, " "))
}

func (r *Renderer) hunk(d Document, row Row, st RowState) string {
	ref := d.Hunks[row.Hunk]
	style := r.theme.HunkHead
	if st.Cursor {
		style = r.theme.Cursor
	}
	origin := r.theme.Dim.Render("worktree")
	if ref.Staged {
		origin = r.theme.Staged.Render("index")
	}
	title := ref.Hunk.Section
	if title == "" {
		title = "⋯"
	}
	return r.fit(r.marker(st) + codeIndent(d.Layout) + style.Render(title) + "  " + origin)
}

func codeIndent(l Layout) string {
	if l == LayoutSplit {
		return strings.Repeat(" ", lineNumWidth+2)
	}
	return strings.Repeat(" ", 2*lineNumWidth+2)
}

func (r *Renderer) line(d Document, row Row, st RowState) string {
	ref := d.Hunks[row.Hunk]
	prefix := r.marker(st)
	if d.Layout == LayoutSplit {
		return r.fit(prefix + r.splitBody(ref, row))
	}
	return r.fit(prefix + r.unifiedBody(ref, row))
}

func (r *Renderer) unifiedBody(ref HunkRef, row Row) string {
	l := ref.Hunk.Lines[row.Left]
	return r.gutter(l.OldLine) + r.gutter(l.NewLine) + " " + r.content(ref.Path, l)
}

// splitBody puts the old side left of the new side. Either index may be -1,
// where the change has no counterpart on that side.
func (r *Renderer) splitBody(ref HunkRef, row Row) string {
	half := (r.width - 5) / 2
	left := r.halfLine(ref, row.Left, true, half)
	right := r.halfLine(ref, row.Right, false, half)
	return left + r.theme.Dim.Render(" │ ") + right
}

func (r *Renderer) halfLine(ref HunkRef, index int, old bool, width int) string {
	if index < 0 || index >= len(ref.Hunk.Lines) {
		return pad("", width)
	}
	l := ref.Hunk.Lines[index]
	num := l.NewLine
	if old {
		num = l.OldLine
	}
	return fit(r.gutter(num)+" "+r.content(ref.Path, l), width)
}

// content renders a line's origin character and text. The origin keeps the diff
// colour while the text keeps the language's, so syntax highlighting and the
// add/remove signal do not fight over the same characters.
func (r *Renderer) content(path string, l git.Line) string {
	origin := string(l.Kind.Origin())
	text := expandTabs(l.Text)
	if l.Kind == git.LineNoNewline {
		return r.theme.Dim.Render(origin + text)
	}
	if r.syntax.Active() {
		return r.styleFor(l).Render(origin) + r.syntax.Line(path, text)
	}
	return r.styleFor(l).Render(origin + text)
}

// expandTabs replaces tabs with spaces up to the next tab stop.
//
// It has to happen before anything measures the line: a tab is zero columns to
// ansi.StringWidth but eight on screen, so a tab-indented line left as it is
// gets padded to the pane width, overflows once the terminal expands it, and
// wraps — which makes the frame a line taller than the terminal and scrolls the
// whole display on every repaint.
//
// Stops are counted from the start of the text rather than the start of the
// row, so the code on a `+` line and the code on a `-` line indent alike.
func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for _, c := range s {
		switch c {
		case '\t':
			n := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case '\n':
			b.WriteRune(c)
			col = 0
		default:
			b.WriteRune(c)
			col += ansi.StringWidth(string(c))
		}
	}
	return b.String()
}

func (r *Renderer) note(row Row, st RowState) string {
	return r.fit(r.marker(st) + "  " + r.theme.Note.Render(expandTabs(row.Text)))
}

func (r *Renderer) comment(d Document, row Row, st RowState) string {
	c := d.Comments[row.Comment]
	text := expandTabs(row.Text)
	body := r.theme.Comment.Render(text)
	if c.Resolved {
		body = r.theme.Resolved.Render(text)
	}

	bar := r.theme.Comment.Render("┃")
	if !row.Head {
		indent := strings.Repeat(" ", ansi.StringWidth(commentTag(c)))
		return r.fit(r.marker(st) + "   " + bar + " " + indent + body)
	}
	tag := r.theme.Author.Render(commentTag(c))
	if st.Cursor {
		tag = r.theme.Cursor.Render(commentTag(c))
	}
	return r.fit(r.marker(st) + "   " + bar + " " + tag + body)
}

// commentTag prefixes a comment with who wrote it, and marks it resolved.
func commentTag(c store.Comment) string {
	tag := string(c.Author)
	if tag == "" {
		tag = "user"
	}
	if c.Resolved {
		tag = "✓ " + tag
	}
	return tag + ": "
}

func (r *Renderer) marker(st RowState) string {
	if st.Cursor {
		return r.theme.Cursor.Render("▌")
	}
	return " "
}

func (r *Renderer) stateSymbol(s git.StageState) string {
	switch s {
	case git.StateStaged:
		return r.theme.Staged.Render(s.Symbol())
	case git.StatePartial:
		return r.theme.Partial.Render(s.Symbol())
	default:
		return s.Symbol()
	}
}

func (r *Renderer) gutter(n int) string {
	if n <= 0 {
		return r.theme.Gutter.Render(strings.Repeat(" ", lineNumWidth))
	}
	return r.theme.Gutter.Render(pad(strconv.Itoa(n), lineNumWidth))
}

func (r *Renderer) styleFor(l git.Line) lipgloss.Style {
	switch l.Kind {
	case git.LineAdded:
		return r.theme.Added
	case git.LineRemoved:
		return r.theme.Removed
	case git.LineNoNewline:
		return r.theme.Dim
	default:
		return r.theme.Context
	}
}

func (r *Renderer) fit(s string) string { return fit(s, r.width) }

// fileLabel describes how a file changed, for the header summary.
func fileLabel(e git.FileEntry) string {
	if e.Untracked {
		return "untracked"
	}
	diff := e.Primary()
	if diff == nil {
		return ""
	}
	if diff.IsBinary {
		return "binary"
	}
	if diff.Status == git.StatusRenamed {
		return "renamed"
	}
	return string(diff.Status)
}

// fit truncates to width and pads short lines, so callers can compose columns
// without counting ANSI escapes themselves.
func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > width {
		return ansi.Truncate(s, width, "…")
	}
	return s + strings.Repeat(" ", width-ansi.StringWidth(s))
}

// pad right-aligns s, for line numbers.
func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

// padRight left-aligns s, for label columns.
func padRight(s string, width int) string {
	if ansi.StringWidth(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-ansi.StringWidth(s))
}
