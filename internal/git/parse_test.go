package git

import (
	"strings"
	"testing"
)

func TestParseDiffModifiedFile(t *testing.T) {
	const in = `diff --git a/src/main.go b/src/main.go
index 83db48f..bf269f4 100644
--- a/src/main.go
+++ b/src/main.go
@@ -10,6 +10,7 @@ func handle(r *Req) error {
 	ctx := r.Context()
-	db.Exec(q)
+	tx := db.Begin()
+	tx.Exec(q)
 	return nil
 }
`

	d, err := ParseDiff(in)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("got %d files, want 1", len(d.Files))
	}

	f := d.Files[0]
	if f.Path() != "src/main.go" {
		t.Errorf("Path() = %q, want src/main.go", f.Path())
	}
	if f.Status != StatusModified {
		t.Errorf("Status = %q, want modified", f.Status)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(f.Hunks))
	}

	h := f.Hunks[0]
	if h.OldStart != 10 || h.OldCount != 6 || h.NewStart != 10 || h.NewCount != 7 {
		t.Errorf("ranges = -%d,%d +%d,%d, want -10,6 +10,7", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
	}
	if h.Section != "func handle(r *Req) error {" {
		t.Errorf("Section = %q", h.Section)
	}

	added, removed := h.Stats()
	if added != 2 || removed != 1 {
		t.Errorf("Stats() = +%d -%d, want +2 -1", added, removed)
	}
}

func TestParseDiffLineNumbers(t *testing.T) {
	const in = `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -5,4 +5,5 @@
 keep1
-gone
+new1
+new2
 keep2
`

	d, err := ParseDiff(in)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}

	want := []struct {
		kind    LineKind
		text    string
		oldLine int
		newLine int
	}{
		{LineContext, "keep1", 5, 5},
		{LineRemoved, "gone", 6, 0},
		{LineAdded, "new1", 0, 6},
		{LineAdded, "new2", 0, 7},
		{LineContext, "keep2", 7, 8},
	}

	got := d.Files[0].Hunks[0].Lines
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Kind != w.kind || g.Text != w.text || g.OldLine != w.oldLine || g.NewLine != w.newLine {
			t.Errorf("line %d = {%v %q old=%d new=%d}, want {%v %q old=%d new=%d}",
				i, g.Kind, g.Text, g.OldLine, g.NewLine, w.kind, w.text, w.oldLine, w.newLine)
		}
	}
}

func TestParseDiffStatuses(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantStatus FileStatus
		wantOld    string
		wantNew    string
		wantBinary bool
	}{
		{
			name: "added",
			in: `diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..3b18e51
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+hello
`,
			wantStatus: StatusAdded,
			wantOld:    "new.txt",
			wantNew:    "new.txt",
		},
		{
			name: "deleted",
			in: `diff --git a/old.txt b/old.txt
deleted file mode 100644
index 3b18e51..0000000
--- a/old.txt
+++ /dev/null
@@ -1 +0,0 @@
-hello
`,
			wantStatus: StatusDeleted,
			wantOld:    "old.txt",
			wantNew:    "old.txt",
		},
		{
			name: "renamed",
			in: `diff --git a/from.txt b/to.txt
similarity index 100%
rename from from.txt
rename to to.txt
`,
			wantStatus: StatusRenamed,
			wantOld:    "from.txt",
			wantNew:    "to.txt",
		},
		{
			name: "binary",
			in: `diff --git a/img.png b/img.png
index 1234567..89abcde 100644
Binary files a/img.png and b/img.png differ
`,
			wantStatus: StatusModified,
			wantOld:    "img.png",
			wantNew:    "img.png",
			wantBinary: true,
		},
		{
			name: "mode change only",
			in: `diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
`,
			wantStatus: StatusModified,
			wantOld:    "run.sh",
			wantNew:    "run.sh",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := ParseDiff(tt.in)
			if err != nil {
				t.Fatalf("ParseDiff: %v", err)
			}
			if len(d.Files) != 1 {
				t.Fatalf("got %d files, want 1", len(d.Files))
			}
			f := d.Files[0]
			if f.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", f.Status, tt.wantStatus)
			}
			if f.OldPath != tt.wantOld {
				t.Errorf("OldPath = %q, want %q", f.OldPath, tt.wantOld)
			}
			if f.NewPath != tt.wantNew {
				t.Errorf("NewPath = %q, want %q", f.NewPath, tt.wantNew)
			}
			if f.IsBinary != tt.wantBinary {
				t.Errorf("IsBinary = %v, want %v", f.IsBinary, tt.wantBinary)
			}
		})
	}
}

func TestParseDiffNoNewlineMarker(t *testing.T) {
	const in = `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -1 +1 @@
-old
\ No newline at end of file
+new
\ No newline at end of file
`

	d, err := ParseDiff(in)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	lines := d.Files[0].Hunks[0].Lines
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	if lines[1].Kind != LineNoNewline {
		t.Errorf("lines[1].Kind = %v, want LineNoNewline", lines[1].Kind)
	}
	if lines[3].Kind != LineNoNewline {
		t.Errorf("lines[3].Kind = %v, want LineNoNewline", lines[3].Kind)
	}
	// The marker must round trip byte-for-byte or the apply corrupts the file.
	if got := lines[1].Render(); got != `\ No newline at end of file` {
		t.Errorf("Render() = %q", got)
	}
}

