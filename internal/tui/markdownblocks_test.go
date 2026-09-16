package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

const tableDiff = "diff --git a/table.md b/table.md\n" +
	"index 1111111..2222222 100644\n" +
	"--- a/table.md\n" +
	"+++ b/table.md\n" +
	"@@ -1,6 +1,7 @@\n" +
	" | ledger | holds | rec? |\n" +
	"-|---|---|---|\n" +
	"+|---|---:|:---:|\n" +
	" | PRIMARY | the real books | yes |\n" +
	"-| ELIMINATION | mirror |\n" +
	"+| ELIMINATION | mirror entries | no |\n" +
	"+| BUDGET | a \\| b | no |\n" +
	" \n" +
	" done | maybe\n"

const codeBlockDiff = "diff --git a/fence.md b/fence.md\n" +
	"index 1111111..2222222 100644\n" +
	"--- a/fence.md\n" +
	"+++ b/fence.md\n" +
	"@@ -1,8 +1,8 @@\n" +
	" read this:\n" +
	" \n" +
	"-```sql\n" +
	"+```json\n" +
	" {\"k\": \"**not** bold\"}\n" +
	" | a | b |\n" +
	" |---|---|\n" +
	" ```\n" +
	" \n" +
	" **bold** again\n"

func markdownRows(t *testing.T, diff, path string, width int) []string {
	t.Helper()
	doc := Build(newSession(t, diff), nil, nil, LayoutUnified)
	r := NewRenderer(DefaultTheme(), NewHighlighter())
	r.SetWidth(width)

	var out []string
	for i, row := range doc.Rows {
		if row.Kind != RowLine || doc.Hunks[row.Hunk].Path != path {
			continue
		}
		out = append(out, r.Row(doc, i, RowState{}))
	}
	return out
}

func rowText(row string) string {
	plain := []rune(strings.TrimRight(ansi.Strip(row), " "))
	if len(plain) < lineNumWidth+3 {
		return ""
	}
	return string(plain[lineNumWidth+3:])
}

func TestAMarkdownTableIsDrawnWithRules(t *testing.T) {
	rows := markdownRows(t, tableDiff, "table.md", 200)
	want := []string{
		"│ ledger      │          holds │ rec? │",
		"├─────────────┼────────────────┼──────┤",
		"├─────────────┼────────────────┼──────┤",
		"│ PRIMARY     │ the real books │ yes  │",
		"│ ELIMINATION │         mirror │      │",
		"│ ELIMINATION │ mirror entries │  no  │",
		"│ BUDGET      │         a \\| b │  no  │",
		"",
		"done | maybe",
	}
	if len(rows) != len(want) {
		t.Fatalf("table.md drew %d rows, want %d: %q", len(rows), len(want), rows)
	}
	for i, w := range want {
		if got := rowText(rows[i]); got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}
}

func TestATableColumnIsAsWideAsEitherSideNeeds(t *testing.T) {
	rows := markdownRows(t, tableDiff, "table.md", 200)
	widths := map[int]int{}
	for i, row := range rows {
		if text := rowText(row); strings.HasPrefix(text, "│") || strings.HasPrefix(text, "├") {
			widths[ansi.StringWidth(text)] = i
		}
	}
	if len(widths) != 1 {
		t.Errorf("the drawn table has rows of %d different widths, want one: %v", len(widths), widths)
	}
}

func TestTableAlignmentFollowsTheDashRow(t *testing.T) {
	rows := markdownRows(t, tableDiff, "table.md", 200)
	for _, row := range rows {
		text := rowText(row)
		if !strings.HasPrefix(text, "│ BUDGET") {
			continue
		}
		if !strings.Contains(text, "│         a \\| b │") {
			t.Errorf("right-aligned column = %q, want its cell pushed to the right", text)
		}
		if !strings.Contains(text, "│  no  │") {
			t.Errorf("centred column = %q, want the cell padded on both sides", text)
		}
		return
	}
	t.Fatal("the BUDGET row was not drawn")
}

func TestScrollingReachesTheEndOfADrawnTable(t *testing.T) {
	doc := Build(newSession(t, tableDiff), nil, nil, LayoutUnified)
	rows := markdownRows(t, tableDiff, "table.md", 200)

	widest := 0
	for _, row := range rows {
		widest = max(widest, ansi.StringWidth(rowText(row)))
	}
	if doc.CodeWidth < widest {
		t.Errorf("CodeWidth = %d, want at least the %d columns the drawn table takes", doc.CodeWidth, widest)
	}
}

