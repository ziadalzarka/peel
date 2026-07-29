package git

import (
	"strings"
	"testing"
)

// parseOne parses a diff expected to contain exactly one file.
func parseOne(t *testing.T, in string) FileDiff {
	t.Helper()
	d, err := ParseDiff(in)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("got %d files, want 1", len(d.Files))
	}
	return d.Files[0]
}

func TestBuildPatchRoundTripsSingleHunk(t *testing.T) {
	const in = `diff --git a/f.txt b/f.txt
index 111..222 100644
--- a/f.txt
+++ b/f.txt
@@ -10,3 +10,4 @@ func x() {
 a
-b
+c
+d
 e
`

	f := parseOne(t, in)
	got, err := BuildPatch(f, f.Hunks, Forward)
	if err != nil {
		t.Fatalf("BuildPatch: %v", err)
	}

	const want = `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -10,3 +10,4 @@
 a
-b
+c
+d
 e
`
	if got != want {
		t.Errorf("BuildPatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildPatchRecomputesLaterHunkOffsets(t *testing.T) {
	// Staging only the second hunk means the first hunk's line growth never
	// happens, so the new-side start must fall back to the old-side start.
	const in = `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -1,3 +1,5 @@
 a
+x
+y
 b
 c
@@ -100,3 +102,4 @@
 p
+q
 r
 s
`

	f := parseOne(t, in)
	got, err := BuildPatch(f, f.Hunks[1:], Forward)
	if err != nil {
		t.Fatalf("BuildPatch: %v", err)
	}

	if !strings.Contains(got, "@@ -100,3 +100,4 @@") {
		t.Errorf("want new-side start rebased to 100, got:\n%s", got)
	}
	if strings.Contains(got, "+102") {
		t.Errorf("new-side start still carries the skipped hunk's offset:\n%s", got)
	}
}

func TestBuildPatchAccumulatesDeltaAcrossHunks(t *testing.T) {
	const in = `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -1,3 +1,5 @@
 a
+x
+y
 b
 c
@@ -100,3 +102,4 @@
 p
+q
 r
 s
`

	f := parseOne(t, in)
	got, err := BuildPatch(f, f.Hunks, Forward)
	if err != nil {
		t.Fatalf("BuildPatch: %v", err)
	}

	// First hunk adds two lines, so the second hunk's new side shifts by two.
	if !strings.Contains(got, "@@ -1,3 +1,5 @@") {
		t.Errorf("first hunk header wrong:\n%s", got)
	}
	if !strings.Contains(got, "@@ -100,3 +102,4 @@") {
		t.Errorf("want second hunk rebased to +102, got:\n%s", got)
	}
}

func TestBuildPatchReverseAnchorsOnNewSide(t *testing.T) {
	// Unstaging applies with --reverse, so the new side is what must match the
	// index and the old side is the one recomputed.
	const in = `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -1,3 +1,5 @@
 a
+x
+y
 b
 c
@@ -100,3 +102,4 @@
 p
+q
 r
 s
`

	f := parseOne(t, in)
	got, err := BuildPatch(f, f.Hunks[1:], Reverse)
	if err != nil {
		t.Fatalf("BuildPatch: %v", err)
	}

	if !strings.Contains(got, "@@ -102,3 +102,4 @@") {
		t.Errorf("want old side rebased onto the new-side anchor 102, got:\n%s", got)
	}
}

func TestBuildPatchPreservesNoNewlineMarker(t *testing.T) {
	const in = `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -1 +1 @@
-old
\ No newline at end of file
+new
\ No newline at end of file
`

	f := parseOne(t, in)
	got, err := BuildPatch(f, f.Hunks, Forward)
	if err != nil {
		t.Fatalf("BuildPatch: %v", err)
	}
	if n := strings.Count(got, `\ No newline at end of file`); n != 2 {
		t.Errorf("got %d no-newline markers, want 2:\n%s", n, got)
	}
}

func TestBuildPatchFileHeaders(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantHead []string
	}{
		{
			name: "added file",
			in: `diff --git a/new.txt b/new.txt
new file mode 100644
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+hello
`,
			wantHead: []string{"new file mode 100644", "--- /dev/null", "+++ b/new.txt"},
		},
		{
			name: "deleted file",
			in: `diff --git a/old.txt b/old.txt
deleted file mode 100644
--- a/old.txt
+++ /dev/null
@@ -1 +0,0 @@
-hello
`,
			wantHead: []string{"deleted file mode 100644", "--- a/old.txt", "+++ /dev/null"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := parseOne(t, tt.in)
			got, err := BuildPatch(f, f.Hunks, Forward)
			if err != nil {
				t.Fatalf("BuildPatch: %v", err)
			}
			for _, want := range tt.wantHead {
				if !strings.Contains(got, want) {
					t.Errorf("patch missing %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestBuildPatchZeroCountUsesLineZero(t *testing.T) {
	const in = `diff --git a/new.txt b/new.txt
new file mode 100644
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+a
+b
`

	f := parseOne(t, in)
	got, err := BuildPatch(f, f.Hunks, Forward)
	if err != nil {
		t.Fatalf("BuildPatch: %v", err)
	}
	if !strings.Contains(got, "@@ -0,0 +1,2 @@") {
		t.Errorf("want empty old side addressed as -0,0:\n%s", got)
	}
}

func TestBuildPatchRejectsBinary(t *testing.T) {
	f := FileDiff{NewPath: "img.png", OldPath: "img.png", IsBinary: true}
	if _, err := BuildPatch(f, []Hunk{{}}, Forward); err == nil {
		t.Fatal("BuildPatch succeeded on a binary file, want error")
	}
}

func TestBuildPatchRejectsNoHunks(t *testing.T) {
	f := FileDiff{NewPath: "f.txt", OldPath: "f.txt"}
	if _, err := BuildPatch(f, nil, Forward); err == nil {
		t.Fatal("BuildPatch succeeded with no hunks, want error")
	}
}

func TestBuildPatchRejectsInconsistentCounts(t *testing.T) {
	// A hunk whose declared counts disagree with its body would corrupt the
	// index silently, so it must be caught before reaching git.
	f := FileDiff{NewPath: "f.txt", OldPath: "f.txt"}
	h := Hunk{
		OldStart: 1, OldCount: 99, NewStart: 1, NewCount: 1,
		Lines: []Line{{Kind: LineContext, Text: "a"}},
	}
	_, err := BuildPatch(f, []Hunk{h}, Forward)
	if err == nil {
		t.Fatal("BuildPatch succeeded with mismatched counts, want error")
	}
	if !strings.Contains(err.Error(), "disagree") {
		t.Errorf("error = %v, want it to name the count mismatch", err)
	}
}

func TestBuildPatchQuotesUnusualPaths(t *testing.T) {
	f := FileDiff{OldPath: `we"ird.txt`, NewPath: `we"ird.txt`, Status: StatusModified}
	h := Hunk{
		OldStart: 1, OldCount: 1, NewStart: 1, NewCount: 1,
		Lines: []Line{{Kind: LineRemoved, Text: "a"}, {Kind: LineAdded, Text: "b"}},
	}
	got, err := BuildPatch(f, []Hunk{h}, Forward)
	if err != nil {
		t.Fatalf("BuildPatch: %v", err)
	}
	if !strings.Contains(got, `"a/we\"ird.txt"`) {
		t.Errorf("path not quoted:\n%s", got)
	}
}

// hunkFrom builds a hunk from a compact "kind:text" spec for selection tests.
func hunkFrom(t *testing.T, specs ...string) Hunk {
	t.Helper()
	h := Hunk{OldStart: 1, NewStart: 1}
	for _, s := range specs {
		kind, text, _ := strings.Cut(s, ":")
		var k LineKind
		switch kind {
		case " ":
			k = LineContext
		case "+":
			k = LineAdded
		case "-":
			k = LineRemoved
		case `\`:
			k = LineNoNewline
		default:
			t.Fatalf("bad line spec %q", s)
		}
		h.Lines = append(h.Lines, Line{Kind: k, Text: text})
	}
	h.OldCount, h.NewCount = countSides(h.Lines)
	return h
}

// renderLines renders a hunk body as compact specs for comparison.
func renderLines(h Hunk) []string {
	out := make([]string, 0, len(h.Lines))
	for _, l := range h.Lines {
		out = append(out, string(l.Kind.Origin())+":"+l.Text)
	}
	return out
}

func TestSelectLinesForward(t *testing.T) {
	// Forward stages into the index, where the old side is what exists today:
	// an unselected addition is dropped, an unselected removal stays as context.
	h := hunkFrom(t, " :ctx", "-:del1", "-:del2", "+:add1", "+:add2", " :end")

	got, ok := SelectLines(h, map[int]bool{1: true, 3: true}, Forward)
	if !ok {
		t.Fatal("SelectLines returned ok=false, want a usable hunk")
	}

	want := []string{" :ctx", "-:del1", " :del2", "+:add1", " :end"}
	assertLines(t, renderLines(got), want)

	if got.OldCount != 4 || got.NewCount != 4 {
		t.Errorf("counts = -%d +%d, want -4 +4", got.OldCount, got.NewCount)
	}
}

func TestSelectLinesReverse(t *testing.T) {
	// Reverse unstages, where the new side is what exists today: an unselected
	// removal is dropped, an unselected addition stays as context.
	h := hunkFrom(t, " :ctx", "-:del1", "-:del2", "+:add1", "+:add2", " :end")

	got, ok := SelectLines(h, map[int]bool{1: true, 3: true}, Reverse)
	if !ok {
		t.Fatal("SelectLines returned ok=false, want a usable hunk")
	}

	want := []string{" :ctx", "-:del1", "+:add1", " :add2", " :end"}
	assertLines(t, renderLines(got), want)

	if got.OldCount != 4 || got.NewCount != 4 {
		t.Errorf("counts = -%d +%d, want -4 +4", got.OldCount, got.NewCount)
	}
}

func TestSelectLinesAllSelectedMatchesWholeHunk(t *testing.T) {
	h := hunkFrom(t, " :ctx", "-:del", "+:add", " :end")
	all := map[int]bool{1: true, 2: true}

	got, ok := SelectLines(h, all, Forward)
	if !ok {
		t.Fatal("SelectLines returned ok=false")
	}
	assertLines(t, renderLines(got), renderLines(h))
	if got.OldCount != h.OldCount || got.NewCount != h.NewCount {
		t.Errorf("counts = -%d +%d, want -%d +%d", got.OldCount, got.NewCount, h.OldCount, h.NewCount)
	}
}

func TestSelectLinesNothingSelected(t *testing.T) {
	h := hunkFrom(t, " :ctx", "-:del", "+:add")
	if _, ok := SelectLines(h, map[int]bool{}, Forward); ok {
		t.Fatal("SelectLines returned ok=true for an empty selection")
	}
}

func TestSelectLinesContextOnlyIsNoOp(t *testing.T) {
	// Selecting only context lines leaves no actual change.
	h := hunkFrom(t, " :a", " :b")
	if _, ok := SelectLines(h, map[int]bool{0: true}, Forward); ok {
		t.Fatal("SelectLines returned ok=true for a context-only selection")
	}
}

func TestSelectLinesDropsOrphanedNoNewlineMarker(t *testing.T) {
	// The marker qualifies the line before it; if that line is dropped the
	// marker must go too or the patch is corrupt.
	h := hunkFrom(t, " :ctx", "+:add", `\:No newline at end of file`)

	got, ok := SelectLines(h, map[int]bool{}, Forward)
	if ok {
		t.Fatalf("SelectLines returned ok=true, got %v", renderLines(got))
	}

	// With the addition selected, the marker survives.
	got, ok = SelectLines(h, map[int]bool{1: true}, Forward)
	if !ok {
		t.Fatal("SelectLines returned ok=false with the addition selected")
	}
	assertLines(t, renderLines(got), []string{" :ctx", "+:add", `\:No newline at end of file`})
}

func TestSelectLinesKeepsMarkerAfterConvertedContext(t *testing.T) {
	h := hunkFrom(t, "-:old", `\:No newline at end of file`, "+:new")

	got, ok := SelectLines(h, map[int]bool{2: true}, Forward)
	if !ok {
		t.Fatal("SelectLines returned ok=false")
	}
	// The removal became context, so its marker still applies.
	assertLines(t, renderLines(got), []string{" :old", `\:No newline at end of file`, "+:new"})
}

func TestSelectLinesThenBuildPatchProducesValidCounts(t *testing.T) {
	const in = `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -1,4 +1,4 @@
 a
-b
-c
+d
+e
 f
`

	f := parseOne(t, in)
	sel, ok := SelectLines(f.Hunks[0], map[int]bool{1: true, 3: true}, Forward)
	if !ok {
		t.Fatal("SelectLines returned ok=false")
	}

	// BuildPatch verifies declared counts against the body, so a successful
	// build proves SelectLines recomputed them correctly. Keeping "-b" and
	// "+d" while demoting "-c" to context leaves both sides at four lines.
	got, err := BuildPatch(f, []Hunk{sel}, Forward)
	if err != nil {
		t.Fatalf("BuildPatch: %v", err)
	}
	if !strings.Contains(got, "@@ -1,4 +1,4 @@") {
		t.Errorf("unexpected header:\n%s", got)
	}
}

func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDirectionString(t *testing.T) {
	if Forward.String() != "forward" || Reverse.String() != "reverse" {
		t.Errorf("Direction.String() = %q / %q", Forward, Reverse)
	}
}
