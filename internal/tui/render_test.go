package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// plainRenderer renders without colour or syntax highlighting, so rows can be
// compared as ordinary strings.
func plainRenderer(width int) *Renderer {
	r := NewRenderer(Theme{}, nil)
	r.SetWidth(width)
	return r
}

func TestRenderUnifiedLineAlignsBothLineNumbers(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)
	r := plainRenderer(40)

	rows := map[string]string{}
	for i, row := range doc.Rows {
		if row.Kind != RowLine || row.Hunk != 0 {
			continue
		}
		line := doc.Hunks[0].Hunk.Lines[row.Left]
		rows[line.Render()] = strings.TrimRight(r.Row(doc, i, RowState{}), " ")
	}

	// One marker column, then a four-wide old-line and new-line column, then the
	// origin character. A line missing from one side leaves that column blank.
	cases := map[string]string{
		" package alpha":               "    1   1  package alpha",
		"-func One() int { return 1 }": "    3     -func One() int { return 1 }",
		"+func One() int { return 2 }": "        3 +func One() int { return 2 }",
	}
	for render, want := range cases {
		got, ok := rows[render]
		if !ok {
			t.Fatalf("no row rendered for %q; have %v", render, keys(rows))
		}
		if got != want {
			t.Errorf("line %q\n got %q\nwant %q", render, got, want)
		}
	}
}

func TestRenderSplitLineShowsBothSidesAcrossADivider(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutSplit)
	r := plainRenderer(80)

	var found string
	for i, row := range doc.Rows {
		if row.Kind != RowLine || row.Left < 0 || row.Right < 0 {
			continue
		}
		lines := doc.Hunks[row.Hunk].Hunk.Lines
		if lines[row.Left].Kind.Origin() != '-' {
			continue
		}
		found = r.Row(doc, i, RowState{})
		break
	}
	if found == "" {
		t.Fatal("no replaced line was rendered side by side")
	}
	if !strings.Contains(found, "│") {
		t.Errorf("split row has no divider: %q", found)
	}
	if !strings.Contains(found, "-func One() int { return 1 }") {
		t.Errorf("split row is missing the old text: %q", found)
	}
	if !strings.Contains(found, "+func One() int { return 2 }") {
		t.Errorf("split row is missing the new text: %q", found)
	}
}

func TestRenderEveryRowIsExactlyOneLineOfTheGivenWidth(t *testing.T) {
	comments := []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 4, Side: store.SideNew, Body: "short\nand a second line", Author: store.AuthorUser},
		{ID: "c2", File: "beta.txt", Body: strings.Repeat("very long comment ", 12), Author: store.AuthorAgent},
	}
	for _, layout := range []Layout{LayoutUnified, LayoutSplit} {
		for _, width := range []int{24, 40, 120} {
			doc := Build(newSession(t, twoFileDiff), comments, nil, layout)
			r := plainRenderer(width)
			for i, row := range doc.Rows {
				got := r.Row(doc, i, RowState{Cursor: i == 3})
				if strings.Contains(got, "\n") {
					t.Fatalf("%v width %d row %d contains a newline", layout, width, i)
				}
				if w := ansi.StringWidth(got); w != width {
					t.Fatalf("%v width %d row %d (%v) rendered %d cells: %q", layout, width, i, row.Kind, w, got)
				}
			}
		}
	}
}

func TestRenderCursorGetsAMarker(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)
	r := plainRenderer(60)
	row := doc.RowOfHunk(0)

	if got := r.Row(doc, row, RowState{}); strings.HasPrefix(got, "▌") {
		t.Errorf("uncursored row starts with the marker: %q", got)
	}
	if got := r.Row(doc, row, RowState{Cursor: true}); !strings.HasPrefix(got, "▌") {
		t.Errorf("cursored row = %q, want a leading marker", got)
	}
}

func TestRenderHunkHeaderNamesWhereTheChangeLives(t *testing.T) {
	doc := Build(partStagedSession(t), nil, nil, LayoutUnified, WithSideFolds(map[string]bool{"alpha.go": false}))
	r := plainRenderer(80)

	first := r.Row(doc, doc.RowOfHunk(0), RowState{})
	second := r.Row(doc, doc.RowOfHunk(1), RowState{})

	if !strings.Contains(first, "index") {
		t.Errorf("staged hunk header = %q, want it marked index", first)
	}
	if !strings.Contains(second, "worktree") {
		t.Errorf("unstaged hunk header = %q, want it marked worktree", second)
	}
	if strings.Contains(first, "@@") {
		t.Errorf("hunk header = %q, want the @@ range dropped", first)
	}
	if !strings.Contains(first, "package alpha") {
		t.Errorf("hunk header = %q, want git's section context", first)
	}
}

