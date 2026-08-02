package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
)

// pathSession builds a session whose files are the given paths, each with one
// replaced line, in the order they are named.
func pathSession(t *testing.T, paths ...string) *app.Session {
	t.Helper()
	var b strings.Builder
	for i, path := range paths {
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n"+
			"index 1111111..2222222 100644\n--- a/%s\n+++ b/%s\n"+
			"@@ -1,2 +1,2 @@\n keep\n-old%d\n+new%d\n", path, path, path, path, i, i)
	}
	return newSession(t, b.String())
}

// filesOf turns paths into the document's view of them, for the tree on its own.
func filesOf(t *testing.T, paths ...string) []FileRef {
	t.Helper()
	entries := pathSession(t, paths...).Files
	files := make([]FileRef, 0, len(entries))
	for _, e := range entries {
		files = append(files, FileRef{Entry: e})
	}
	return files
}

// layout renders the tree as "depth:name" lines, which is what the pane draws
// minus the styling.
func layout(rows []paneRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		name := row.Name
		if row.File < 0 {
			name += "/"
		}
		out = append(out, fmt.Sprintf("%d:%s", row.Depth, name))
	}
	return out
}

func TestFileTreePutsFilesUnderTheirDirectories(t *testing.T) {
	rows := fileTree(filesOf(t, "internal/git/repo.go", "internal/tui/view.go", "main.go"))

	want := []string{"0:internal/", "1:git/", "2:repo.go", "1:tui/", "2:view.go", "0:main.go"}
	if got := layout(rows); !equal(got, want) {
		t.Errorf("the tree reads\n%v\nwant\n%v", got, want)
	}
}

// A directory with one way down is joined onto what is below it: the pane is
// narrow, and a level that only ever leads to one place says nothing an indent
// would not.
func TestFileTreeJoinsADirectoryWithOneWayDown(t *testing.T) {
	rows := fileTree(filesOf(t, "internal/tui/view.go", "internal/tui/model.go"))

	want := []string{"0:internal/tui/", "1:view.go", "1:model.go"}
	if got := layout(rows); !equal(got, want) {
		t.Errorf("the tree reads\n%v\nwant\n%v", got, want)
	}
}

// A directory holding a changed file of its own is a level worth drawing, so it
// is not joined onto the directory below it.
func TestFileTreeKeepsADirectoryThatHoldsAFile(t *testing.T) {
	rows := fileTree(filesOf(t, "internal/app.go", "internal/tui/view.go"))

	want := []string{"0:internal/", "1:app.go", "1:tui/", "2:view.go"}
	if got := layout(rows); !equal(got, want) {
		t.Errorf("the tree reads\n%v\nwant\n%v", got, want)
	}
}

// Rows come out in the order the document holds the files — under a
// walkthrough that is the narrative's order, not git's — so reading the pane
// top to bottom still reads the diff top to bottom. A directory takes the
// position of the first file inside it, and collects the ones further down.
func TestFileTreeFollowsTheDocumentsOrder(t *testing.T) {
	rows := fileTree(filesOf(t, "tui/view.go", "main.go", "tui/model.go"))

	want := []string{"0:tui/", "1:view.go", "1:model.go", "0:main.go"}
	if got := layout(rows); !equal(got, want) {
		t.Errorf("the tree reads\n%v\nwant\n%v", got, want)
	}
}

func TestFileTreeOfFilesWithNoDirectory(t *testing.T) {
	rows := fileTree(filesOf(t, "a.go", "b.go"))

	want := []string{"0:a.go", "0:b.go"}
	if got := layout(rows); !equal(got, want) {
		t.Errorf("the tree reads\n%v\nwant\n%v", got, want)
	}
	if rows := fileTree(nil); len(rows) != 0 {
		t.Errorf("the tree of no files has %d rows, want none", len(rows))
	}
}

// A directory reads as staged once every file under it is, so a pass down the
// diff can be seen to be finishing off whole directories.
func TestFileTreeStatesADirectoryByTheFilesUnderIt(t *testing.T) {
	files := filesOf(t, "tui/view.go", "tui/model.go", "main.go")
	states := func() []git.StageState {
		var out []git.StageState
		for _, row := range fileTree(files) {
			out = append(out, row.State)
		}
		return out
	}

	if got := states()[0]; got != git.StateUnstaged {
		t.Errorf("with nothing staged the directory is %v, want unstaged", got)
	}

	stage(&files[0])
	if got := states()[0]; got != git.StatePartial {
		t.Errorf("with one of two files staged the directory is %v, want partial", got)
	}

	stage(&files[1])
	if got := states()[0]; got != git.StateStaged {
		t.Errorf("with both files staged the directory is %v, want staged", got)
	}
	if got := states()[3]; got != git.StateUnstaged {
		t.Errorf("the file outside the directory is %v, want unstaged", got)
	}
}

// A directory holding a half-staged file is itself partial: there is still
// something under it to deal with.
func TestFileTreeIsPartialForAHalfStagedFile(t *testing.T) {
	files := filesOf(t, "tui/view.go")
	files[0].Entry.Staged = files[0].Entry.Unstaged
	if got := fileTree(files)[0].State; got != git.StatePartial {
		t.Errorf("the directory of a half-staged file is %v, want partial", got)
	}
}

// stage moves a file's change into the index, the way staging it would.
func stage(f *FileRef) {
	f.Entry.Staged, f.Entry.Unstaged = f.Entry.Unstaged, nil
}

func TestTheFilePaneDrawsTheTree(t *testing.T) {
	m := newModel(t, newFakeBackend(pathSession(t, "internal/tui/view.go", "internal/tui/model.go", "main.go")),
		WithSize(100, 20))

	want := []string{"internal/tui/", "│ view.go", "│ model.go", "main.go"}
	got := paneLines(t, m)[:len(want)]
	for i, line := range want {
		if !strings.Contains(got[i], line) {
			t.Errorf("pane row %d is %q, want it to hold %q", i, got[i], line)
		}
	}
	if strings.Contains(got[1], "internal") {
		t.Errorf("pane row 1 is %q, want the file's name without the directory above it", got[1])
	}
}

// The pane scrolls to the row the marked file is on, which is past its own
// index once the directories above it have rows of their own.
func TestThePaneScrollsToTheMarkedFilesRow(t *testing.T) {
	var paths []string
	for i := range 8 {
		paths = append(paths, fmt.Sprintf("dir%02d/file.txt", i))
	}
	m := newModel(t, newFakeBackend(pathSession(t, paths...)), WithSize(100, 12))

	for range 7 {
		press(t, m, "]")
	}
	file := m.markedFile()
	row := m.paneRowOf(file)
	if row != 2*file+1 {
		t.Fatalf("file %d, one per directory, is on pane row %d, want %d", file, row, 2*file+1)
	}
	if row < m.bodyHeight() {
		t.Fatalf("pane row %d fits the window without scrolling, so the test proves nothing", row)
	}
	if row < m.fileTop || row >= m.fileTop+m.bodyHeight() {
		t.Errorf("the pane window is [%d,%d), which does not hold the marked file's row %d",
			m.fileTop, m.fileTop+m.bodyHeight(), row)
	}
}

// paneLines is the file pane's column of the view, without the diff beside it.
func paneLines(t *testing.T, m *Model) []string {
	t.Helper()
	width := m.filePaneWidth()
	if width == 0 {
		t.Fatal("the file pane is not showing")
	}
	var out []string
	body := strings.Split(m.View(), "\n")[headerHeight : headerHeight+m.bodyHeight()]
	for _, line := range body {
		out = append(out, ansi.Truncate(line, width, ""))
	}
	return out
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
