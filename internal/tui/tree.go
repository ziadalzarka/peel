package tui

import (
	"strings"

	"github.com/ziadalzarka/peel/internal/git"
)

// paneRow is one line of the file pane: a changed file, or a directory holding
// some of them.
type paneRow struct {
	// Name labels the row: a file's base name, or the directory segments the
	// row stands for.
	Name string
	// Path is the file's path, or the path of the directory down to and
	// including this row.
	Path string
	// Depth is how many directories the row sits under.
	Depth int
	// File indexes into Document.Files, and is -1 on a directory row.
	File int
	// State is the file's staging state, or how far through a directory's files
	// staging has got.
	State git.StageState
}

// fileTree lays the changed files out under the directories they live in, so
// the pane says where a file is and not only what it is called.
//
// Rows come out in the order the document holds the files, and a directory
// takes the position of the first file inside it — read top to bottom, the pane
// still reads the diff top to bottom. A directory holding nothing but one more
// directory is joined onto it, `internal/tui` rather than two rows and an
// indent, since the pane is narrow and a level with one way down says nothing.
func fileTree(files []FileRef) []paneRow {
	parts := make([][]string, len(files))
	group := make([]int, len(files))
	for i, f := range files {
		parts[i] = strings.Split(f.Entry.Path, "/")
		group[i] = i
	}
	return branch(files, parts, group, 0, 0)
}

// node is one entry of a directory while the tree is being built: a file, or a
// directory and the files somewhere below it.
type node struct {
	name string
	// file indexes into Document.Files, and is -1 on a directory.
	file int
	// files are the files below a directory, in the document's order.
	files []int
}

// branch lays out one directory: the files that live directly in it and the
// directories under it, each followed by its own contents, all in the order
// they first appear.
func branch(files []FileRef, parts [][]string, group []int, seg, depth int) []paneRow {
	var order []node
	at := map[string]int{}
	for _, fi := range group {
		if len(parts[fi]) == seg+1 {
			order = append(order, node{name: parts[fi][seg], file: fi})
			continue
		}
		name := parts[fi][seg]
		i, ok := at[name]
		if !ok {
			i = len(order)
			at[name] = i
			order = append(order, node{name: name, file: -1})
		}
		order[i].files = append(order[i].files, fi)
	}

	var rows []paneRow
	for _, n := range order {
		if n.file >= 0 {
			entry := files[n.file].Entry
			rows = append(rows, paneRow{
				Name:  n.name,
				Path:  entry.Path,
				Depth: depth,
				File:  n.file,
				State: entry.State(),
			})
			continue
		}
		name, next := n.name, seg+1
		for onlyBelow(parts, n.files, next) {
			name += "/" + parts[n.files[0]][next]
			next++
		}
		rows = append(rows, paneRow{
			Name:  name,
			Path:  strings.Join(parts[n.files[0]][:next], "/"),
			Depth: depth,
			File:  -1,
			State: groupState(files, n.files),
		})
		rows = append(rows, branch(files, parts, n.files, next, depth+1)...)
	}
	return rows
}

// onlyBelow reports that a directory holds no changed file of its own and only
// one directory below it, which is what makes the two worth joining into a row.
func onlyBelow(parts [][]string, group []int, seg int) bool {
	for _, fi := range group {
		if len(parts[fi]) <= seg+1 {
			return false
		}
		if parts[fi][seg] != parts[group[0]][seg] {
			return false
		}
	}
	return len(group) > 0
}

// groupState is how far through a directory's files staging has got: staged
// once every one of them is, partial from the first until the last.
func groupState(files []FileRef, group []int) git.StageState {
	staged, unstaged := 0, 0
	for _, fi := range group {
		switch files[fi].Entry.State() {
		case git.StateStaged:
			staged++
		case git.StateUnstaged:
			unstaged++
		default:
			return git.StatePartial
		}
	}
	switch {
	case staged == 0:
		return git.StateUnstaged
	case unstaged == 0:
		return git.StateStaged
	default:
		return git.StatePartial
	}
}