func TestRenderHunkHeaderWithoutSectionStillSeparates(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)
	r := plainRenderer(80)

	got := r.Row(doc, doc.RowOfHunk(1), RowState{})
	if !strings.Contains(got, "⋯") {
		t.Errorf("sectionless hunk header = %q, want a separator mark", got)
	}
}

func TestRenderFileHeaderShowsStateAndCounts(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)
	r := plainRenderer(80)

	got := r.Row(doc, doc.RowOfFile(0), RowState{})
	for _, want := range []string{"alpha.go", "+2 -1", "modified", "▾"} {
		if !strings.Contains(got, want) {
			t.Errorf("file header = %q, want it to contain %q", got, want)
		}
	}

	collapsed := Build(newSession(t, twoFileDiff), nil, map[string]bool{"alpha.go": true}, LayoutUnified)
	if got := r.Row(collapsed, collapsed.RowOfFile(0), RowState{}); !strings.Contains(got, "▸") {
		t.Errorf("collapsed file header = %q, want the closed arrow", got)
	}
}

func TestRenderCommentShowsAuthorAndResolvedState(t *testing.T) {
	comments := []store.Comment{
		{ID: "open", File: "alpha.go", Body: "needs a test", Author: store.AuthorAgent},
		{ID: "done", File: "beta.txt", Body: "fixed", Author: store.AuthorUser, Resolved: true},
	}
	doc := Build(newSession(t, twoFileDiff), comments, nil, LayoutUnified)
	r := plainRenderer(80)

	open := r.Row(doc, doc.RowOfComment("open"), RowState{})
	if !strings.Contains(open, "agent: needs a test") {
		t.Errorf("comment row = %q", open)
	}
	done := r.Row(doc, doc.RowOfComment("done"), RowState{})
	if !strings.Contains(done, "✓ user: fixed") {
		t.Errorf("resolved comment row = %q, want a tick", done)
	}
}

// The cursor rests on diff lines now, so the marker has to appear on a line row
// without shifting the row it marks.
func TestRenderCursorMarksADiffLineWithoutShiftingIt(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)
	r := plainRenderer(60)

	row := -1
	for i, x := range doc.Rows {
		if x.Kind == RowLine {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("no diff line row")
	}

	plain := r.Row(doc, row, RowState{})
	marked := r.Row(doc, row, RowState{Cursor: true})
	if strings.Contains(plain, "▌") {
		t.Errorf("a row the cursor is not on shows the marker: %q", plain)
	}
	if !strings.Contains(marked, "▌") {
		t.Errorf("the cursor row = %q, want the marker", marked)
	}
	if ansi.StringWidth(plain) != ansi.StringWidth(marked) {
		t.Errorf("the marker changed the row width: %d without, %d with",
			ansi.StringWidth(plain), ansi.StringWidth(marked))
	}
	if strings.TrimPrefix(marked, "▌") != strings.TrimPrefix(plain, " ") {
		t.Errorf("the marker shifted the line body:\n%q\n%q", marked, plain)
	}
}

// A marker only at the far left of a split row says which row the cursor is on
// but leaves the other pane unmarked, so each side gets one.
func TestRenderCursorMarksBothSidesOfASplitRow(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutSplit)
	r := plainRenderer(60)

	row := -1
	for i, x := range doc.Rows {
		if x.Kind == RowLine {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("no diff line row")
	}

	plain := r.Row(doc, row, RowState{})
	marked := r.Row(doc, row, RowState{Cursor: true})
	if strings.Contains(plain, "▌") {
		t.Errorf("a split row the cursor is not on shows a marker: %q", plain)
	}
	if !strings.HasPrefix(marked, "▌") {
		t.Errorf("the cursor row = %q, want the old side marked at the left edge", marked)
	}
	if !strings.Contains(marked, splitRule+"▌") {
		t.Errorf("the cursor row = %q, want the new side marked across the divider", marked)
	}
	if got := strings.ReplaceAll(marked, "▌", " "); got != plain {
		t.Errorf("the markers shifted the split row:\n%q\n%q", got, plain)
	}
}

