package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/git"
)

const (
	markdownBold      = "\x1b[1m"
	markdownItalic    = "\x1b[3m"
	markdownUnderline = "\x1b[4m"
)

func TestAnAddedMarkdownLineIsMarkedRatherThanBanded(t *testing.T) {
	doc := Build(newSession(t, threeFileDiff), nil, nil, LayoutUnified)
	r := plainRenderer(40)
	r.addedFill, r.removedFill = testAddedFill, testRemovedFill

	var added, removed string
	for i, row := range doc.Rows {
		if row.Kind != RowLine || doc.Hunks[row.Hunk].Path != "gamma.md" {
			continue
		}
		switch doc.Hunks[row.Hunk].Hunk.Lines[row.Left].Kind {
		case git.LineAdded:
			added = r.Row(doc, i, RowState{})
		case git.LineRemoved:
			removed = r.Row(doc, i, RowState{})
		}
	}
	if added == "" || removed == "" {
		t.Fatalf("gamma.md rendered no added or no removed row: added %q, removed %q", added, removed)
	}

	if strings.Contains(added, testAddedFill) {
		t.Errorf("added markdown line = %q, want no green band", added)
	}
	if !strings.Contains(added, markdownAddedMark+"second") {
		t.Errorf("added markdown line = %q, want a bar where the + stands", added)
	}
	if !strings.Contains(removed, testRemovedFill) || !strings.Contains(removed, "-first") {
		t.Errorf("removed markdown line = %q, want it banded red as before", removed)
	}
}

func TestASplitMarkdownRowBandsOnlyItsRemovedSide(t *testing.T) {
	doc := Build(newSession(t, threeFileDiff), nil, nil, LayoutSplit)
	r := plainRenderer(80)
	r.addedFill, r.removedFill = testAddedFill, testRemovedFill

	var got string
	for i, row := range doc.Rows {
		if row.Kind == RowLine && row.Left >= 0 && row.Right >= 0 &&
			doc.Hunks[row.Hunk].Path == "gamma.md" &&
			doc.Hunks[row.Hunk].Hunk.Lines[row.Left].Kind == git.LineRemoved {
			got = r.Row(doc, i, RowState{})
		}
	}
	if got == "" {
		t.Fatal("no replaced markdown line was rendered side by side")
	}
	if !strings.Contains(got, testRemovedFill) {
		t.Errorf("split row = %q, want the removed side banded red", got)
	}
	if strings.Contains(got, testAddedFill) {
		t.Errorf("split row = %q, want no green band on the added side", got)
	}
	if !strings.Contains(got, markdownAddedMark+"second") {
		t.Errorf("split row = %q, want a bar where the + stands", got)
	}
}

func TestMarkdownIsStyledWithoutChangingItsText(t *testing.T) {
	h := NewHighlighter()
	if h == nil {
		t.Fatal("chroma is missing its terminal256 formatter or github-dark style, or the Markdown style failed to build")
	}
	for _, tc := range []struct{ name, line, want string }{
		{"a heading is bold", "## Getting started", markdownBold},
		{"a deeper heading is bold", "#### Flags", markdownBold},
		{"bold text is bold", "run **only** this", markdownBold},
		{"underscored bold text is bold", "run __only__ this", markdownBold},
		{"italic text is italic", "run *only* this", markdownItalic},
		{"underscored italic text is italic", "run _only_ this", markdownItalic},
		{"link text is underlined", "see [the docs](https://example.com/docs)", markdownUnderline},
		{"a bare link is underlined", "see https://example.com/docs", markdownUnderline},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := h.Line("README.md", tc.line)
			if !strings.Contains(got, tc.want) {
				t.Errorf("%q = %q, want %q in it", tc.line, got, tc.want)
			}
			if plain := ansi.Strip(got); plain != tc.line {
				t.Errorf("styling changed the text to %q, want %q", plain, tc.line)
			}
		})
	}
}

func TestMarkdownLeavesWhatOnlyLooksLikeEmphasisPlain(t *testing.T) {
	h := NewHighlighter()
	if h == nil {
		t.Fatal("chroma is missing its terminal256 formatter or github-dark style, or the Markdown style failed to build")
	}
	for _, line := range []string{
		"`a **b** c`",
		"call snake_case_name here",
		"2 * 3 * 4",
		"#hashtag",
		"- a list item",
		`a \*literal\* star`,
	} {
		got := h.Line("notes.md", line)
		for _, seq := range []string{markdownBold, markdownItalic, markdownUnderline} {
			if strings.Contains(got, seq) {
				t.Errorf("%q = %q, want no %q in it", line, got, seq)
			}
		}
		if plain := ansi.Strip(got); plain != line {
			t.Errorf("styling changed the text to %q, want %q", plain, line)
		}
	}
}

func TestMarkdownTablePipesAreDimmedAndTheirCellsAreNot(t *testing.T) {
	h := NewHighlighter()
	if h == nil {
		t.Fatal("chroma is missing its terminal256 formatter or github-dark style, or the Markdown style failed to build")
	}
	separator := h.Line("notes.md", "|---|:---:|")
	dim := separator[:strings.Index(separator, "|")]
	if dim == "" {
		t.Fatalf("separator row = %q, want it coloured", separator)
	}

	row := h.Line("notes.md", "| name | value |")
	if got := strings.Count(row, dim+"|"); got != 3 {
		t.Errorf("table row = %q, want its 3 pipes dimmed, got %d", row, got)
	}
	if strings.Contains(row, dim+" name") {
		t.Errorf("table row = %q, want the cell text left undimmed", row)
	}
	if plain := ansi.Strip(row); plain != "| name | value |" {
		t.Errorf("styling changed the text to %q", plain)
	}
}

func TestMarkdownStylingStaysOutOfOtherLanguages(t *testing.T) {
	h := NewHighlighter()
	if h == nil {
		t.Fatal("chroma is missing its terminal256 formatter or github-dark style, or the Markdown style failed to build")
	}
	if got := h.Line("main.go", "x := a * b * c"); strings.Contains(got, markdownItalic) {
		t.Errorf("a Go line picked up markdown italics: %q", got)
	}
}

func TestMarkdownIsRecognisedByExtension(t *testing.T) {
	for path, want := range map[string]bool{
		"README.md":           true,
		"docs/guide.markdown": true,
		"docs/NOTES.MD":       true,
		"main.go":             false,
		"md":                  false,
	} {
		if got := isMarkdown(path); got != want {
			t.Errorf("isMarkdown(%q) = %v, want %v", path, got, want)
		}
	}
}
