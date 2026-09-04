package git

import (
	"strings"
	"testing"
)

// parseOne parses a diff expected to hold exactly one file.
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

// A hunk that is the whole diff comes back out as it went in, minus the section
// git printed for whoever was reading it.
func TestPatchRoundTripsAHunk(t *testing.T) {
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
	got, err := Patch(f, f.Hunks[0])
	if err != nil {
		t.Fatalf("Patch: %v", err)
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
		t.Errorf("Patch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// The header of a later hunk counts the lines the hunks above it added. Staging
// it alone adds none of them, so the new side has to be numbered again — this is
// the arithmetic that writes the wrong lines into the index when it is skipped.
func TestPatchNumbersTheNewSideForThisHunkAlone(t *testing.T) {
	const in = `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -1,3 +1,5 @@
 a
+one
+two
 b
 c
@@ -20,3 +22,4 @@
 x
+three
 y
 z
`

	f := parseOne(t, in)
	got, err := Patch(f, f.Hunks[1])
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !strings.Contains(got, "@@ -20,3 +20,4 @@") {
		t.Errorf("want the new side back at line 20, since the hunk above is not in this patch:\n%s", got)
	}
}

// The marker qualifies the line above it and says whether the file ends with a
// newline. Dropping it writes a newline nobody typed.
func TestPatchKeepsTheNoNewlineMarker(t *testing.T) {
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
	got, err := Patch(f, f.Hunks[0])
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if n := strings.Count(got, `\ No newline at end of file`); n != 2 {
		t.Errorf("got %d no-newline markers, want both:\n%s", n, got)
	}
}

// A side that does not exist is named /dev/null and given the mode of the side
// that does, since git apply has to create or remove the file rather than edit
// it.
func TestPatchHeadersNameTheMissingSide(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "added file",
			in: `diff --git a/new.txt b/new.txt
new file mode 100644
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+hello
+there
`,
			want: []string{"new file mode 100644", "--- /dev/null", "+++ b/new.txt", "@@ -0,0 +1,2 @@"},
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
			want: []string{"deleted file mode 100644", "--- a/old.txt", "+++ /dev/null", "@@ -1 +0,0 @@"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := parseOne(t, tt.in)
			got, err := Patch(f, f.Hunks[0])
			if err != nil {
				t.Fatalf("Patch: %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("patch missing %q:\n%s", want, got)
				}
			}
		})
	}
}

// There are no hunks to stage, so there is nothing to render — and a patch built
// out of a binary file's absent body would be a patch that deletes it.
func TestPatchRefusesABinaryFile(t *testing.T) {
	f := FileDiff{OldPath: "img.png", NewPath: "img.png", IsBinary: true}
	if _, err := Patch(f, Hunk{}); err == nil {
		t.Fatal("Patch built one for a binary file")
	}
}

// A header that disagrees with the body is a patch git reads as further lines
// than it was given. Catching it here is the difference between a refusal and a
// silently wrong index.
func TestPatchRefusesAHunkThatDisagreesWithItself(t *testing.T) {
	f := FileDiff{OldPath: "f.txt", NewPath: "f.txt"}
	h := Hunk{
		OldStart: 1, OldCount: 99, NewStart: 1, NewCount: 1,
		Lines: []Line{{Kind: LineContext, Text: "a"}},
	}
	_, err := Patch(f, h)
	if err == nil {
		t.Fatal("Patch built one from counts that do not match the body")
	}
	if !strings.Contains(err.Error(), "declares") {
		t.Errorf("error = %v, want it to name the mismatch", err)
	}
}

// git quotes a path it could not otherwise read back, and a patch it cannot read
// back is a patch it applies to the wrong file or not at all.
func TestPatchQuotesAPathGitWould(t *testing.T) {
	f := FileDiff{OldPath: `we"ird.txt`, NewPath: `we"ird.txt`}
	h := Hunk{
		OldStart: 1, OldCount: 1, NewStart: 1, NewCount: 1,
		Lines: []Line{{Kind: LineRemoved, Text: "a"}, {Kind: LineAdded, Text: "b"}},
	}
	got, err := Patch(f, h)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !strings.Contains(got, `"a/we\"ird.txt"`) {
		t.Errorf("path not quoted:\n%s", got)
	}
}