func TestARunOfPipesWithoutADashRowIsLeftAlone(t *testing.T) {
	const prose = "diff --git a/prose.md b/prose.md\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/prose.md\n" +
		"+++ b/prose.md\n" +
		"@@ -1,2 +1,2 @@\n" +
		"-pipe a | b here\n" +
		"+pipe a | b there\n"

	for _, row := range markdownRows(t, prose, "prose.md", 80) {
		if text := rowText(row); !strings.HasSuffix(text, "pipe a | b here") && !strings.HasSuffix(text, "pipe a | b there") {
			t.Errorf("prose with a pipe in it was redrawn as %q", text)
		}
	}
}

func TestATableInsideACodeBlockIsLeftAlone(t *testing.T) {
	var seen []string
	for _, row := range markdownRows(t, codeBlockDiff, "fence.md", 80) {
		text := rowText(row)
		if strings.HasPrefix(text, tableBar) || strings.HasPrefix(text, tableLeft) {
			t.Errorf("a table inside a fence was drawn as one: %q", text)
		}
		seen = append(seen, text)
	}
	for _, want := range []string{"| a | b |", "|---|---|"} {
		if !slices.Contains(seen, want) {
			t.Errorf("fenced row %q was not drawn verbatim, got %q", want, seen)
		}
	}
}

func TestCodeInABlockIsColouredByItsLanguage(t *testing.T) {
	h := NewHighlighter()
	if h == nil {
		t.Fatal("chroma is missing its terminal256 formatter or github-dark style")
	}
	got := h.Code("go", "func main() {}")
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("a fenced Go line = %q, want it coloured", got)
	}
	if plain := ansi.Strip(got); plain != "func main() {}" {
		t.Errorf("colouring changed the text to %q", plain)
	}
	if got := h.Code("no-such-language", "func main() {}"); got != "func main() {}" {
		t.Errorf("an unknown fence language = %q, want the line untouched", got)
	}
}

func TestEmphasisStopsAtACodeBlock(t *testing.T) {
	rows := markdownRows(t, codeBlockDiff, "fence.md", 80)
	var fenced, outside string
	for _, row := range rows {
		switch text := rowText(row); {
		case strings.Contains(text, "**not**"):
			fenced = row
		case strings.Contains(text, "**bold**"):
			outside = row
		}
	}
	if fenced == "" || outside == "" {
		t.Fatalf("fence.md drew no fenced line or no line past the fence: %q", rows)
	}
	if strings.Contains(fenced, markdownBold) {
		t.Errorf("stars inside a fence were read as emphasis: %q", fenced)
	}
	if !strings.Contains(outside, markdownBold) {
		t.Errorf("the line past the fence lost its emphasis: %q", outside)
	}
}

func TestACodeBlockMarkerIsDimmedRatherThanRead(t *testing.T) {
	h := NewHighlighter()
	if h == nil {
		t.Fatal("chroma is missing its terminal256 formatter or github-dark style")
	}
	dim := h.Line("notes.md", "|---|---|")
	dim = dim[:strings.Index(dim, "|")]
	for _, marker := range []string{"```", "```sql", "~~~", "   ```go"} {
		got := h.Line("notes.md", marker)
		if !strings.Contains(got, dim) {
			t.Errorf("fence marker %q = %q, want it dimmed like a table's pipes", marker, got)
		}
		if plain := ansi.Strip(got); plain != marker {
			t.Errorf("dimming changed the text to %q, want %q", plain, marker)
		}
	}
}

func TestEachSideOfARewrittenCodeBlockKeepsItsOwnLanguage(t *testing.T) {
	doc := Build(newSession(t, codeBlockDiff), nil, nil, LayoutUnified)
	var md *markdownHunk
	for _, ref := range doc.Hunks {
		if ref.Path == "fence.md" {
			md = ref.markdown
		}
	}
	if md == nil {
		t.Fatal("fence.md has no markdown layout")
	}

	lines := doc.Hunks[0].Hunk.Lines
	for i, l := range lines {
		if strings.Contains(l.Text, "**not**") {
			if got := md.at(i).lang; got != "json" {
				t.Errorf("the line under the rewritten fence reads as %q, want the new side's json", got)
			}
			if !md.at(i).code {
				t.Errorf("line %d is not marked as fenced code", i)
			}
			return
		}
	}
	t.Fatal("the fenced line was not found")
}