func TestRenderOutOfRangeRowIsEmpty(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)
	r := plainRenderer(40)

	if got := r.Row(doc, -1, RowState{}); got != "" {
		t.Errorf("row -1 = %q, want empty", got)
	}
	if got := r.Row(doc, doc.Len(), RowState{}); got != "" {
		t.Errorf("row past the end = %q, want empty", got)
	}
}

// The fills are set by hand rather than taken from DefaultTheme, which renders
// bare when the test binary's output is not a terminal.
const (
	testAddedFill   = "\x1b[42m"
	testRemovedFill = "\x1b[41m"
)

func TestRenderBandsChangedLinesInTheirColour(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)
	r := plainRenderer(40)
	r.addedFill, r.removedFill = testAddedFill, testRemovedFill

	rows := map[string]string{}
	for i, row := range doc.Rows {
		if row.Kind != RowLine || row.Hunk != 0 {
			continue
		}
		rows[doc.Hunks[0].Hunk.Lines[row.Left].Render()] = r.Row(doc, i, RowState{})
	}

	for render, want := range map[string]string{
		"+func One() int { return 2 }": testAddedFill,
		"-func One() int { return 1 }": testRemovedFill,
		" package alpha":               "",
	} {
		got, ok := rows[render]
		if !ok {
			t.Fatalf("no row rendered for %q; have %v", render, keys(rows))
		}
		if want == "" {
			if strings.Contains(got, "\x1b") {
				t.Errorf("context line %q was banded: %q", render, got)
			}
			continue
		}
		if !strings.HasPrefix(got, " "+want) {
			t.Errorf("line %q = %q, want the band to open the row after the marker", render, got)
		}
		if !strings.HasSuffix(got, resetSequence) {
			t.Errorf("line %q = %q, want the band to close at the end of the row", render, got)
		}
		if !strings.Contains(got, render) {
			t.Errorf("line %q = %q, want the band to leave the text alone", render, got)
		}
	}
}

func TestRenderBandsBothSidesOfASplitRow(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutSplit)
	r := plainRenderer(80)
	r.addedFill, r.removedFill = testAddedFill, testRemovedFill

	var got string
	for i, row := range doc.Rows {
		if row.Kind == RowLine && row.Left >= 0 && row.Right >= 0 &&
			doc.Hunks[row.Hunk].Hunk.Lines[row.Left].Kind == git.LineRemoved {
			got = r.Row(doc, i, RowState{})
			break
		}
	}
	if got == "" {
		t.Fatal("no replaced line was rendered side by side")
	}
	red, green := strings.Index(got, testRemovedFill), strings.Index(got, testAddedFill)
	if red < 0 || green < 0 {
		t.Fatalf("split row = %q, want both sides banded", got)
	}
	if red > green {
		t.Errorf("split row = %q, want the removed side banded left of the added one", got)
	}
}

func TestRenderBandSurvivesSyntaxHighlighting(t *testing.T) {
	h := NewHighlighter()
	if h == nil {
		t.Skip("no formatter available")
	}
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)
	r := NewRenderer(Theme{}, h)
	r.SetWidth(60)
	r.addedFill = testAddedFill

	var got string
	for i, row := range doc.Rows {
		if row.Kind == RowLine && doc.Hunks[row.Hunk].Hunk.Lines[row.Left].Kind == git.LineAdded {
			got = r.Row(doc, i, RowState{})
			break
		}
	}
	if !strings.Contains(got, "\x1b[38") {
		t.Fatalf("added row = %q, want it syntax highlighted", got)
	}
	spans := strings.Split(strings.TrimSuffix(got, resetSequence), resetSequence)
	for _, span := range spans[1:] {
		if !strings.HasPrefix(span, testAddedFill) {
			t.Errorf("added row = %q, want the band armed again after every reset", got)
			break
		}
	}
}

func TestFillLeavesAnUnbandedLineAlone(t *testing.T) {
	if got := fill("", "plain"); got != "plain" {
		t.Errorf("fill without a colour changed the line: %q", got)
	}
	if got := fillSequence(lipgloss.NewStyle()); got != "" {
		t.Errorf("a style with no background has the sequence %q", got)
	}
}

