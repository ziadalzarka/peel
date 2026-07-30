// Package tui is peel's interactive review UI.
//
// Navigation and rendering are kept separate from bubbletea so they can be
// tested without a terminal: Document flattens a session into a list of rows,
// Renderer turns one row into a string, and Model only maps key presses onto
// those two.
package tui

import (
	"strings"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// Layout selects how a hunk body is laid out.
type Layout int

const (
	// LayoutUnified shows one column of +/- lines.
	LayoutUnified Layout = iota
	// LayoutSplit shows old and new side by side.
	LayoutSplit
)

// Toggle returns the other layout.
func (l Layout) Toggle() Layout {
	if l == LayoutUnified {
		return LayoutSplit
	}
	return LayoutUnified
}

func (l Layout) String() string {
	if l == LayoutSplit {
		return "split"
	}
	return "unified"
}

// RowKind classifies one line of the document.
type RowKind int

const (
	// RowFile is a file header.
	RowFile RowKind = iota
	// RowHunk is a hunk header.
	RowHunk
	// RowLine is one line of a hunk body.
	RowLine
	// RowNote is an informational line, such as "binary file".
	RowNote
	// RowComment is one review comment shown at its anchor.
	RowComment
	// RowStep is a walkthrough step's heading, shown above the files it covers.
	RowStep
	// RowStepText is one wrapped line of a step's explanation.
	RowStepText
	// RowBlank separates files.
	RowBlank
)

// Row is one rendered line of the document.
//
// Left and Right index into the enclosing hunk's Lines. In unified layout only
// Left is set; in split layout either side may be -1 where the other side has
// no counterpart.
//
// Every row renders to exactly one terminal line, which is what lets the
// viewport treat the document as a flat window. A comment body spanning several
// lines therefore becomes several rows, and only the first carries Head.
type Row struct {
	Kind    RowKind
	File    int
	Hunk    int
	Left    int
	Right   int
	Comment int
	// Step indexes into Document.Steps on a walkthrough row, and is -1 on every
	// other row.
	Step int
	Text string
	Head bool
}

// HunkRef is one addressable hunk within the document.
type HunkRef struct {
	File int
	Path string
	// Staged reports that this hunk is in the index rather than the working
	// tree, which is what labels it on screen and what makes a reload put the
	// cursor back on whatever is still unstaged.
	Staged bool
	ID     git.HunkID
	Hunk   git.Hunk
}

// FileRef is one file within the document.
type FileRef struct {
	Entry git.FileEntry
	// Hunks indexes into Document.Hunks.
	Hunks []int
	// Row is the position of the file's header.
	Row int
	// Collapsed reports that the file's body is hidden.
	Collapsed bool
}

// StepRef is one walkthrough group as the document lays it out.
type StepRef struct {
	store.Step
	// Files indexes into Document.Files, in the order the step names them.
	Files []int
	// Row is the position of the step's heading.
	Row int
	// Folded hides the explanation, leaving the heading, so a walkthrough that
	// has been read can be got out of the way of the diff it describes.
	Folded bool
}

// Document is a session flattened into navigable rows.
type Document struct {
	Files []FileRef
	Hunks []HunkRef
	Rows  []Row
	// Steps are the walkthrough groups the files are laid out under, in reading
	// order. It is empty when there is no walkthrough on screen.
	Steps    []StepRef
	Comments []store.Comment
	Layout   Layout
}

// Build flattens a session into rows. collapsed hides a file's body by path.
func Build(s *app.Session, comments []store.Comment, collapsed map[string]bool, layout Layout, opts ...BuildOption) Document {
	doc := Document{Comments: comments, Layout: layout}
	if s == nil {
		return doc
	}
	var groups Groups
	for _, opt := range opts {
		opt(&groups)
	}
	idx := indexComments(comments)

	for _, group := range groupFiles(s.Files, groups.Steps) {
		doc.addStep(group, groups)
		for _, si := range group.files {
			entry := s.Files[si]
			fi := len(doc.Files)
			hidden := collapsed[entry.Path]
			doc.Files = append(doc.Files, FileRef{Entry: entry, Row: len(doc.Rows), Collapsed: hidden})
			doc.add(Row{Kind: RowFile, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1})
			doc.addComments(fi, -1, idx.takeFile(entry.Path))

			if !hidden {
				doc.addBody(fi, entry, idx)
			}
			doc.addComments(fi, -1, idx.rest(entry.Path))
			doc.add(Row{Kind: RowBlank, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1})
		}
	}
	return doc
}

// addStep puts a walkthrough group's heading and explanation in front of the
// files it covers, which is all the walkthrough is: the same diff, read in the
// order the narrative gives it, with the narrative in place.
func (d *Document) addStep(group fileGroup, groups Groups) {
	if group.step == nil {
		return
	}
	index := len(d.Steps)
	ref := StepRef{Step: *group.step, Row: len(d.Rows), Folded: groups.Folded[index]}
	// The group's files are appended by the caller, directly after this heading.
	for i := range group.files {
		ref.Files = append(ref.Files, len(d.Files)+i)
	}
	d.Steps = append(d.Steps, ref)

	// The heading carries the first file it introduces, so the file list beside
	// the diff marks the file the window is opening on rather than nothing.
	file := -1
	if len(ref.Files) > 0 {
		file = ref.Files[0]
	}
	row := Row{Kind: RowStep, File: file, Hunk: -1, Left: -1, Right: -1, Step: index}
	d.add(row)

	row.Kind = RowStepText
	if !ref.Folded {
		for _, line := range wrapBody(group.step.Body, groups.Width) {
			row.Text = line
			d.add(row)
		}
	}
	// A blank prose line closes the group, so the file header below it is not
	// pressed against the explanation.
	row.Text = ""
	d.add(row)
}

func (d *Document) add(r Row) { d.Rows = append(d.Rows, r) }

func (d *Document) addComments(file, hunk int, ids []int) {
	for _, ci := range ids {
		for n, text := range strings.Split(d.Comments[ci].Body, "\n") {
			d.add(Row{
				Kind:    RowComment,
				File:    file,
				Hunk:    hunk,
				Left:    -1,
				Right:   -1,
				Comment: ci,
				Step:    -1,
				Text:    text,
				Head:    n == 0,
			})
		}
	}
}

func (d *Document) addBody(fi int, entry git.FileEntry, idx *commentIndex) {
	if entry.IsBinary() {
		d.add(Row{Kind: RowNote, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1,
			Text: "binary file — no diff to show"})
		return
	}

	for _, side := range sidesOf(entry) {
		for _, h := range side.diff.Hunks {
			hi := len(d.Hunks)
			d.Hunks = append(d.Hunks, HunkRef{
				File:   fi,
				Path:   entry.Path,
				Staged: side.staged,
				ID:     side.diff.ID(h),
				Hunk:   h,
			})
			d.Files[fi].Hunks = append(d.Files[fi].Hunks, hi)
			d.add(Row{Kind: RowHunk, File: fi, Hunk: hi, Left: -1, Right: -1, Step: -1})

			for _, pair := range pairLines(h.Lines, d.Layout) {
				d.add(Row{Kind: RowLine, File: fi, Hunk: hi, Left: pair.left, Right: pair.right, Step: -1})
				d.addComments(fi, hi, idx.takeLine(entry.Path, h.Lines, pair))
			}
		}
	}

	if len(d.Files[fi].Hunks) == 0 {
		d.add(Row{Kind: RowNote, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Text: emptyNote(entry)})
	}
}

// emptyNote explains a file that changed without producing hunks.
func emptyNote(e git.FileEntry) string {
	diff := e.Primary()
	if diff == nil {
		return "no changes"
	}
	if diff.OldMode != "" && diff.NewMode != "" && diff.OldMode != diff.NewMode {
		return "mode " + diff.OldMode + " → " + diff.NewMode
	}
	switch diff.Status {
	case git.StatusRenamed:
		return "renamed from " + diff.OldPath
	case git.StatusCopied:
		return "copied from " + diff.OldPath
	}
	return "no textual changes"
}

type side struct {
	diff   *git.FileDiff
	staged bool
}

// sidesOf lists a file's change sides, index first so each file reads
// index-then-worktree.
func sidesOf(e git.FileEntry) []side {
	var out []side
	if e.Staged != nil {
		out = append(out, side{diff: e.Staged, staged: true})
	}
	if e.Unstaged != nil {
		out = append(out, side{diff: e.Unstaged, staged: false})
	}
	return out
}

// linePair is one displayed row of a hunk body: an old-side index, a new-side
// index, or both.
type linePair struct{ left, right int }

// pairLines lays a hunk body out for the given layout.
//
// Unified emits every line once. Split walks each run of removals and the
// additions that follow it together, so a replaced line shows its old and new
// text on the same row.
func pairLines(lines []git.Line, layout Layout) []linePair {
	if layout == LayoutUnified {
		out := make([]linePair, 0, len(lines))
		for i := range lines {
			out = append(out, linePair{left: i, right: -1})
		}
		return out
	}

	var out []linePair
	for i := 0; i < len(lines); {
		switch lines[i].Kind {
		case git.LineContext:
			out = append(out, linePair{left: i, right: i})
			i++
		case git.LineNoNewline:
			out = append(out, noNewlinePair(lines, i))
			i++
		default:
			var removed, added []int
			for ; i < len(lines) && lines[i].Kind == git.LineRemoved; i++ {
				removed = append(removed, i)
			}
			for ; i < len(lines) && lines[i].Kind == git.LineAdded; i++ {
				added = append(added, i)
			}
			for k := 0; k < max(len(removed), len(added)); k++ {
				out = append(out, linePair{left: at(removed, k), right: at(added, k)})
			}
		}
	}
	return out
}

// noNewlinePair puts the marker on whichever side the line before it belongs
// to, since that is the side missing the newline.
func noNewlinePair(lines []git.Line, i int) linePair {
	if i == 0 {
		return linePair{left: i, right: i}
	}
	switch lines[i-1].Kind {
	case git.LineAdded:
		return linePair{left: -1, right: i}
	case git.LineRemoved:
		return linePair{left: i, right: -1}
	default:
		return linePair{left: i, right: i}
	}
}

func at(s []int, i int) int {
	if i < len(s) {
		return s[i]
	}
	return -1
}

// Len is the number of rows.
func (d Document) Len() int { return len(d.Rows) }

// IsStop reports whether the cursor may rest on row i.
//
// Every row can hold the cursor except the blank between files, the
// continuation lines of a multi-line comment — a comment is addressed from its
// first line — and a walkthrough explanation, which is addressed from its
// heading. Diff lines are stops, so a note can be left on unchanged code without
// a mode to enter first.
func (d Document) IsStop(i int) bool {
	if i < 0 || i >= len(d.Rows) {
		return false
	}
	switch d.Rows[i].Kind {
	case RowComment:
		return d.Rows[i].Head
	case RowBlank, RowStepText:
		return false
	default:
		return true
	}
}

// IsMark reports whether row i heads something whole: a file, a hunk, a comment,
// or a walkthrough group. These are what j and k jump between, while the arrows
// step row by row.
func (d Document) IsMark(i int) bool {
	if i < 0 || i >= len(d.Rows) {
		return false
	}
	switch d.Rows[i].Kind {
	case RowFile, RowHunk, RowStep:
		return true
	case RowComment:
		return d.Rows[i].Head
	default:
		return false
	}
}

// NextMark returns the next file, hunk or comment after from, or from itself.
func (d Document) NextMark(from int) int {
	for i := from + 1; i < len(d.Rows); i++ {
		if d.IsMark(i) {
			return i
		}
	}
	return from
}

// PrevMark returns the previous file, hunk or comment before from, or from
// itself.
func (d Document) PrevMark(from int) int {
	for i := from - 1; i >= 0; i-- {
		if d.IsMark(i) {
			return i
		}
	}
	return from
}

// FirstStop returns the first row the cursor may rest on, or 0.
func (d Document) FirstStop() int {
	for i := range d.Rows {
		if d.IsStop(i) {
			return i
		}
	}
	return 0
}

// NextStop returns the next cursor position after from, or from itself.
func (d Document) NextStop(from int) int {
	for i := from + 1; i < len(d.Rows); i++ {
		if d.IsStop(i) {
			return i
		}
	}
	return from
}

// PrevStop returns the previous cursor position before from, or from itself.
func (d Document) PrevStop(from int) int {
	for i := from - 1; i >= 0; i-- {
		if d.IsStop(i) {
			return i
		}
	}
	return from
}

// StopBetween returns a cursor position within the inclusive row range, or -1
// when the range holds none. fromTop picks the first one, otherwise the last —
// so a window scrolled down lands the cursor on the row nearest the edge it
// arrived from.
func (d Document) StopBetween(lo, hi int, fromTop bool) int {
	lo, hi = max(lo, 0), min(hi, len(d.Rows)-1)
	if fromTop {
		for i := lo; i <= hi; i++ {
			if d.IsStop(i) {
				return i
			}
		}
		return -1
	}
	for i := hi; i >= lo; i-- {
		if d.IsStop(i) {
			return i
		}
	}
	return -1
}

// LastStop returns the final cursor position.
func (d Document) LastStop() int {
	for i := len(d.Rows) - 1; i >= 0; i-- {
		if d.IsStop(i) {
			return i
		}
	}
	return 0
}

// NextFile returns the top of the file after the one holding from.
func (d Document) NextFile(from int) int {
	for i := from + 1; i < len(d.Rows); i++ {
		if d.Rows[i].Kind == RowFile {
			return d.topOf(i)
		}
	}
	return from
}

// PrevFile returns the top of the file before the one holding from, stepping to
// the top of the current file first.
func (d Document) PrevFile(from int) int {
	start := d.topOf(d.RowOfFile(d.FileAt(from)))
	if start >= 0 && start < from {
		return start
	}
	for i := from - 1; i >= 0; i-- {
		if d.Rows[i].Kind == RowFile {
			return d.topOf(i)
		}
	}
	return from
}

// topOf returns the row a jump to a file header should land on: the walkthrough
// heading that introduces the file, when there is one, so jumping forward never
// skips the note written about the group the file opens.
func (d Document) topOf(row int) int {
	for row > 0 {
		switch d.Rows[row-1].Kind {
		case RowStep, RowStepText:
			row--
		default:
			return row
		}
	}
	return row
}

// Nearest returns the closest cursor position at or after i, falling back to
// the one before it.
func (d Document) Nearest(i int) int {
	if d.IsStop(i) {
		return i
	}
	if next := d.NextStop(i); d.IsStop(next) {
		return next
	}
	return d.PrevStop(i)
}

// FileAt returns the file index the row belongs to, or -1.
func (d Document) FileAt(row int) int {
	if row < 0 || row >= len(d.Rows) {
		return -1
	}
	return d.Rows[row].File
}

// StepAt returns the walkthrough group a heading or explanation row belongs to,
// and -1 on every other row.
func (d Document) StepAt(row int) int {
	if row < 0 || row >= len(d.Rows) {
		return -1
	}
	step := d.Rows[row].Step
	if step < 0 || step >= len(d.Steps) {
		return -1
	}
	return step
}

// RowOfFile returns the header row of a file, or -1.
func (d Document) RowOfFile(file int) int {
	if file < 0 || file >= len(d.Files) {
		return -1
	}
	return d.Files[file].Row
}

// RowOfHunk returns the header row of a hunk, or -1.
func (d Document) RowOfHunk(hunk int) int {
	for i, r := range d.Rows {
		if r.Kind == RowHunk && r.Hunk == hunk {
			return i
		}
	}
	return -1
}

// RowOfLine returns the row displaying a hunk line, or -1.
func (d Document) RowOfLine(hunk, line int) int {
	if hunk < 0 || line < 0 {
		return -1
	}
	for i, r := range d.Rows {
		if r.Kind == RowLine && r.Hunk == hunk && (r.Left == line || r.Right == line) {
			return i
		}
	}
	return -1
}

// TargetKind says what the cursor addresses.
type TargetKind int

const (
	// TargetNone has nothing to act on.
	TargetNone TargetKind = iota
	// TargetFile is a file as a whole.
	TargetFile
	// TargetHunk is one hunk.
	TargetHunk
	// TargetLine is the lines one row of a hunk body displays.
	TargetLine
)

// Target is what the cursor addresses.
type Target struct {
	Kind TargetKind
	Path string
	File int
	Hunk int
}

// TargetAt resolves the row under the cursor to the smallest thing it names,
// which is what a comment anchors to.
//
// A comment row acts on its file, so operating on a commented line does what it
// looks like it should. A walkthrough row acts on nothing: it names a group of
// files, and commenting on a whole group from one keypress is not what the row
// looks like it means.
func (d Document) TargetAt(row int) Target {
	if row < 0 || row >= len(d.Rows) {
		return Target{}
	}
	r := d.Rows[row]
	if r.Kind == RowStep || r.Kind == RowStepText {
		return Target{}
	}
	if r.Kind == RowHunk && r.Hunk >= 0 && r.Hunk < len(d.Hunks) {
		h := d.Hunks[r.Hunk]
		return Target{Kind: TargetHunk, Path: h.Path, File: h.File, Hunk: r.Hunk}
	}
	if r.Kind == RowLine && r.Hunk >= 0 && r.Hunk < len(d.Hunks) {
		h := d.Hunks[r.Hunk]
		return Target{Kind: TargetLine, Path: h.Path, File: h.File, Hunk: r.Hunk}
	}
	if r.File < 0 || r.File >= len(d.Files) {
		return Target{}
	}
	return Target{Kind: TargetFile, Path: d.Files[r.File].Entry.Path, File: r.File, Hunk: -1}
}

// FileTargetAt resolves the row under the cursor to the file staging acts on.
//
// Staging is whole-file, so a hunk header or a diff line resolves to the file it
// belongs to rather than to itself. A walkthrough row resolves to nothing: it
// names a group of files, and staging a whole group from one keypress is not
// what the row looks like it means.
func (d Document) FileTargetAt(row int) (FileRef, bool) {
	if row < 0 || row >= len(d.Rows) {
		return FileRef{}, false
	}
	r := d.Rows[row]
	if r.Kind == RowStep || r.Kind == RowStepText {
		return FileRef{}, false
	}
	if r.File < 0 || r.File >= len(d.Files) {
		return FileRef{}, false
	}
	return d.Files[r.File], true
}

// LineAt returns the hunk and the index of the line a row addresses, for
// anchoring a comment.
//
// Where a split row shows a removal beside the addition that replaced it, the
// new side wins: a review note is usually about the code that is arriving, not
// the code leaving.
func (d Document) LineAt(row int) (HunkRef, int, bool) {
	ref, r, ok := d.hunkRow(row)
	if !ok {
		return HunkRef{}, 0, false
	}
	for _, i := range []int{r.Right, r.Left} {
		if i >= 0 && i < len(ref.Hunk.Lines) {
			return ref, i, true
		}
	}
	return HunkRef{}, 0, false
}

// hunkRow returns a hunk-body row and the hunk it belongs to.
func (d Document) hunkRow(row int) (HunkRef, Row, bool) {
	if row < 0 || row >= len(d.Rows) {
		return HunkRef{}, Row{}, false
	}
	r := d.Rows[row]
	if r.Kind != RowLine || r.Hunk < 0 || r.Hunk >= len(d.Hunks) {
		return HunkRef{}, Row{}, false
	}
	return d.Hunks[r.Hunk], r, true
}

// CommentAt returns the comment displayed on a row.
func (d Document) CommentAt(row int) (store.Comment, bool) {
	if row < 0 || row >= len(d.Rows) {
		return store.Comment{}, false
	}
	r := d.Rows[row]
	if r.Kind != RowComment || r.Comment < 0 || r.Comment >= len(d.Comments) {
		return store.Comment{}, false
	}
	return d.Comments[r.Comment], true
}

// commentIndex places comments at the rows they anchor to, using each comment
// at most once so a line appearing on both the staged and unstaged side does
// not duplicate it.
type commentIndex struct {
	comments []store.Comment
	byFile   map[string][]int
	used     map[int]bool
}

func indexComments(cs []store.Comment) *commentIndex {
	idx := &commentIndex{comments: cs, byFile: map[string][]int{}, used: map[int]bool{}}
	for i, c := range cs {
		idx.byFile[c.File] = append(idx.byFile[c.File], i)
	}
	return idx
}

// takeFile returns the file-level comments for a path.
func (x *commentIndex) takeFile(path string) []int {
	return x.take(path, func(c store.Comment) bool { return c.Line <= 0 })
}

// takeLine returns comments anchored to either line of a displayed pair.
func (x *commentIndex) takeLine(path string, lines []git.Line, pair linePair) []int {
	return x.take(path, func(c store.Comment) bool {
		if c.Line <= 0 {
			return false
		}
		for _, i := range []int{pair.left, pair.right} {
			if i < 0 || i >= len(lines) {
				continue
			}
			if anchors(lines[i], c) {
				return true
			}
		}
		return false
	})
}

// rest returns the comments for a path that nothing claimed, so a comment whose
// line has since moved out of the diff still appears under its file.
func (x *commentIndex) rest(path string) []int {
	return x.take(path, func(store.Comment) bool { return true })
}

func (x *commentIndex) take(path string, match func(store.Comment) bool) []int {
	var out []int
	for _, i := range x.byFile[path] {
		if x.used[i] || !match(x.comments[i]) {
			continue
		}
		x.used[i] = true
		out = append(out, i)
	}
	return out
}

func anchors(l git.Line, c store.Comment) bool {
	if c.Side == store.SideOld {
		return l.OldLine == c.Line
	}
	return l.NewLine == c.Line
}
