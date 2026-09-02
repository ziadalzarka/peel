package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/gittest"
	"github.com/ziadalzarka/peel/internal/store"
)

// contextDiff changes one line near the top of a sixty-line file and another
// near the bottom, so the diff leaves out a line above the first hunk,
// thirty-eight between the two, and seven past the last.
const contextDiff = `diff --git a/wide.go b/wide.go
index 1111111..2222222 100644
--- a/wide.go
+++ b/wide.go
@@ -2,7 +2,7 @@
 line 2
 line 3
 line 4
-line 5
+line five
 line 6
 line 7
 line 8
@@ -47,7 +47,7 @@
 line 47
 line 48
 line 49
-line 50
+line fifty
 line 51
 line 52
 line 53
`

// contextFile is the working tree's copy of that file: what the diff was
// measured against, and what the code it left out is read back out of.
func contextFile() []string {
	lines := make([]string, 0, 60)
	for i := 1; i <= 60; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	lines[4] = "line five"
	lines[49] = "line fifty"
	return lines
}

// wideSide names the only side contextDiff has.
var wideSide = FileSide{Path: "wide.go"}

// expansionOf pairs the file with however far the runs of hidden code have been
// opened.
func expansionOf(revealed map[ExpandKey]int) Expansion {
	return Expansion{
		Files:    map[FileSide][]string{wideSide: contextFile()},
		Revealed: revealed,
	}
}

// hunkKey names the code read in from one hunk of a one-file diff: below it, or
// above it when up is set.
func hunkKey(diff string, side FileSide, hunk int, up bool) ExpandKey {
	parsed, err := git.ParseDiff(diff)
	if err != nil {
		panic(err)
	}
	f := parsed.Files[0]
	return ExpandKey{FileSide: side, Hunk: f.ID(f.Hunks[hunk]), Up: up}
}

// down and up name the two things a press can grow on contextDiff's hunks: the
// code below hunk n, and the code above it.
func down(hunk int) ExpandKey { return hunkKey(contextDiff, wideSide, hunk, false) }
func up(hunk int) ExpandKey   { return hunkKey(contextDiff, wideSide, hunk, true) }

// expandKinds lists the rows a document holds, so a test can say where the
// offers to read more sit among the hunks and their lines.
func expandKinds(doc Document) []RowKind {
	var out []RowKind
	for _, r := range doc.Rows {
		out = append(out, r.Kind)
	}
	return out
}

// newNumbers lists the new-side line numbers a document draws, in the order it
// draws them. Removals carry none and are left out.
func newNumbers(doc Document) []int {
	var out []int
	for _, r := range doc.Rows {
		if r.Kind != RowLine {
			continue
		}
		line := doc.Hunks[r.Hunk].Hunk.Lines[max(r.Left, r.Right)]
		if line.NewLine > 0 {
			out = append(out, line.NewLine)
		}
	}
	return out
}

// lineTexts is the same for the code itself.
func lineTexts(doc Document) []string {
	var out []string
	for _, r := range doc.Rows {
		if r.Kind != RowLine {
			continue
		}
		out = append(out, doc.Hunks[r.Hunk].Hunk.Lines[max(r.Left, r.Right)].Text)
	}
	return out
}

// A hunk's three lines of context stop where git stopped printing, and what is
// left out is marked: one row per end of each run, against the hunk it would
// carry further.
func TestBuildMarksWhereTheDiffLeavesCodeOut(t *testing.T) {
	doc := Build(newSession(t, contextDiff), nil, nil, LayoutUnified, WithExpansion(expansionOf(nil)))

	want := []struct {
		key    ExpandKey
		dir    ExpandDir
		hidden int
	}{
		// Nothing above the first hunk to carry down, so the single line over it
		// is offered from below only.
		{up(0), ExpandUp, 1},
		// The long run between the hunks opens from either end.
		{down(0), ExpandDown, 38},
		{up(1), ExpandUp, 38},
		// Nothing below the last hunk to carry back, and the seven lines past it
		// are known only because the file itself was read.
		{down(1), ExpandDown, 7},
	}
	if len(doc.Expands) != len(want) {
		t.Fatalf("marked %d runs, want %d: %+v", len(doc.Expands), len(want), doc.Expands)
	}
	for i, w := range want {
		got := doc.Expands[i]
		if got.ExpandKey != w.key || got.Dir != w.dir || got.Hidden != w.hidden {
			t.Errorf("run %d = %+v, want %v %v hiding %d", i, got, w.key, w.dir, w.hidden)
		}
		if doc.Rows[got.Row].Kind != RowExpand || doc.Rows[got.Row].Expand != i {
			t.Errorf("run %d is not at row %d: %+v", i, got.Row, doc.Rows[got.Row])
		}
	}

	// The row over a hunk sits under its header, where the lines it opens will
	// arrive; the row under a hunk comes after that hunk's last line.
	want2 := []RowKind{
		RowFile,
		RowHunk, RowExpand, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowExpand,
		RowHunk, RowExpand, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowExpand,
		RowBlank,
	}
	if got := expandKinds(doc); !equalKinds(got, want2) {
		t.Errorf("rows = %v,\nwant %v", got, want2)
	}
}

