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
	Text    string
	Head    bool
}

// HunkRef is one addressable hunk within the document.
type HunkRef struct {
	File int
	Path string
	// Staged reports that this hunk is in the index, so `u` unstages it and `s`
	// has nothing to do.
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

// Document is a session flattened into navigable rows.
type Document struct {
	Files    []FileRef
	Hunks    []HunkRef
	Rows     []Row
	Comments []store.Comment
	Layout   Layout
}

// Build flattens a session into rows. collapsed hides a file's body by path.
func Build(s *app.Session, comments []store.Comment, collapsed map[string]bool, layout Layout) Document {
	doc := Document{Comments: comments, Layout: layout}
	if s == nil {
		return doc
	}
	idx := indexComments(comments)

	for fi := range s.Files {
		entry := s.Files[fi]
		hidden := collapsed[entry.Path]
		doc.Files = append(doc.Files, FileRef{Entry: entry, Row: len(doc.Rows), Collapsed: hidden})
		doc.add(Row{Kind: RowFile, File: fi, Hunk: -1, Left: -1, Right: -1})
		doc.addComments(fi, -1, idx.takeFile(entry.Path))

		if !hidden {
			doc.addBody(fi, entry, idx)
		}
		doc.addComments(fi, -1, idx.rest(entry.Path))
		doc.add(Row{Kind: RowBlank, File: fi, Hunk: -1, Left: -1, Right: -1})
	}
	return doc
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
				Text:    text,
				Head:    n == 0,
			})
		}
	}
}

func (d *Document) addBody(fi int, entry git.FileEntry, idx *commentIndex) {
	if entry.IsBinary() {
		d.add(Row{Kind: RowNote, File: fi, Hunk: -1, Left: -1, Right: -1,
			Text: "binary file — whole-file staging only"})
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
			d.add(Row{Kind: RowHunk, File: fi, Hunk: hi, Left: -1, Right: -1})

			for _, pair := range pairLines(h.Lines, d.Layout) {
				d.add(Row{Kind: RowLine, File: fi, Hunk: hi, Left: pair.left, Right: pair.right})
				d.addComments(fi, hi, idx.takeLine(entry.Path, h.Lines, pair))
			}
		}
	}

	if len(d.Files[fi].Hunks) == 0 {
		d.add(Row{Kind: RowNote, File: fi, Hunk: -1, Left: -1, Right: -1, Text: emptyNote(entry)})
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
func (d Document) IsStop(i int) bool {
	if i < 0 || i >= len(d.Rows) {
		return false
	}
	switch d.Rows[i].Kind {
	case RowFile, RowHunk:
		return true
	case RowComment:
		return d.Rows[i].Head
	default:
		return false
	}
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

// LastStop returns the final cursor position.
func (d Document) LastStop() int {
	for i := len(d.Rows) - 1; i >= 0; i-- {
		if d.IsStop(i) {
			return i
		}
	}
	return 0
}

// NextFile returns the header row of the file after the one holding from.
func (d Document) NextFile(from int) int {
	for i := from + 1; i < len(d.Rows); i++ {
		if d.Rows[i].Kind == RowFile {
			return i
		}
	}
	return from
}

// PrevFile returns the header row of the file before the one holding from,
// stepping to the top of the current file first.
func (d Document) PrevFile(from int) int {
	start := d.RowOfFile(d.FileAt(from))
	if start >= 0 && start < from {
		return start
	}
	for i := from - 1; i >= 0; i-- {
		if d.Rows[i].Kind == RowFile {
			return i
		}
	}
	return from
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
	for i, r := range d.Rows {
		if r.Kind == RowLine && r.Hunk == hunk && (r.Left == line || r.Right == line) {
			return i
		}
	}
	return -1
}

// TargetKind says what a stage or unstage at the cursor applies to.
type TargetKind int

const (
	// TargetNone has nothing to act on.
	TargetNone TargetKind = iota
	// TargetFile applies to a whole file.
	TargetFile
	// TargetHunk applies to one hunk.
	TargetHunk
)

// Target is what an operation at the cursor addresses.
type Target struct {
	Kind TargetKind
	Path string
	File int
	Hunk int
	// Staged reports that the addressed change is already in the index.
	Staged bool
	// Binary marks a file that can only be staged whole.
	Binary bool
}

// TargetAt resolves the row under the cursor to something actionable.
//
// A comment row acts on its file, so operating on a commented line does what it
// looks like it should.
func (d Document) TargetAt(row int) Target {
	if row < 0 || row >= len(d.Rows) {
		return Target{}
	}
	r := d.Rows[row]
	if r.Kind == RowHunk && r.Hunk >= 0 && r.Hunk < len(d.Hunks) {
		h := d.Hunks[r.Hunk]
		return Target{Kind: TargetHunk, Path: h.Path, File: h.File, Hunk: r.Hunk, Staged: h.Staged}
	}
	if r.File < 0 || r.File >= len(d.Files) {
		return Target{}
	}
	entry := d.Files[r.File].Entry
	return Target{
		Kind:   TargetFile,
		Path:   entry.Path,
		File:   r.File,
		Hunk:   -1,
		Staged: entry.Staged != nil,
		Binary: entry.IsBinary(),
	}
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
