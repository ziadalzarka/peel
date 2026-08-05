package tui

import "github.com/ziadalzarka/peel/internal/git"

// expandStep is how many lines one press reads in.
//
// The same twenty GitHub uses: enough that a call site arrives in one press,
// small enough that a reviewer opening a run of hidden code sees where they are
// rather than the file falling open all at once.
const expandStep = 20

// FileSide names one half of one file: the copy its hunks are numbered against.
//
// A part-staged file has two of them and they are different files — the index's
// line 12 and the working tree's line 12 are not the same line — so the code
// read in around a hunk has to come out of the half that hunk belongs to.
type FileSide struct {
	Path string
	// Staged marks the index's side of the file, HEAD→index.
	Staged bool
}

// ExpandKey names one hunk and one side of it: the code read in going down from
// a hunk, or the code read in going up to one.
//
// It names a hunk rather than a run because that is what a press is actually
// about — carry this hunk on, carry this one back — and because a run is not a
// thing that survives a reload. Counting the runs would renumber every one of
// them the moment a change arrives above, so what had been read in at the bottom
// of a file would reappear around whichever run had taken its number; naming the
// pair of hunks either side would lose it instead, since a change landing in
// between splits the run in two. Hanging it off the one hunk it was read towards
// leaves it exactly where it was through both, and a hunk that is itself
// rewritten has a new ID, so the code read in around it reads as closed rather
// than as some other hunk's.
type ExpandKey struct {
	FileSide
	// Hunk is the hunk the code was read in from.
	Hunk git.HunkID
	// Up marks the code read in going up to that hunk — the run above it. False
	// is the run below it, read downwards.
	Up bool
}

// Reveal is how much of a run has been read in from each end.
//
// Two counts rather than one because a run between two hunks is opened from
// both: downward it carries the hunk above it further on, upward it carries the
// hunk below it further back. They meet in the middle and stop — what has been
// read from one end is not hidden from the other, and a line drawn twice would
// read as a line that is there twice.
type Reveal struct{ Top, Bottom int }

// clamp holds a reveal inside a run of n lines, so state kept from before the
// file changed cannot read past the end of the run it was recorded against.
func (r Reveal) clamp(n int) Reveal {
	r.Top = min(max(r.Top, 0), n)
	r.Bottom = min(max(r.Bottom, 0), n-r.Top)
	return r
}

// Expansion is the unchanged code a review has read in around its hunks, and
// the copies of the files it is read out of.
type Expansion struct {
	// Files holds each side's own copy of the file, line by line. A side with no
	// entry cannot be expanded at all, which is what leaves a pull request — code
	// that is not in this working tree — with no rows offering to.
	Files map[FileSide][]string
	// Revealed is how many lines have been read in from each hunk, each way.
	Revealed map[ExpandKey]int
}

// ExpandDir is which end of a run of hidden lines a row opens.
type ExpandDir int

const (
	// ExpandDown opens the top of the run, carrying the hunk above it on.
	ExpandDown ExpandDir = iota
	// ExpandUp opens the bottom of the run, carrying the hunk below it back.
	ExpandUp
	// ExpandAll opens a run short enough that one press finishes it, so it is
	// offered as one row rather than as two ends of something still hidden.
	ExpandAll
)

// ExpandRef is one row offering to read in code the diff left out, as the
// document lays it out.
type ExpandRef struct {
	ExpandKey
	// File indexes into Document.Files.
	File int
	// Dir is which end of the run this row opens.
	Dir ExpandDir
	// Hidden is how many lines the run still holds back — the whole run, not
	// what one press of this row would show.
	Hidden int
	// Row is the position of the row.
	Row int
}

// gap is one run of unchanged lines a side's diff leaves out, in the numbering
// of the file that side is measured against.
type gap struct {
	// first and last are the new-side line numbers the run covers, inclusive. A
	// run with nothing in it has last below first.
	first, last int
	// delta is how far ahead of the old-side numbering the new side runs through
	// this gap, which is what gives a revealed line its second number.
	delta int
	// above and below are the hunks the run lies between. One of them is zero at
	// each end of the file, where there is no hunk on that side.
	above, below git.HunkID
}

// down names the code read in from the hunk above the run, and up the code read
// in from the hunk below it.
func (g gap) down(side FileSide) ExpandKey { return ExpandKey{FileSide: side, Hunk: g.above} }
func (g gap) up(side FileSide) ExpandKey   { return ExpandKey{FileSide: side, Hunk: g.below, Up: true} }

func (g gap) len() int { return max(g.last-g.first+1, 0) }

// gaps is one side's runs of hidden lines, together with the file they would be
// read out of and how far each has been opened.
type gaps struct {
	side  FileSide
	lines []string
	runs  []gap
	// reveal is parallel to runs, already clamped to what each run holds.
	reveal []Reveal
	// ok reports that the side's file could be read. Without it there is nothing
	// to reveal and no row is drawn offering to.
	ok bool
}