func TestFitTruncatesAndPads(t *testing.T) {
	if got := fit("abc", 6); got != "abc   " {
		t.Errorf("fit padded to %q", got)
	}
	if got := fit("abcdefgh", 4); ansi.StringWidth(got) != 4 || !strings.HasSuffix(got, "…") {
		t.Errorf("fit truncated to %q", got)
	}
	if got := fit("abc", 0); got != "" {
		t.Errorf("fit to width 0 = %q", got)
	}
}

func TestShortenTrimsFromTheLeftSoTheFilenameSurvives(t *testing.T) {
	if got := shorten("internal/tui/model.go", 12); got != "…tui/model.go" && ansi.StringWidth(got) > 12 {
		t.Errorf("shorten = %q (width %d)", got, ansi.StringWidth(got))
	}
	if got := shorten("model.go", 20); got != "model.go" {
		t.Errorf("short path was changed: %q", got)
	}
	if got := shorten("model.go", 1); got != "…" {
		t.Errorf("shorten to 1 = %q", got)
	}
	if got := shorten("model.go", 0); got != "" {
		t.Errorf("shorten to 0 = %q", got)
	}
}

func TestHighlighterLeavesTextAloneForUnknownLanguages(t *testing.T) {
	h := NewHighlighter()
	if h == nil {
		t.Skip("no formatter available")
	}
	if got := h.Line("notes.unknownext", "hello"); got != "hello" {
		t.Errorf("unknown language changed the text: %q", got)
	}
	if got := h.Line("main.go", "   "); got != "   " {
		t.Errorf("blank text was altered: %q", got)
	}
	coloured := h.Line("main.go", "func main() {}")
	if !strings.Contains(coloured, "func") {
		t.Errorf("highlighted line lost its text: %q", coloured)
	}
	if strings.Contains(coloured, "\n") {
		t.Errorf("highlighted line gained a newline: %q", coloured)
	}
}

func TestNilHighlighterIsInactive(t *testing.T) {
	var h *Highlighter
	if h.Active() {
		t.Error("a nil highlighter should be inactive")
	}
	if got := h.Line("main.go", "x := 1"); got != "x := 1" {
		t.Errorf("nil highlighter changed the text: %q", got)
	}
}