func TestParseDiffMultipleHunksAndFiles(t *testing.T) {
	const in = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,3 +1,4 @@
 one
+two
 three
 four
@@ -20,3 +21,3 @@
 x
-y
+z
diff --git a/b.txt b/b.txt
--- a/b.txt
+++ b/b.txt
@@ -1 +1 @@
-p
+q
`

	d, err := ParseDiff(in)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	if len(d.Files) != 2 {
		t.Fatalf("got %d files, want 2", len(d.Files))
	}
	if len(d.Files[0].Hunks) != 2 {
		t.Errorf("file 0: got %d hunks, want 2", len(d.Files[0].Hunks))
	}
	if len(d.Files[1].Hunks) != 1 {
		t.Errorf("file 1: got %d hunks, want 1", len(d.Files[1].Hunks))
	}
	if got := d.Files[0].Hunks[1].OldStart; got != 20 {
		t.Errorf("second hunk OldStart = %d, want 20", got)
	}
}

func TestParseDiffAbbreviatedRange(t *testing.T) {
	// git omits the count when it is exactly 1.
	const in = `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -3 +3 @@
-a
+b
`

	d, err := ParseDiff(in)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	h := d.Files[0].Hunks[0]
	if h.OldStart != 3 || h.OldCount != 1 || h.NewStart != 3 || h.NewCount != 1 {
		t.Errorf("ranges = -%d,%d +%d,%d, want -3,1 +3,1", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
	}
	// Rendering must abbreviate the same way git does.
	if got := h.Header(); got != "@@ -3 +3 @@" {
		t.Errorf("Header() = %q, want @@ -3 +3 @@", got)
	}
}

func TestParseDiffEmptyContextLine(t *testing.T) {
	// git emits a bare "" rather than " " for a blank context line when
	// trailing whitespace is stripped in transit.
	in := "diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1,3 +1,3 @@\n a\n\n-b\n+c\n"

	d, err := ParseDiff(in)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	lines := d.Files[0].Hunks[0].Lines
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	if lines[1].Kind != LineContext || lines[1].Text != "" {
		t.Errorf("lines[1] = {%v %q}, want blank context", lines[1].Kind, lines[1].Text)
	}
}

func TestParseDiffPreservesCarriageReturns(t *testing.T) {
	in := "diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-old\r\n+new\r\n"

	d, err := ParseDiff(in)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	lines := d.Files[0].Hunks[0].Lines
	if lines[0].Text != "old\r" {
		t.Errorf("removed text = %q, want %q", lines[0].Text, "old\r")
	}
	if lines[1].Text != "new\r" {
		t.Errorf("added text = %q, want %q", lines[1].Text, "new\r")
	}
}

func TestParseDiffQuotedPaths(t *testing.T) {
	const in = `diff --git "a/dir/f\303\251.txt" "b/dir/f\303\251.txt"
--- "a/dir/f\303\251.txt"
+++ "b/dir/f\303\251.txt"
@@ -1 +1 @@
-a
+b
`

	d, err := ParseDiff(in)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	if got := d.Files[0].Path(); got != "dir/fé.txt" {
		t.Errorf("Path() = %q, want dir/fé.txt", got)
	}
}

func TestParseDiffEmptyInput(t *testing.T) {
	d, err := ParseDiff("")
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	if len(d.Files) != 0 {
		t.Errorf("got %d files, want 0", len(d.Files))
	}
}

func TestParseDiffSkipsPreamble(t *testing.T) {
	// `git show` prefixes the diff with commit metadata.
	const in = `commit abc123
Author: Someone <s@example.com>

    subject line

diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -1 +1 @@
-a
+b
`

	d, err := ParseDiff(in)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("got %d files, want 1", len(d.Files))
	}
}

func TestParseDiffMalformedHeader(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"missing closing at", "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,2 +1,2\n a\n"},
		{"non numeric range", "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -x,2 +1,2 @@\n a\n"},
		{"one range only", "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,2 @@\n a\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseDiff(tt.in); err == nil {
				t.Fatal("ParseDiff succeeded, want error")
			}
		})
	}
}

func TestParseDiffRejectsUnknownBodyLine(t *testing.T) {
	in := "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,2 +1,2 @@\n a\n?bogus\n"
	_, err := ParseDiff(in)
	if err == nil {
		t.Fatal("ParseDiff succeeded, want error")
	}
	if !strings.Contains(err.Error(), "unrecognized line") {
		t.Errorf("error = %v, want it to mention the unrecognized line", err)
	}
}

func TestUnquotePath(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`plain.txt`, `plain.txt`},
		{`"simple.txt"`, `simple.txt`},
		{`"f\303\251.txt"`, `fé.txt`},
		{`"with space.txt"`, `with space.txt`},
		{`"tab\there.txt"`, "tab\there.txt"},
		{`"quote\"in.txt"`, `quote"in.txt`},
		{`"back\\slash.txt"`, `back\slash.txt`},
	}

	for _, tt := range tests {
		if got := unquotePath(tt.in); got != tt.want {
			t.Errorf("unquotePath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