// gapsOf works out where a side's diff leaves code out. id names a hunk the way
// the rest of peel names it, which is what the runs between them are keyed on.
//
// Between two hunks the answer is arithmetic — the lines one ends on and the
// next begins at — but past the last hunk it is the file's own length, which is
// why nothing can be offered for a side whose file was not read.
func gapsOf(side FileSide, hunks []git.Hunk, id func(git.Hunk) git.HunkID, x Expansion) gaps {
	lines, ok := x.Files[side]
	out := gaps{side: side, lines: lines, ok: ok}
	if !ok {
		return out
	}

	end, delta, above := 0, 0, git.HunkID{}
	for _, h := range hunks {
		out.runs = append(out.runs, gap{first: end + 1, last: newFirst(h) - 1, delta: delta, above: above, below: id(h)})
		end, delta, above = newLast(h), newLast(h)-oldLast(h), id(h)
	}
	out.runs = append(out.runs, gap{first: end + 1, last: len(lines), delta: delta, above: above})

	for i, run := range out.runs {
		// A file read a moment after the diff it is being measured against can be
		// shorter than the diff says: the run is cut to what the file actually
		// holds rather than reading off the end of it.
		out.runs[i].last = min(run.last, len(lines))
		read := Reveal{Top: x.Revealed[run.down(side)], Bottom: x.Revealed[run.up(side)]}
		out.reveal = append(out.reveal, read.clamp(out.runs[i].len()))
	}
	return out
}

// newFirst and newLast are the new-side lines a hunk covers.
//
// A hunk that only removes lines covers none of them, and git numbers it from
// the line before the removal — so it ends on that line and the run after it
// starts on the next, which is what leaves the span between them empty.
func newFirst(h git.Hunk) int {
	if h.NewCount == 0 {
		return h.NewStart + 1
	}
	return h.NewStart
}

func newLast(h git.Hunk) int {
	if h.NewCount == 0 {
		return h.NewStart
	}
	return h.NewStart + h.NewCount - 1
}

// oldLast is the same for the old side, which is what the two numberings are
// compared at to work out how far they have drifted apart.
func oldLast(h git.Hunk) int {
	if h.OldCount == 0 {
		return h.OldStart
	}
	return h.OldStart + h.OldCount - 1
}

func (g gaps) has(i int) bool { return g.ok && i >= 0 && i < len(g.runs) }

// hidden is how many lines run i is still holding back.
func (g gaps) hidden(i int) int {
	if !g.has(i) {
		return 0
	}
	r := g.reveal[i]
	return g.runs[i].len() - r.Top - r.Bottom
}

// top returns the lines read in from the start of run i: the code that follows
// the hunk above it, which is drawn as a continuation of that hunk.
func (g gaps) top(i int) []git.Line {
	if !g.has(i) {
		return nil
	}
	return g.read(g.runs[i], g.runs[i].first, g.reveal[i].Top)
}

// bottom returns the lines read in from the end of run i: the code that comes
// before the hunk below it, drawn as that hunk's own first lines.
func (g gaps) bottom(i int) []git.Line {
	if !g.has(i) {
		return nil
	}
	n := g.reveal[i].Bottom
	return g.read(g.runs[i], g.runs[i].last-n+1, n)
}

// read turns count lines of the file, from the given new-side number, into diff
// lines. They are unchanged code, so each carries a number on both sides and
// reads exactly as the context git printed itself does.
func (g gaps) read(run gap, from, count int) []git.Line {
	out := make([]git.Line, 0, max(count, 0))
	for n := from; n < from+count; n++ {
		if n < 1 || n > len(g.lines) {
			continue
		}
		out = append(out, git.Line{
			Kind:    git.LineContext,
			Text:    g.lines[n-1],
			OldLine: n - run.delta,
			NewLine: n,
		})
	}
	return out
}

// keyOf names what a row on run i opens: the code below the hunk above it, or
// the code above the hunk below it.
func (g gaps) keyOf(i int, dir ExpandDir) ExpandKey {
	if !g.has(i) {
		return ExpandKey{}
	}
	if dir == ExpandUp {
		return g.runs[i].up(g.side)
	}
	return g.runs[i].down(g.side)
}

// below returns the row hanging under the hunk above run i, when there is one to
// draw.
//
// A run between two hunks that is longer than a press is opened from both ends,
// so it gets this row and the one above the hunk below it. A run short enough to
// finish in one press gets only this one: two rows offering the same twelve
// lines is a choice about nothing.
func (g gaps) below(i, hunks int) (ExpandDir, bool) {
	if g.hidden(i) == 0 || i == 0 {
		return 0, false
	}
	if i == hunks {
		return ExpandDown, true
	}
	if g.hidden(i) <= expandStep {
		return ExpandAll, true
	}
	return ExpandDown, true
}

// above returns the row sitting over the head of the hunk below run i.
func (g gaps) above(i, hunks int) (ExpandDir, bool) {
	if g.hidden(i) == 0 || i == hunks {
		return 0, false
	}
	if i == 0 {
		return ExpandUp, true
	}
	if g.hidden(i) <= expandStep {
		return 0, false
	}
	return ExpandUp, true
}

// withContext puts a hunk's revealed lines around the ones git printed, so what
// was read in is part of the hunk rather than a second thing beside it: the
// cursor walks it, a note anchors to it, and both of its line numbers are the
// numbers the diff would have given it.
func withContext(head, lines, tail []git.Line) []git.Line {
	if len(head) == 0 && len(tail) == 0 {
		return lines
	}
	out := make([]git.Line, 0, len(head)+len(lines)+len(tail))
	out = append(out, head...)
	out = append(out, lines...)
	return append(out, tail...)
}