func TestExtensionOfCachesByExtensionOrBaseName(t *testing.T) {
	cases := map[string]string{
		"internal/tui/model.go": ".go",
		"Makefile":              "Makefile",
		"a/b/Dockerfile":        "Dockerfile",
		".gitignore":            ".gitignore",
	}
	for path, want := range cases {
		if got := extensionOf(path); got != want {
			t.Errorf("extensionOf(%q) = %q, want %q", path, got, want)
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// lineRow finds the row displaying the hunk line with the given origin.
func lineRow(t *testing.T, d Document, origin byte) int {
	t.Helper()
	for i, row := range d.Rows {
		if row.Kind != RowLine {
			continue
		}
		for _, side := range []int{row.Left, row.Right} {
			lines := d.Hunks[row.Hunk].Hunk.Lines
			if side >= 0 && side < len(lines) && lines[side].Kind.Origin() == origin {
				return i
			}
		}
	}
	t.Fatalf("no %q line in the document", origin)
	return -1
}

func TestRenderScrolledCodeSlidesUnderAPinnedGutter(t *testing.T) {
	doc := Build(newSession(t, longLineDiff), nil, nil, LayoutUnified)
	r := plainRenderer(60)
	row := lineRow(t, doc, '+')

	at0 := r.Row(doc, row, RowState{})
	r.SetOffset(20)

	// One marker column, both line-number columns, the space before the code and
	// the origin character. None of it moves, so the row still says which line it
	// is and whether the line was added.
	head := 1 + 2*lineNumWidth + 1 + 1
	want := at0[:head] + fit(wideLine[20:], 60-head)
	if got := r.Row(doc, row, RowState{}); got != want {
		t.Errorf("scrolled row\n got %q\nwant %q", got, want)
	}
}

func TestRenderScrolledCodeSlidesBothSidesOfASplitRow(t *testing.T) {
	doc := Build(newSession(t, longLineDiff), nil, nil, LayoutSplit)
	r := plainRenderer(80)
	row := lineRow(t, doc, '+')
	r.SetOffset(20)

	got := r.Row(doc, row, RowState{})
	if !strings.Contains(got, splitDivider) {
		t.Fatalf("split row lost its divider: %q", got)
	}
	if strings.Contains(got, wideLine[:20]) {
		t.Errorf("split row still shows the columns scrolled past: %q", got)
	}
	if !strings.Contains(got, wideLine[20:40]) {
		t.Errorf("split row does not show the code scrolled to: %q", got)
	}
	// The old side is far shorter than the offset, so it scrolls away entirely
	// rather than staying put beside the new side.
	if strings.Contains(got, "var short") {
		t.Errorf("the old side did not scroll with the new one: %q", got)
	}
}

// TestShiftKeepsTheColourOpenedLeftOfTheCut covers what makes it safe to
// highlight a whole line and then cut it: a cut landing inside a token would
// otherwise drop the escape that coloured it and leave the tail bare.
func TestShiftKeepsTheColourOpenedLeftOfTheCut(t *testing.T) {
	const want = "\x1b[31mtext\x1b[0m"
	if got := shift("\x1b[31mred text\x1b[0m", 4); got != want {
		t.Errorf("shift = %q, want %q", got, want)
	}
}

func TestCodeColumnsLeavesLessRoomSideBySide(t *testing.T) {
	r := plainRenderer(80)
	unified, split := r.CodeColumns(LayoutUnified), r.CodeColumns(LayoutSplit)
	if split >= unified {
		t.Errorf("split fits %d columns of code and unified %d, want split to fit fewer", split, unified)
	}
	if split < 1 {
		t.Errorf("split fits %d columns, want at least one", split)
	}
}

// The two halves of a part-staged file are the same run of green lines one after
// the other, so where one ends and the other begins is drawn as a break across
// the pane rather than said in a word at the end of a line.
func TestRenderSideHeadingBreaksTheDiffInTwo(t *testing.T) {
	doc := Build(sessionOf([]git.FileEntry{partStagedFile(t, "notes.txt")}), nil, nil, LayoutUnified)
	r := plainRenderer(70)

	staged := r.Row(doc, doc.Sides[0].Row, RowState{})
	work := r.Row(doc, doc.Sides[1].Row, RowState{})

	if !strings.Contains(staged, "staged") || !strings.Contains(staged, "index") {
		t.Errorf("staged heading = %q, want it to say what is in the index", staged)
	}
	if !strings.Contains(staged, "+4 -0") {
		t.Errorf("staged heading = %q, want the counts of the half it heads", staged)
	}
	if !strings.Contains(staged, "▸") {
		t.Errorf("staged heading = %q, want a folded arrow — it opens hidden", staged)
	}
	if !strings.Contains(work, "unstaged") || strings.Contains(work, "▸") || strings.Contains(work, "▾") {
		t.Errorf("working-tree heading = %q, want it named and unfoldable", work)
	}
	for _, row := range []string{staged, work} {
		if !strings.Contains(row, "──") {
			t.Errorf("heading = %q, want a rule across the pane", row)
		}
		if got := ansi.StringWidth(row); got != 70 {
			t.Errorf("heading is %d columns wide, want the pane's 70", got)
		}
	}
}

// A folded part-staged file shows nothing but its header, so the header is where
// the split has to be readable: four lines staged and one new is not one change
// of five lines.
func TestRenderFileHeaderSplitsAPartStagedCount(t *testing.T) {
	doc := Build(sessionOf([]git.FileEntry{partStagedFile(t, "notes.txt")}), nil, nil, LayoutUnified)
	r := plainRenderer(70)

	got := r.Row(doc, doc.RowOfFile(0), RowState{})
	if !strings.Contains(got, "index +4 -0") || !strings.Contains(got, "worktree +1 -0") {
		t.Errorf("file header = %q, want each half counted", got)
	}
}

// An ordinary file has one half, and a word naming it on every hunk says
// nothing.
func TestRenderHunkHeaderOfAnUnstagedFileNamesNoHalf(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)
	r := plainRenderer(70)

	if got := r.Row(doc, doc.RowOfHunk(0), RowState{}); strings.Contains(got, "worktree") {
		t.Errorf("hunk header = %q, want no half named where there is only one", got)
	}
}