func TestCodeBlockMarkersAreRecognisedByShape(t *testing.T) {
	for _, tc := range []struct {
		line string
		lang string
		ok   bool
	}{
		{"```", "", true},
		{"```sql", "sql", true},
		{"~~~ Go", "go", true},
		{"````js", "js", true},
		{"  ```{.python}", "python", true},
		{"```js {highlight}", "js", true},
		{"``inline``", "", false},
		{"-- not a fence", "", false},
	} {
		_, _, info, ok := blockMarker(tc.line)
		if ok != tc.ok {
			t.Errorf("blockMarker(%q) ok = %v, want %v", tc.line, ok, tc.ok)
			continue
		}
		if ok {
			if got := blockLang(info); got != tc.lang {
				t.Errorf("blockLang(%q) = %q, want %q", tc.line, got, tc.lang)
			}
		}
	}
}

// tableEdgeRows renders every row of path, including the borders, which are not
// lines of the file and so are left out of markdownRows.
func tableEdgeRows(t *testing.T, diff, path string, layout Layout, width int) ([]string, Document) {
	t.Helper()
	doc := Build(newSession(t, diff), nil, nil, layout)
	r := NewRenderer(DefaultTheme(), NewHighlighter())
	r.SetWidth(width)

	var out []string
	for i, row := range doc.Rows {
		if row.Kind != RowLine && row.Kind != RowTableEdge {
			continue
		}
		if doc.Hunks[row.Hunk].Path != path {
			continue
		}
		out = append(out, strings.TrimRight(ansi.Strip(r.Row(doc, i, RowState{})), " "))
	}
	return out, doc
}

func TestADrawnTableIsClosedTopAndBottom(t *testing.T) {
	rows, _ := tableEdgeRows(t, tableDiff, "table.md", LayoutUnified, 200)
	want := []string{
		"       ┌─────────────┬────────────────┬──────┐",
		"    1  │ ledger      │          holds │ rec? │",
		"    2 -├─────────────┼────────────────┼──────┤",
		"    2 │├─────────────┼────────────────┼──────┤",
		"    3  │ PRIMARY     │ the real books │ yes  │",
		"    4 -│ ELIMINATION │         mirror │      │",
		"    4 ││ ELIMINATION │ mirror entries │  no  │",
		"    5 ││ BUDGET      │         a \\| b │  no  │",
		"       └─────────────┴────────────────┴──────┘",
		"    6",
		"    7  done | maybe",
	}
	if len(rows) != len(want) {
		t.Fatalf("table.md drew %d rows, want %d: %q", len(rows), len(want), rows)
	}
	for i, w := range want {
		if rows[i] != w {
			t.Errorf("row %d = %q, want %q", i, rows[i], w)
		}
	}
}

func TestATableBorderIsNoLineOfTheFile(t *testing.T) {
	_, doc := tableEdgeRows(t, tableDiff, "table.md", LayoutUnified, 200)
	edges := 0
	for i, row := range doc.Rows {
		if row.Kind != RowTableEdge {
			continue
		}
		edges++
		if doc.IsStop(i) {
			t.Errorf("row %d is a table border and the cursor stops on it", i)
		}
	}
	if edges != 2 {
		t.Errorf("table.md drew %d borders, want a top and a bottom", edges)
	}
}

func TestATableAddedWholeIsBorderedOnlyOnTheSideThatHasIt(t *testing.T) {
	const added = "diff --git a/new.md b/new.md\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/new.md\n" +
		"+++ b/new.md\n" +
		"@@ -1,1 +1,4 @@\n" +
		" intro\n" +
		"+| a | b |\n" +
		"+|---|---|\n" +
		"+| 1 | 2 |\n"

	rows, _ := tableEdgeRows(t, added, "new.md", LayoutSplit, 80)
	for _, row := range rows {
		if !strings.Contains(row, tableTopLeft) && !strings.Contains(row, tableFootLeft) {
			continue
		}
		left, _, found := strings.Cut(row, splitRule)
		if !found {
			t.Fatalf("split row has no divider: %q", row)
		}
		if strings.TrimSpace(left) != "" {
			t.Errorf("a table with no old side was bordered on the left half: %q", row)
		}
	}
}

func TestABorderIsAsWideAsTheTableItCloses(t *testing.T) {
	rows, _ := tableEdgeRows(t, tableDiff, "table.md", LayoutUnified, 200)
	widths := map[int]bool{}
	for _, row := range rows {
		text := rowText(row)
		if text == "" || !strings.ContainsAny(text, tableBar+tableLeft+tableTopLeft+tableFootLeft) {
			continue
		}
		widths[ansi.StringWidth(text)] = true
	}
	if len(widths) != 1 {
		t.Errorf("the drawn table has %d different widths, want one: %v", len(widths), widths)
	}
}
