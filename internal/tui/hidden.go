package tui

import (
	"context"
	"fmt"
	"slices"

	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

type HiddenRef struct {
	RunID  string
	Label  string
	Author store.Author
	File   int
	Hunk   int
	Lines  int
	First  int
	Last   int
	Open   bool
	Row    int
}

func WithHiddenRuns(runs []store.HiddenRun, open map[string]bool) BuildOption {
	return func(c *buildConfig) {
		c.runs = runs
		c.openRuns = open
	}
}

func (d Document) HiddenAt(row int) int {
	if row < 0 || row >= len(d.Rows) || d.Rows[row].Kind != RowHidden {
		return -1
	}
	return d.Rows[row].Run
}

func (d Document) RowOfHidden(run string, hunk, first int) int {
	for _, ref := range d.HiddenRows {
		if ref.RunID == run && ref.Hunk == hunk && ref.First == first {
			return ref.Row
		}
	}
	return -1
}

func (d Document) RowHiding(hunk, line int) int {
	if hunk < 0 || line < 0 {
		return -1
	}
	for _, ref := range d.HiddenRows {
		if ref.Hunk == hunk && !ref.Open && line >= ref.First && line <= ref.Last {
			return ref.Row
		}
	}
	return -1
}

func (d Document) hiddenInFile(file int) int {
	n := 0
	for _, ref := range d.HiddenRows {
		if ref.File == file && !ref.Open {
			n += ref.Lines
		}
	}
	return n
}

func (d Document) runOpen(run int) bool { return d.openRuns[d.runs[run].ID] }

func (d *Document) fileRun(entry git.FileEntry) int {
	if entry.Conflicted {
		return -1
	}
	found := -1
	for i, r := range d.runs {
		if r.File != entry.Path || !r.WholeFile() {
			continue
		}
		if !d.runOpen(i) {
			return i
		}
		if found < 0 {
			found = i
		}
	}
	return found
}

func (d *Document) addHiddenFile(fi int, entry git.FileEntry, run int) {
	lines := 0
	for _, s := range sidesOf(entry) {
		for _, h := range s.diff.Hunks {
			lines += realLines(h.Lines)
		}
	}
	d.addHiddenRow(fi, -1, run, lines, -1, -1)
}

func (d *Document) addHiddenRow(fi, hi, run, lines, first, last int) {
	r := d.runs[run]
	open := d.runOpen(run)
	ri := len(d.HiddenRows)
	d.HiddenRows = append(d.HiddenRows, HiddenRef{
		RunID: r.ID, Label: r.Label, Author: r.Author,
		File: fi, Hunk: hi, Lines: lines, First: first, Last: last, Open: open, Row: len(d.Rows),
	})
	d.add(Row{Kind: RowHidden, File: fi, Hunk: hi, Left: -1, Right: -1, Step: -1, Side: -1, Expand: -1, Run: ri})
	if !open {
		d.HiddenLines += lines
	}
}

func (d *Document) addHiddenSegment(fi, hi int, pairs []linePair, hides []int, from int) {
	run := hides[from]
	lines := d.Hunks[hi].Hunk.Lines
	count, last := 0, lineOf(pairs[from])
	for pi := from; pi < len(pairs) && hides[pi] == run; pi++ {
		count += pairLineCount(lines, pairs[pi])
		last = max(last, pairs[pi].left, pairs[pi].right)
	}
	if !d.runOpen(run) {
		d.Hunks[hi].HiddenLines += count
	}
	d.addHiddenRow(fi, hi, run, count, firstOfPair(pairs[from]), last)
}

func firstOfPair(p linePair) int {
	if p.left >= 0 && p.right >= 0 {
		return min(p.left, p.right)
	}
	return max(p.left, p.right)
}

func (d *Document) hiddenPairs(ref HunkRef, pairs []linePair, idx *commentIndex, wholeFile bool) []int {
	if len(d.runs) == 0 || d.Files[ref.File].Entry.Conflicted {
		return nil
	}
	lines := ref.Hunk.Lines
	at := newSidePositions(lines)
	out := make([]int, len(pairs))
	for pi, pair := range pairs {
		out[pi] = -1
		if idx.touches(ref, pair) || d.draftOnLine(ref, pair) {
			continue
		}
		out[pi] = d.runOver(ref, pair, at, wholeFile)
	}
	return out
}

func (d *Document) runOver(ref HunkRef, pair linePair, at []int, wholeFile bool) int {
	found := -1
	for i, r := range d.runs {
		if r.File != ref.Path || !runHolds(r, ref, pair, at, wholeFile) {
			continue
		}
		if !d.runOpen(i) {
			return i
		}
		if found < 0 {
			found = i
		}
	}
	return found
}

func runHolds(r store.HiddenRun, ref HunkRef, pair linePair, at []int, wholeFile bool) bool {
	if r.WholeFile() {
		return wholeFile
	}
	if !sameOrigin(r.Origin, ref) {
		return false
	}
	for _, i := range []int{pair.left, pair.right} {
		if i < 0 || i >= len(at) {
			continue
		}
		if at[i] < r.Line || at[i] > r.LastLine() {
			return false
		}
	}
	return true
}

func newSidePositions(lines []git.Line) []int {
	at := make([]int, len(lines))
	next := 0
	for i := len(lines) - 1; i >= 0; i-- {
		switch {
		case lines[i].Kind == git.LineNoNewline:
		case lines[i].NewLine > 0:
			next = lines[i].NewLine
			at[i] = next
		default:
			at[i] = next
		}
	}
	for i, l := range lines {
		if l.Kind == git.LineNoNewline && i > 0 {
			at[i] = at[i-1]
		}
	}
	return at
}

func realLines(lines []git.Line) int {
	n := 0
	for _, l := range lines {
		if l.Kind != git.LineNoNewline {
			n++
		}
	}
	return n
}

func pairLineCount(lines []git.Line, pair linePair) int {
	n := 0
	for _, i := range []int{pair.left, pair.right} {
		if i >= 0 && i < len(lines) && lines[i].Kind != git.LineNoNewline {
			n++
		}
	}
	if pair.left == pair.right && n == 2 {
		return 1
	}
	return n
}

func (r *Renderer) hidden(d Document, row Row, st RowState) string {
	ref := d.HiddenRows[row.Run]
	style := r.theme.Note
	if st.Cursor {
		style = r.theme.Cursor
	}
	return r.fit(r.marker(st) + codeIndent(d.Layout) + style.Render(hiddenText(ref)))
}

func hiddenText(ref HiddenRef) string {
	arrow, what := "▸", plural(ref.Lines, "line")+" hidden"
	if ref.Open {
		arrow, what = "▾", plural(ref.Lines, "line")
	}
	if ref.Lines == 0 {
		what = "file hidden"
		if ref.Open {
			what = "file"
		}
	}
	text := arrow + " " + what
	if ref.Author != "" && ref.Author != store.AuthorUnknown {
		text += " by " + string(ref.Author)
	}
	if ref.Label != "" {
		text += " — " + ref.Label
	}
	return text
}

func loadHiddenRuns(ctx context.Context, backend Backend) []store.HiddenRun {
	runs, err := backend.HiddenRuns(ctx)
	if err != nil {
		return nil
	}
	return runs
}

func (m *Model) activeRuns() int {
	n := 0
	for _, r := range m.hiddenRuns {
		if !r.Outdated {
			n++
		}
	}
	return n
}

func (m *Model) runsInForce() []store.HiddenRun {
	if m.othersHidden {
		return nil
	}
	out := make([]store.HiddenRun, 0, len(m.hiddenRuns))
	for _, r := range m.hiddenRuns {
		if !r.Outdated {
			out = append(out, r)
		}
	}
	return out
}

func (m *Model) toggleRun(at int) (what string, back, forth func(*Model)) {
	ref := m.doc.HiddenRows[at]
	view := m.viewport()
	was := m.openRuns[ref.RunID]
	setFlag(m.openRuns, ref.RunID, true, !was)
	m.rebuild()
	if row := m.doc.RowOfHidden(ref.RunID, ref.Hunk, ref.First); row >= 0 {
		m.moveTo(row)
	}
	what = "hiding " + plural(ref.Lines, "line") + " again"
	if !was {
		what = "opening " + plural(ref.Lines, "line")
	}
	m.status = what
	return what, runFold(ref.RunID, was, view), runFold(ref.RunID, !was, m.viewport())
}

func runFold(id string, open bool, v viewport) func(*Model) {
	return func(m *Model) {
		setFlag(m.openRuns, id, true, open)
		m.rebuild()
		m.putViewport(v)
	}
}

func (m *Model) reviewModeStatus(comments int) string {
	if m.activeRuns() == 0 {
		if m.othersHidden {
			return plural(comments, "comment") + " by others hidden"
		}
		return plural(comments, "comment") + " by others shown"
	}
	if m.othersHidden {
		return "full diff — nothing collapsed, comments by others hidden"
	}
	return fmt.Sprintf("review mode — %s by others shown, %s collapsed",
		plural(comments, "comment"), plural(m.doc.HiddenLines, "line"))
}

func (m *Model) othersHiddenLabel() string {
	if m.activeRuns() > 0 {
		return "full diff"
	}
	return "others hidden"
}

func withHiddenLines(status string, lines int) string {
	if lines == 0 {
		return status
	}
	return status + ", including " + plural(lines, "hidden line")
}

func sameRuns(a, b []store.HiddenRun) bool {
	return slices.EqualFunc(a, b, func(x, y store.HiddenRun) bool {
		return x.ID == y.ID && x.File == y.File && x.Line == y.Line && x.EndLine == y.EndLine &&
			x.Origin == y.Origin && x.Label == y.Label && x.Outdated == y.Outdated
	})
}
