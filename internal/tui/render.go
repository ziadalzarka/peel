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

// RowState is how the cursor and any line selection affect one row.
type RowState struct {
	// Cursor marks the row the browse cursor rests on.
	Cursor bool
	// Focus marks the line the line-selection cursor rests on.
	Focus bool
	// Selecting reports that line selection is under way. Every line row then
	// reserves the selection column, whether or not it can be selected, so the
	// diff does not shift under the reviewer.
	Selecting bool
	// Selectable marks a line eligible for line-level staging.
	Selectable bool
	// Selected marks a line chosen for line-level staging.
	Selected bool
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
		r.marker(st),
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
	return r.fit(r.marker(st) + "  " + style.Render(ref.Hunk.Header()) + "  " + origin)
}

func (r *Renderer) line(d Document, row Row, st RowState) string {
	ref := d.Hunks[row.Hunk]
	prefix := r.marker(st) + r.selectSymbol(st)
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
	if l.Kind == git.LineNoNewline {
		return r.theme.Dim.Render(origin + l.Text)
	}
	if r.syntax.Active() {
		return r.styleFor(l).Render(origin) + r.syntax.Line(path, l.Text)
	}
	return r.styleFor(l).Render(origin + l.Text)
}

func (r *Renderer) note(row Row, st RowState) string {
	return r.fit(r.marker(st) + "  " + r.theme.Note.Render(row.Text))
}

func (r *Renderer) comment(d Document, row Row, st RowState) string {
	c := d.Comments[row.Comment]
	body := r.theme.Comment.Render(row.Text)
	if c.Resolved {
		body = r.theme.Resolved.Render(row.Text)
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
	if st.Cursor || st.Focus {
		return r.theme.Cursor.Render("▌")
	}
	return " "
}

// selectSymbol shows a line's selection state. The column exists only while
// selecting, so the diff stays uncluttered the rest of the time.
func (r *Renderer) selectSymbol(st RowState) string {
	switch {
	case !st.Selecting:
		return ""
	case !st.Selectable:
		return " "
	case st.Selected:
		return r.theme.Selected.Render("◉")
	default:
		return r.theme.Dim.Render("○")
	}
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