func equalKinds(a, b []RowKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Without a copy of the file there is nothing to read in, so nothing offers to.
// This is the pull request case: the code is not in this working tree.
func TestBuildOffersNothingWithoutTheFile(t *testing.T) {
	doc := Build(newSession(t, contextDiff), nil, nil, LayoutUnified)

	if len(doc.Expands) != 0 {
		t.Errorf("marked %d runs with no file to read them from", len(doc.Expands))
	}
	for _, r := range doc.Rows {
		if r.Kind == RowExpand {
			t.Fatal("a row offered to read in code peel has no copy of")
		}
	}
}

// Opening the top of a run carries the hunk above it on: the lines arrive as
// that hunk's own, numbered as the diff would have numbered them.
func TestOpeningTheTopOfARunExtendsTheHunkAbove(t *testing.T) {
	doc := Build(newSession(t, contextDiff), nil, nil, LayoutUnified,
		WithExpansion(expansionOf(map[ExpandKey]int{down(0): 20})))

	if got := len(doc.Hunks[0].Hunk.Lines); got != 28 {
		t.Fatalf("the first hunk draws %d lines, want its own 8 and 20 read in", got)
	}
	read := doc.Hunks[0].Hunk.Lines[8:]
	for i, l := range read {
		wantNum := 9 + i
		if l.Kind != git.LineContext {
			t.Fatalf("line %d read in as %v, want unchanged context", wantNum, l.Kind)
		}
		if l.NewLine != wantNum || l.OldLine != wantNum {
			t.Errorf("read line %d is numbered %d/%d", i, l.OldLine, l.NewLine)
		}
		if want := fmt.Sprintf("line %d", wantNum); l.Text != want {
			t.Errorf("read line %d = %q, want %q", i, l.Text, want)
		}
	}

	// Eighteen are left, few enough to finish in one press, so the two ends
	// become the single row that finishes them.
	if len(doc.Expands) != 3 {
		t.Fatalf("runs = %+v, want the two ends of the long one down to one row", doc.Expands)
	}
	if got := doc.Expands[1]; got.Dir != ExpandAll || got.Hidden != 18 {
		t.Errorf("the shortened run = %+v, want one row hiding 18", got)
	}
}

// Opening the bottom of a run carries the hunk below it back, and the lines are
// drawn under that hunk's header — where the code that leads up to it belongs.
func TestOpeningTheBottomOfARunExtendsTheHunkBelow(t *testing.T) {
	doc := Build(newSession(t, contextDiff), nil, nil, LayoutUnified,
		WithExpansion(expansionOf(map[ExpandKey]int{up(1): 20})))

	if got := len(doc.Hunks[1].Hunk.Lines); got != 28 {
		t.Fatalf("the second hunk draws %d lines, want its own 8 and 20 read in", got)
	}
	for i, l := range doc.Hunks[1].Hunk.Lines[:20] {
		if want := 27 + i; l.NewLine != want {
			t.Errorf("read line %d is numbered %d, want %d", i, l.NewLine, want)
		}
	}
	if got := doc.Hunks[1].Hunk.Lines[20].Text; got != "line 47" {
		t.Errorf("the hunk's own first line is now %q, want line 47", got)
	}
	if got := newNumbers(doc); !strings.HasPrefix(fmt.Sprint(got), "[2 3 4 5 6 7 8 27 28") {
		t.Errorf("line numbers = %v, want the read-in run to start at 27", got)
	}
}

// A run opened from both ends meets in the middle and stops. Nothing is drawn
// twice, which is what would make a file look like it had a line in it twice.
func TestARunOpenedFromBothEndsIsNeverDrawnTwice(t *testing.T) {
	doc := Build(newSession(t, contextDiff), nil, nil, LayoutUnified,
		WithExpansion(expansionOf(map[ExpandKey]int{down(0): 20, up(1): 30})))

	numbers := newNumbers(doc)
	for i := range numbers {
		if want := 2 + i; numbers[i] != want {
			t.Fatalf("line %d of the diff is numbered %d, want %d: %v", i, numbers[i], want, numbers)
		}
	}
	if len(numbers) != 52 {
		t.Errorf("the diff draws %d numbered lines, want 2 through 53", len(numbers))
	}
	for _, e := range doc.Expands {
		if e.ExpandKey == down(0) || e.ExpandKey == up(1) {
			t.Errorf("the run between the hunks still offers %+v with nothing left in it", e)
		}
	}
}

// The two hunks now run into each other, and what the reviewer reads is the
// file itself with the change marked in it.
func TestAFullyOpenedRunLeavesTheCodeContinuous(t *testing.T) {
	doc := Build(newSession(t, contextDiff), nil, nil, LayoutUnified,
		WithExpansion(expansionOf(map[ExpandKey]int{
			up(0):   1,
			down(0): 38,
			down(1): 7,
		})))

	if len(doc.Expands) != 0 {
		t.Errorf("runs left = %+v, want none", doc.Expands)
	}
	texts := lineTexts(doc)
	if got := texts[0]; got != "line 1" {
		t.Errorf("the diff opens on %q, want the first line of the file", got)
	}
	if got := texts[len(texts)-1]; got != "line 60" {
		t.Errorf("the diff ends on %q, want the last line of the file", got)
	}
}

// sectionDiff changes a line at the top of a file and another forty-two lines
// below it, with a second declaration in the run between them: the hunk at the
// bottom is the one git names a section for.
const sectionDiff = `diff --git a/pagination.kt b/pagination.kt
index 1111111..2222222 100644
--- a/pagination.kt
+++ b/pagination.kt
@@ -1,7 +1,7 @@
 sealed interface Pagination {
     val v2 = 0
     val v3 = 0
-    val v4 = 0
+    val v4 = 1
     val v5 = 0
     val v6 = 0
     val v7 = 0
@@ -50,7 +50,7 @@ sealed interface Cursor {
     val v50 = 0
     val v51 = 0
     val v52 = 0
-    val v53 = 0
+    val v53 = 1
     val v54 = 0
     val v55 = 0
     val v56 = 0
`

// cursorSection is what git named the second hunk after its @@.
const cursorSection = "sealed interface Cursor {"

// sectionFile is the working tree's copy of that file, with the declaration the
// second hunk is named after sitting on line 30, inside the run of code the diff
// leaves out.
func sectionFile() []string {
	lines := make([]string, 0, 60)
	for i := 1; i <= 60; i++ {
		lines = append(lines, fmt.Sprintf("    val v%d = 0", i))
	}
	lines[0] = "sealed interface Pagination {"
	lines[3] = "    val v4 = 1"
	lines[29] = cursorSection
	lines[52] = "    val v53 = 1"
	lines[59] = "}"
	return lines
}

var sectionSide = FileSide{Path: "pagination.kt"}

// sectionDown and sectionUp name the run between sectionDiff's two hunks, from
// the hunk above it and from the hunk below it.
func sectionDown(hunk int) ExpandKey { return hunkKey(sectionDiff, sectionSide, hunk, false) }
func sectionUp(hunk int) ExpandKey   { return hunkKey(sectionDiff, sectionSide, hunk, true) }

// sectionDoc builds that diff with the run opened as far as revealed says.
func sectionDoc(t *testing.T, revealed map[ExpandKey]int) Document {
	t.Helper()
	return Build(newSession(t, sectionDiff), nil, nil, LayoutUnified, WithExpansion(Expansion{
		Files:    map[FileSide][]string{sectionSide: sectionFile()},
		Revealed: revealed,
	}))
}

// The diff cuts the file off above the hunk, so the header says what encloses
// it.
func TestAHunkHeaderNamesTheSectionTheDiffCutOff(t *testing.T) {
	doc := sectionDoc(t, nil)

	if doc.Hunks[1].SectionShown {
		t.Fatalf("hunk %+v reads as already showing its section, with the run above it closed", doc.Hunks[1].Hunk.Section)
	}
	if got := plainRenderer(80).Row(doc, doc.RowOfHunk(1), RowState{}); !strings.Contains(got, cursorSection) {
		t.Errorf("hunk header = %q, want it to name %q", got, cursorSection)
	}
}

// Once the run above the hunk is open the declaration is on screen a few rows
// up, and the header repeating it is one more line of the same words.
func TestAHunkHeaderDropsASectionThatHasBeenReadIn(t *testing.T) {
	doc := sectionDoc(t, map[ExpandKey]int{sectionDown(0): 42})

	if !doc.Hunks[1].SectionShown {
		t.Fatalf("hunk reads as still hiding %q, with the code up to it open", doc.Hunks[1].Hunk.Section)
	}
	got := plainRenderer(80).Row(doc, doc.RowOfHunk(1), RowState{})
	if strings.Contains(got, cursorSection) {
		t.Errorf("hunk header = %q, want the section dropped: the line is drawn above it", got)
	}
	if !strings.Contains(got, "⋯") {
		t.Errorf("hunk header = %q, want a separator mark in place of the section", got)
	}
}

// The same holds for a hunk read back towards the declaration from below: the
// line arrives under the header rather than over it, and is just as much on
// screen.
func TestAHunkHeaderDropsASectionItsOwnHeadReadsBackTo(t *testing.T) {
	doc := sectionDoc(t, map[ExpandKey]int{sectionUp(1): 20})

	if !doc.Hunks[1].SectionShown {
		t.Errorf("hunk reads as still hiding %q, with its head read back past the line", doc.Hunks[1].Hunk.Section)
	}
}

// Code still left out between the declaration and the hunk is the case the
// header is for: something else could start inside what is hidden, so git's
// answer still says more than the screen does.
func TestAHunkHeaderKeepsASectionWithCodeStillHiddenUnderIt(t *testing.T) {
	doc := sectionDoc(t, map[ExpandKey]int{sectionDown(0): 25})

	if !strings.Contains(strings.Join(lineTexts(doc), "\n"), cursorSection) {
		t.Fatalf("the read-in code does not reach %q, so there is nothing to repeat", cursorSection)
	}
	if doc.Hunks[1].SectionShown {
		t.Errorf("hunk reads as showing its section with lines still hidden between them")
	}
	if got := plainRenderer(80).Row(doc, doc.RowOfHunk(1), RowState{}); !strings.Contains(got, cursorSection) {
		t.Errorf("hunk header = %q, want it to keep naming %q across the gap", got, cursorSection)
	}
}

// insertDiff adds a line, so the two sides stop counting alike: a line read in
// below the change is a different number on each of them, and taking the new
// one for both would put a note on the wrong line of the old file.
const insertDiff = `diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,4 +1,5 @@
 one
 two
+inserted
 three
 four
`

func TestAReadLineKeepsBothOfItsNumbers(t *testing.T) {
	side := FileSide{Path: "notes.txt"}
	doc := Build(newSession(t, insertDiff), nil, nil, LayoutUnified, WithExpansion(Expansion{
		Files:    map[FileSide][]string{side: {"one", "two", "inserted", "three", "four", "five", "six", "seven"}},
		Revealed: map[ExpandKey]int{hunkKey(insertDiff, side, 0, false): 3},
	}))

	lines := doc.Hunks[0].Hunk.Lines
	read := lines[len(lines)-3:]
	want := []git.Line{
		{Kind: git.LineContext, Text: "five", OldLine: 5, NewLine: 6},
		{Kind: git.LineContext, Text: "six", OldLine: 6, NewLine: 7},
		{Kind: git.LineContext, Text: "seven", OldLine: 7, NewLine: 8},
	}
	for i := range want {
		if read[i] != want[i] {
			t.Errorf("read line %d = %+v, want %+v", i, read[i], want[i])
		}
	}
}

// A run short enough to finish in one press is one row, not two ends of
// something still hidden: two offers over the same twelve lines is a choice
// about nothing.
func TestAShortRunBetweenHunksIsOneRow(t *testing.T) {
	doc := Build(newSession(t, contextDiff), nil, nil, LayoutUnified,
		WithExpansion(expansionOf(map[ExpandKey]int{down(0): 30})))

	var between []ExpandRef
	for _, e := range doc.Expands {
		if e.ExpandKey == down(0) || e.ExpandKey == up(1) {
			between = append(between, e)
		}
	}
	if len(between) != 1 {
		t.Fatalf("the shortened run has %d rows, want one", len(between))
	}
	if between[0].Dir != ExpandAll || between[0].Hidden != 8 {
		t.Errorf("the row = %+v, want one press over the last 8 lines", between[0])
	}
}

// A file whose last hunk runs to the end of it has nothing past that hunk, and
// says nothing.
func TestNothingIsOfferedPastTheEndOfTheFile(t *testing.T) {
	side := FileSide{Path: "notes.txt"}
	doc := Build(newSession(t, insertDiff), nil, nil, LayoutUnified, WithExpansion(Expansion{
		Files: map[FileSide][]string{side: {"one", "two", "inserted", "three", "four"}},
	}))

	if len(doc.Expands) != 0 {
		t.Errorf("runs = %+v, want none: the hunk covers the whole file", doc.Expands)
	}
}

// Reading code in is a display change. The hunks keep the IDs the CLI and the
// agent address them by, since nothing about the change itself has moved.
func TestReadingCodeInLeavesHunkIDsAlone(t *testing.T) {
	plain := Build(newSession(t, contextDiff), nil, nil, LayoutUnified, WithExpansion(expansionOf(nil)))
	opened := Build(newSession(t, contextDiff), nil, nil, LayoutUnified,
		WithExpansion(expansionOf(map[ExpandKey]int{down(0): 20, up(1): 18})))

	for i := range plain.Hunks {
		if got, want := opened.Hunks[i].ID, plain.Hunks[i].ID; got != want {
			t.Errorf("hunk %d is addressed as %s, want %s", i, got, want)
		}
	}
}

// State kept from before a file changed cannot read past the end of the run it
// was recorded against, so a shrinking file cannot be drawn out of one that has
// already gone.
func TestAStaleReadIsCutToWhatTheFileHolds(t *testing.T) {
	doc := Build(newSession(t, contextDiff), nil, nil, LayoutUnified, WithExpansion(Expansion{
		Files:    map[FileSide][]string{wideSide: contextFile()[:20]},
		Revealed: map[ExpandKey]int{down(0): 500, down(1): 500},
	}))

	// The run between the hunks reaches line 46 by the diff's arithmetic but the
	// file stops at 20, so twelve lines is all there is to read: the rest of the
	// ask is dropped rather than run off the end.
	if got := len(doc.Hunks[0].Hunk.Lines); got != 20 {
		t.Fatalf("the first hunk draws %d lines, want its own 8 and the 12 the file still has", got)
	}
	for _, l := range doc.Hunks[0].Hunk.Lines[8:] {
		if l.NewLine > 20 {
			t.Errorf("line %d was read out of a file with 20 lines in it", l.NewLine)
		}
	}
	// Past the last hunk the file has already ended, so there was nothing there
	// to ask for.
	if got := len(doc.Hunks[1].Hunk.Lines); got != 8 {
		t.Errorf("the second hunk draws %d lines, want only its own 8", got)
	}
}

// The split layout draws a read-in line on both sides at once, the way it draws
// any other unchanged line.
func TestReadCodeIsPairedInTheSplitLayout(t *testing.T) {
	doc := Build(newSession(t, contextDiff), nil, nil, LayoutSplit,
		WithExpansion(expansionOf(map[ExpandKey]int{down(0): 3})))

	last := doc.Rows[0]
	for _, r := range doc.Rows {
		if r.Kind == RowLine && r.Hunk == 0 {
			last = r
		}
	}
	if last.Left < 0 || last.Right < 0 {
		t.Errorf("the last read-in row is %+v, want the same line on both sides", last)
	}
	if got := doc.Hunks[0].Hunk.Lines[last.Left].Text; got != "line 11" {
		t.Errorf("the last read-in line is %q, want line 11", got)
	}
}

// stagedGapDiff and workingGapDiff are one file git holds in both places at
// once, each half leaving different code out.
const stagedGapDiff = `diff --git a/both.txt b/both.txt
index 1111111..2222222 100644
--- a/both.txt
+++ b/both.txt
@@ -1,3 +1,3 @@
 1
-old
+2
 3
`

const workingGapDiff = `diff --git a/both.txt b/both.txt
index 2222222..3333333 100644
--- a/both.txt
+++ b/both.txt
@@ -8,3 +8,3 @@
 8
-old
+9
 10
`

// The two halves of a part-staged file are measured against different files, so
// the code read in around each has to come out of that half's own copy — the
// index's line 4 is not the working tree's line 4.
func TestEachHalfReadsItsOwnCopyOfTheFile(t *testing.T) {
	staged := parseFiles(t, stagedGapDiff)[0].Unstaged
	working := parseFiles(t, workingGapDiff)[0].Unstaged
	session := sessionOf([]git.FileEntry{{Path: "both.txt", Staged: staged, Unstaged: working}})

	indexed, disk := make([]string, 10), make([]string, 10)
	for i := range indexed {
		indexed[i] = fmt.Sprintf("index %d", i+1)
		disk[i] = fmt.Sprintf("work %d", i+1)
	}

	doc := Build(session, nil, nil, LayoutUnified,
		WithSideFolds(map[string]bool{"both.txt": false}),
		WithExpansion(Expansion{
			Files: map[FileSide][]string{
				{Path: "both.txt", Staged: true}: indexed,
				{Path: "both.txt"}:               disk,
			},
			Revealed: map[ExpandKey]int{
				hunkKey(stagedGapDiff, FileSide{Path: "both.txt", Staged: true}, 0, false): 2,
				hunkKey(workingGapDiff, FileSide{Path: "both.txt"}, 0, true):               2,
			},
		}))

	if len(doc.Hunks) != 2 {
		t.Fatalf("hunks = %d, want one per half", len(doc.Hunks))
	}
	index := doc.Hunks[0].Hunk.Lines
	if got := index[len(index)-2].Text; got != "index 4" {
		t.Errorf("the staged half read in %q, want the index's line 4", got)
	}
	if got := doc.Hunks[1].Hunk.Lines[0].Text; got != "work 6" {
		t.Errorf("the working half read in %q, want the file on disk at line 6", got)
	}
}

// expandModel opens a review on contextDiff with the file behind it available,
// and runs the read Init asks for — so the rows offering to show what the diff
// left out are on screen, as they are a moment after peel starts.
func expandModel(t *testing.T, opts ...Option) (*fakeBackend, *Model) {
	t.Helper()
	backend := newFakeBackend(newSession(t, contextDiff))
	backend.context = map[FileSide][]string{wideSide: contextFile()}
	m := newModel(t, backend, opts...)
	settle(t, m, m.Init(), 0)
	return backend, m
}

// Nothing offers to read in code until the files behind the diff have been
// read, which happens off the first frame.
func TestNothingIsOfferedUntilTheFilesAreRead(t *testing.T) {
	backend := newFakeBackend(newSession(t, contextDiff))
	backend.context = map[FileSide][]string{wideSide: contextFile()}
	m := newModel(t, backend)

	if len(m.doc.Expands) != 0 {
		t.Errorf("the first frame offered %+v with no file read yet", m.doc.Expands)
	}
	settle(t, m, m.Init(), 0)
	if len(m.doc.Expands) == 0 {
		t.Error("the files were read and nothing offered to show what the diff left out")
	}
}

func TestSpaceReadsInAStepOfTheCodeAroundAHunk(t *testing.T) {
	_, m := expandModel(t)

	row := m.doc.RowOfExpand(down(0), ExpandDown)
	if row < 0 {
		t.Fatal("the run between the hunks is not marked")
	}
	m.moveTo(row)
	press(t, m, "space")

	if got := len(m.doc.Hunks[0].Hunk.Lines); got != 28 {
		t.Errorf("the hunk draws %d lines, want 20 more than its own 8", got)
	}
	if got := m.status; !strings.Contains(got, "20 lines") {
		t.Errorf("status = %q, want it to say how much arrived", got)
	}
	// The cursor stays on the row, so holding space walks outwards from the hunk
	// rather than being left behind by the code arriving above it.
	if got := m.doc.ExpandAt(m.cursor); got < 0 {
		t.Fatalf("the cursor is on a %v, want the row it was pressed on", m.doc.Rows[m.cursor].Kind)
	}
	if got := m.doc.Expands[m.doc.ExpandAt(m.cursor)]; got.ExpandKey != down(0) || got.Hidden != 18 {
		t.Errorf("the cursor is on %+v, want the same run with 18 left", got)
	}
}

func TestSpaceFinishesARunAndLeavesTheCursorOnTheCode(t *testing.T) {
	_, m := expandModel(t)

	m.moveTo(m.doc.RowOfExpand(down(0), ExpandDown))
	press(t, m, "space", "space")

	if got := m.doc.RowOfExpand(down(0), ExpandDown); got >= 0 {
		t.Errorf("the run is still marked at row %d with nothing left in it", got)
	}
	if got := m.doc.Rows[m.cursor].Kind; got != RowLine {
		t.Fatalf("the cursor is on a %v, want the code that arrived where the row was", got)
	}
	if m.cursor < m.top || m.cursor >= m.top+m.bodyHeight() {
		t.Errorf("cursor %d is outside the window [%d,%d)", m.cursor, m.top, m.top+m.bodyHeight())
	}
}

// The same key still folds a file away: what it acts on is whatever the cursor
// names, and a file header names the file.
func TestSpaceStillFoldsTheFileItIsPressedOn(t *testing.T) {
	_, m := expandModel(t)

	m.moveTo(m.doc.RowOfFile(0))
	press(t, m, "space")

	if !m.collapsed["wide.go"] {
		t.Error("space on the file header did not fold the file")
	}
}

// A note left on code that was read in anchors to the line it is actually on,
// so it comes back on that line rather than under the file.
func TestANoteOnReadInCodeAnchorsToItsLine(t *testing.T) {
	backend, m := expandModel(t)

	m.moveTo(m.doc.RowOfExpand(down(0), ExpandDown))
	press(t, m, "space")
	// One step back off the row is the last line that arrived under it.
	press(t, m, "up", "c")
	typeText(t, m, "what is this for")
	press(t, m, "enter")

	if len(backend.added) != 1 {
		t.Fatalf("added %d comments, want 1", len(backend.added))
	}
	got := backend.added[0]
	if got.File != "wide.go" || got.Line != 28 || got.Side != store.SideNew {
		t.Errorf("the note landed on %s:%d (%s), want wide.go:28 on the new side", got.File, got.Line, got.Side)
	}
	if got.Origin != store.OriginWorktree {
		t.Errorf("the note records origin %q, want the working tree's diff", got.Origin)
	}
}

// A file staged is a file read again, and until that read lands the copies
// already in hand are the ones on screen. Taking them down for the length of a
// git call would blink every offer to read more, on every file, out and back in
// each time the reviewer pressed `s`.
func TestStagingLeavesTheRestOfTheDiffAloneWhileItIsReadBack(t *testing.T) {
	backend := newFakeBackend(newSession(t, contextDiff+otherFileDiff))
	backend.context = map[FileSide][]string{
		wideSide:            contextFile(),
		{Path: "other.txt"}: {"keep", "new"},
	}
	m := newModel(t, backend)
	settle(t, m, m.Init(), 0)

	offered := len(m.doc.Expands)
	if offered == 0 {
		t.Fatal("nothing offered to read more before staging")
	}
	m.moveTo(m.doc.RowOfFile(fileIndexOf(t, m, "other.txt")))

	// The read-back is held here, on the frame the reviewer sees between the
	// write landing and the files being read again.
	_, cmd := m.Update(keyMsg("s"))
	loaded, ok := cmd().(loadedMsg)
	if !ok {
		t.Fatalf("staging produced %T, want the read-back", cmd())
	}
	m.Update(loaded)

	if got := len(m.doc.Expands); got != offered {
		t.Errorf("offered %d runs while the files were being read, want the %d still on screen", got, offered)
	}
}

// fileIndexOf finds a file in the document by path.
func fileIndexOf(t *testing.T, m *Model, path string) int {
	t.Helper()
	for i, f := range m.doc.Files {
		if f.Entry.Path == path {
			return i
		}
	}
	t.Fatalf("%s is not in the diff", path)
	return -1
}

// otherFileDiff is a second file to stage, so a test can press `s` on one file
// and watch what happens to another.
const otherFileDiff = `diff --git a/other.txt b/other.txt
index 3333333..4444444 100644
--- a/other.txt
+++ b/other.txt
@@ -1,2 +1,2 @@
 keep
-old
+new
`

// The diff moving on takes the copies with it: what was asked for is kept, but
// it is drawn out of the file as it is now.
func TestTheFilesAreReadAgainWhenTheDiffMovesOn(t *testing.T) {
	backend, m := expandModel(t)
	m.moveTo(m.doc.RowOfExpand(down(0), ExpandDown))
	press(t, m, "space")
	reads := len(backend.contextCalls)

	changed := contextFile()
	changed[20] = "line 21 rewritten"
	backend.context = map[FileSide][]string{wideSide: changed}
	press(t, m, "r")

	if len(backend.contextCalls) <= reads {
		t.Error("the diff was read again and the files behind it were not")
	}
	if got := m.revealed[down(0)]; got != 20 {
		t.Errorf("the run reopened to %+v, want the 20 lines still asked for", got)
	}
	if !strings.Contains(strings.Join(lineTexts(m.doc), "\n"), "line 21 rewritten") {
		t.Error("the code on screen is text from the file as it was")
	}
}

// A read that fails costs the offers to read more and nothing else: the review
// is still the review.
func TestAFailedReadIsNotAnError(t *testing.T) {
	backend := newFakeBackend(newSession(t, contextDiff))
	backend.contextErr = fmt.Errorf("no such file")
	m := newModel(t, backend)
	settle(t, m, m.Init(), 0)

	if m.err != nil {
		t.Errorf("err = %v, want a review that carries on", m.err)
	}
	if len(m.doc.Expands) != 0 {
		t.Errorf("offered %+v out of a file that could not be read", m.doc.Expands)
	}
}

// The read is told which session it is for rather than reading one off the
// backend, so a reload landing beside it cannot swap the files out from under
// it — and so it reads the files the reviewer is looking at.
func TestTheReadIsToldWhichSessionItIsFor(t *testing.T) {
	backend, _ := expandModel(t)

	if len(backend.contextCalls) != 1 {
		t.Fatalf("asked for the files %d times, want once", len(backend.contextCalls))
	}
	if got := backend.contextCalls[0]; got != backend.session {
		t.Errorf("read the files for %v, want the session on screen", got)
	}
}

// sixtyLineRepo is a repository whose one changed file has forty-six unchanged
// lines above the change and seven below it — more than a diff prints and more
// than one press reads in.
func sixtyLineRepo(t *testing.T) *gittest.Repo {
	t.Helper()
	sixty := func(fiftieth string) string {
		var b strings.Builder
		for i := 1; i <= 60; i++ {
			if i == 50 && fiftieth != "" {
				b.WriteString(fiftieth + "\n")
				continue
			}
			fmt.Fprintf(&b, "line %d\n", i)
		}
		return b.String()
	}
	repo := gittest.New(t)
	repo.Write("wide.go", sixty(""))
	repo.Commit("initial")
	repo.Write("wide.go", sixty("line fifty"))
	return repo
}

// readModel is realModel with the read of the files behind the diff done, which
// is what Init starts on a real run.
func readModel(t *testing.T, repo *gittest.Repo) *Model {
	t.Helper()
	m := realModel(t, repo)
	settle(t, m, m.Init(), 0)
	return m
}

// End to end, over git rather than a fixture: the seven lines past the last hunk
// are known only because peel read the file itself, and pressing space on the
// row that says so puts them on screen.
func TestSpaceReadsTheRealFileBehindTheDiff(t *testing.T) {
	m := readModel(t, sixtyLineRepo(t))

	if got := body(m); strings.Contains(got, "line 60") {
		t.Fatalf("the diff already showed the end of the file:\n%s", got)
	}
	press(t, m, "G")
	if got := m.doc.Rows[m.cursor].Kind; got != RowExpand {
		t.Fatalf("the last row is a %v, want the offer to read on to the end of the file", got)
	}
	if got := body(m); !strings.Contains(got, "▾ 7 lines hidden") {
		t.Fatalf("nothing said how much the diff left out:\n%s", got)
	}

	press(t, m, "space")

	got := body(m)
	if !strings.Contains(got, "line 60") {
		t.Errorf("the end of the file was not read in:\n%s", got)
	}
	if strings.Contains(got, "7 lines hidden") {
		t.Errorf("a run read to its end still offers more:\n%s", got)
	}
	// What is left is the other run, above the hunk, untouched by the press.
	if len(m.doc.Expands) != 1 || m.doc.Expands[0].Hidden != 46 {
		t.Errorf("runs left = %+v, want the 46 lines above the hunk alone", m.doc.Expands)
	}
}

// The other end of the same file: the forty-six lines above the hunk arrive
// twenty at a time, and the row that offers them counts down.
func TestSpaceReadsTowardsTheTopOfTheRealFile(t *testing.T) {
	m := readModel(t, sixtyLineRepo(t))

	press(t, m, "g")
	for m.doc.Rows[m.cursor].Kind != RowExpand {
		press(t, m, "down")
	}
	press(t, m, "space")

	got := body(m)
	if !strings.Contains(got, "line 27") {
		t.Errorf("twenty lines above the hunk were not read in:\n%s", got)
	}
	if strings.Contains(got, "line 26") {
		t.Errorf("more than a step arrived at once:\n%s", got)
	}
	if !strings.Contains(got, "▴ 26 lines hidden") {
		t.Errorf("the row did not count down to what is left:\n%s", got)
	}
}

// The arrows stop on the offer to read more, because that is where space is
// pressed. j and k do not: it sits against a hunk they already stop on, and
// stopping again would put two extra presses between every pair of hunks.
func TestJumpingHunkToHunkStepsPastTheOfferToReadMore(t *testing.T) {
	doc := Build(newSession(t, contextDiff), nil, nil, LayoutUnified, WithExpansion(expansionOf(nil)))

	for i, row := range doc.Rows {
		if row.Kind != RowExpand {
			continue
		}
		if !doc.IsStop(i) {
			t.Errorf("row %d does not take the cursor, so space cannot be pressed on it", i)
		}
		if doc.IsMark(i) {
			t.Errorf("row %d is a j/k stop, putting a press between two hunks", i)
		}
	}

	_, m := expandModel(t)
	press(t, m, "j")
	if got := m.doc.Rows[m.cursor].Kind; got != RowHunk {
		t.Errorf("j landed on a %v, want the first hunk", got)
	}
	press(t, m, "down")
	if got := m.doc.Rows[m.cursor].Kind; got != RowExpand {
		t.Errorf("↓ landed on a %v, want the offer under the hunk header", got)
	}
}

// The brackets do stop on it. Ten lines that ran past a run of hidden code would
// put the reviewer below a gap with nothing on screen saying they had crossed
// one, so the jump ends on the row that says so — and the next press carries on
// from there.
func TestTenLinesStopsAtTheOfferToReadMore(t *testing.T) {
	_, m := expandModel(t)

	// Down the first hunk's body, which is eight lines with the offer under it.
	press(t, m, "j", "down", "down")
	if got := m.doc.Rows[m.cursor].Kind; got != RowLine {
		t.Fatalf("the cursor is on a %v, want the first line of the hunk body", got)
	}

	press(t, m, "]")
	if got := m.doc.Rows[m.cursor].Kind; got != RowExpand {
		t.Fatalf("] landed on a %v, want the ▾ row under the hunk", got)
	}
	if m.cursor != m.doc.Expands[1].Row {
		t.Errorf("] landed on row %d, want the run below the first hunk at %d",
			m.cursor, m.doc.Expands[1].Row)
	}

	press(t, m, "]")
	if got := m.doc.Rows[m.cursor].Kind; got == RowExpand && m.cursor == m.doc.Expands[1].Row {
		t.Error("a second ] stayed on the row it had stopped on")
	}

	press(t, m, "[")
	if m.cursor != m.doc.Expands[1].Row {
		t.Errorf("[ back left the cursor at row %d, want the ▾ row at %d it came from",
			m.cursor, m.doc.Expands[1].Row)
	}
}

// A hunk that only adds lines covers none on the old side, and one that only
// removes them covers none on the new — so the numbers git prints are one short
// of where the run either side of it starts, and the two sides have drifted by
// exactly what the hunk added or took away.
func TestARunBesideAHunkThatOnlyAddsOrOnlyRemoves(t *testing.T) {
	ten := func() []string {
		out := make([]string, 0, 10)
		for i := 1; i <= 10; i++ {
			out = append(out, fmt.Sprintf("line %d", i))
		}
		return out
	}
	side := FileSide{Path: "ten.go"}
	id := func(h git.Hunk) git.HunkID {
		return git.HunkID{Path: side.Path, OldStart: h.OldStart, OldCount: h.OldCount, NewStart: h.NewStart, NewCount: h.NewCount}
	}
	// The code read in below the only hunk, named the way the document names it.
	under := func(hunks []git.Hunk) ExpandKey {
		return ExpandKey{FileSide: side, Hunk: id(hunks[0])}
	}
	x := func(revealed map[ExpandKey]int) Expansion {
		return Expansion{Files: map[FileSide][]string{side: ten()}, Revealed: revealed}
	}

	// Four lines inserted at the top of a file that had six: everything from the
	// fifth line on is the old file, four numbers further down.
	added := []git.Hunk{{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 4}}
	g := gapsOf(side, added, id, x(map[ExpandKey]int{under(added): 2}))
	if got := g.hidden(1); got != 4 {
		t.Errorf("the run under the insertion hides %d, want the 6 lines past it less the 2 read in", got)
	}
	read := g.top(1)
	if len(read) != 2 || read[0].NewLine != 5 || read[0].OldLine != 1 {
		t.Errorf("read = %+v, want new line 5 as old line 1", read)
	}

	// Three lines cut from the middle of a file that had thirteen: the code after
	// them is three numbers higher in the file it left.
	removed := []git.Hunk{{OldStart: 5, OldCount: 3, NewStart: 4, NewCount: 0}}
	g = gapsOf(side, removed, id, x(map[ExpandKey]int{under(removed): 2}))
	if got := g.runs[0]; got.first != 1 || got.last != 4 {
		t.Errorf("the run above the deletion is %+v, want lines 1 to 4", got)
	}
	if got := g.runs[1]; got.first != 5 || got.last != 10 {
		t.Errorf("the run below the deletion is %+v, want lines 5 to 10", got)
	}
	read = g.top(1)
	if len(read) != 2 || read[0].NewLine != 5 || read[0].OldLine != 8 {
		t.Errorf("read = %+v, want new line 5 as old line 8", read)
	}
}

// The row under the cursor is drawn as the cursor's, like every other row: it is
// what space acts on, so it has to be visibly where the cursor is.
func TestTheOfferUnderTheCursorIsMarkedAsTheCursorRow(t *testing.T) {
	doc := Build(newSession(t, contextDiff), nil, nil, LayoutUnified, WithExpansion(expansionOf(nil)))
	r := plainRenderer(70)

	row := doc.Expands[0].Row
	plain := r.Row(doc, row, RowState{})
	under := r.Row(doc, row, RowState{Cursor: true})
	if plain == under {
		t.Errorf("the row reads the same with and without the cursor: %q", plain)
	}
	if !strings.Contains(under, "1 line hidden") {
		t.Errorf("the cursor row lost what it says: %q", under)
	}
}

// Two files opened at once keep their runs apart: what was read into one is not
// read into the other at the same position.
func TestEachFileOpensItsOwnRuns(t *testing.T) {
	entries := append(parseFiles(t, contextDiff), parseFiles(t, strings.ReplaceAll(contextDiff, "wide.go", "other.go"))...)
	other := FileSide{Path: "other.go"}
	doc := Build(sessionOf(entries), nil, nil, LayoutUnified, WithExpansion(Expansion{
		Files:    map[FileSide][]string{wideSide: contextFile(), other: contextFile()},
		Revealed: map[ExpandKey]int{hunkKey(strings.ReplaceAll(contextDiff, "wide.go", "other.go"), other, 0, false): 20},
	}))

	byPath := map[string]int{}
	for _, ref := range doc.Hunks {
		byPath[ref.Path] += len(ref.Hunk.Lines)
	}
	if byPath["wide.go"] != 16 {
		t.Errorf("wide.go draws %d lines, want only the 16 its two hunks hold", byPath["wide.go"])
	}
	if byPath["other.go"] != 36 {
		t.Errorf("other.go draws %d lines, want its 16 and the 20 read into it", byPath["other.go"])
	}
}

// A folded file shows nothing at all, and an offer to read more of it would be
// the one thing left on screen under a header that says the file is put away.
func TestAFoldedFileOffersNothing(t *testing.T) {
	doc := Build(newSession(t, contextDiff), nil, map[string]bool{"wide.go": true}, LayoutUnified,
		WithExpansion(expansionOf(nil)))

	if len(doc.Expands) != 0 {
		t.Errorf("a folded file offered %+v", doc.Expands)
	}
}

// oneHunkDiff is contextDiff with its first hunk taken out, so a reload can add
// a change above one the reviewer has already opened code around.
var oneHunkDiff = strings.Replace(contextDiff, `@@ -2,7 +2,7 @@
 line 2
 line 3
 line 4
-line 5
+line five
 line 6
 line 7
 line 8
`, "", 1)

// A run is named by the hunks around it, not by its position among them. A
// change arriving above one the reviewer has opened code around would otherwise
// renumber every run below it, and what had been read in at the bottom of the
// file would reappear around whichever run had taken its number.
func TestAChangeAboveDoesNotMoveWhatWasReadInBelow(t *testing.T) {
	backend := newFakeBackend(newSession(t, oneHunkDiff))
	backend.context = map[FileSide][]string{wideSide: contextFile()}
	m := newModel(t, backend)
	settle(t, m, m.Init(), 0)

	// Read twenty lines in above the hunk, so it starts at line 27.
	for i, r := range m.doc.Rows {
		if r.Kind == RowExpand && m.doc.Expands[r.Expand].Dir == ExpandUp {
			m.moveTo(i)
			break
		}
	}
	press(t, m, "space")
	if got := lineTexts(m.doc)[0]; got != "line 27" {
		t.Fatalf("the diff starts at %q, want the twenty lines read in above the hunk", got)
	}

	backend.nextSession = newSession(t, contextDiff)
	press(t, m, "r")

	// The hunk the code was read in against is untouched by a change forty lines
	// above it, so what was read in is still there.
	if got := len(m.doc.Hunks[1].Hunk.Lines); got != 28 {
		t.Errorf("the hunk draws %d lines, want its own 8 and the 20 read in", got)
	}
	if got := m.doc.Hunks[1].Hunk.Lines[0].Text; got != "line 27" {
		t.Errorf("the hunk now starts at %q, want line 27", got)
	}
	// And the new hunk at the top opened nothing of its own.
	if got := len(m.doc.Hunks[0].Hunk.Lines); got != 8 {
		t.Errorf("the new hunk draws %d lines, want only its own 8", got)
	}
}

// When the hunk a run was opened against is itself rewritten, the run it named
// is gone. It reads as closed rather than as some other run of the same number:
// nothing wrong is drawn, there is simply less on screen than before.
func TestCodeReadInAroundARewrittenHunkClosesAgain(t *testing.T) {
	backend := newFakeBackend(newSession(t, contextDiff))
	backend.context = map[FileSide][]string{wideSide: contextFile()}
	m := newModel(t, backend)
	settle(t, m, m.Init(), 0)

	for i, r := range m.doc.Rows {
		if r.Kind == RowExpand && m.doc.Expands[r.Expand].ExpandKey == down(1) {
			m.moveTo(i)
			break
		}
	}
	press(t, m, "space")
	if got := len(m.doc.Hunks[1].Hunk.Lines); got != 15 {
		t.Fatalf("the last hunk draws %d lines, want its own 8 and the 7 to the end of the file", got)
	}

	// The same change, now two lines wider: a different hunk by every number git
	// prints on it.
	backend.nextSession = newSession(t, strings.Replace(contextDiff,
		"@@ -47,7 +47,7 @@\n line 47", "@@ -46,8 +46,8 @@\n line 46\n line 47", 1))
	press(t, m, "r")

	if got := len(m.doc.Hunks[1].Hunk.Lines); got != 9 {
		t.Errorf("the rewritten hunk draws %d lines, want only its own", got)
	}
	if len(m.doc.Expands) == 0 {
		t.Error("nothing offers to read the end of the file back in")
	}
}
