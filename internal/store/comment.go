// Package store persists review comments and cached walkthroughs under the
// repository's git directory, where they survive restarts and stay invisible to
// `git status`.
package store

import (
	"fmt"
	"strings"
	"time"
)

// Side names which version of a file a comment is anchored to.
type Side string

const (
	// SideNew anchors to the changed file — the usual case.
	SideNew Side = "new"
	// SideOld anchors to the pre-change file, for commenting on a deletion.
	SideOld Side = "old"
)

// Valid reports whether s is a recognised side.
func (s Side) Valid() bool { return s == SideNew || s == SideOld }

// Origin names which of the two diffs a comment's line number was read from.
//
// A working tree has two — HEAD→index and index→worktree — and a file can carry
// both at once. Line 12 is a different line in each, so a note that does not say
// which one it was written on lands on whatever happens to sit at that number in
// the other.
type Origin string

const (
	// OriginIndex anchors to the staged change: the file as the index holds it.
	OriginIndex Origin = "index"
	// OriginWorktree anchors to the unstaged change: the file on disk.
	OriginWorktree Origin = "worktree"
)

// Valid reports whether o is a recognised origin.
func (o Origin) Valid() bool { return o == OriginIndex || o == OriginWorktree }

// Author distinguishes notes the user wrote from notes an agent left.
type Author string

const (
	// AuthorUser marks a comment written by the person reviewing.
	AuthorUser Author = "user"
	// AuthorAgent marks a comment written by Claude Code or another tool.
	AuthorAgent Author = "agent"
)

// Valid reports whether a is a recognised author.
func (a Author) Valid() bool { return a == AuthorUser || a == AuthorAgent }

// Comment is one inline review note.
type Comment struct {
	ID   string `json:"id"`
	File string `json:"file"`
	// Line is the 1-based line number on Side. Zero means the comment applies
	// to the file as a whole rather than a specific line.
	Line int `json:"line"`
	// EndLine is the last line of the run the note was written on, when it was
	// written on more than one. Zero means the note is about Line alone.
	//
	// A run is one continuous stretch of one side of one hunk, which is what
	// makes the pair a range rather than two numbers: everything between them is
	// code the note is about.
	EndLine int  `json:"endLine,omitempty"`
	Side    Side `json:"side"`
	// Origin is which of the two diffs Line was read from. Empty means the note
	// predates the distinction, or came from a caller that did not make it, and
	// is placed on whichever side claims it first.
	Origin Origin `json:"origin,omitempty"`
	Body   string `json:"body"`
	// Hunk optionally records the hunk the comment was written against, so the
	// TUI can still show it after line numbers move.
	Hunk string `json:"hunk,omitempty"`
	// Blob is the git object holding the exact file content Line counts lines
	// in: the version that was on screen when the note was written.
	//
	// A line number alone does not survive the file changing, and peel reviews a
	// working tree an editor and an agent both write to while the review is
	// open. The file has no commit of its own to name, so peel makes one thing
	// that cannot move — the content, frozen in git's object store — and the
	// number becomes a number *in that*. Where it is now is then a diff between
	// two immutable blobs, which is git's own answer rather than a guess.
	//
	// Empty on a note written before peel recorded one, which still lands by its
	// number alone.
	Blob      string    `json:"blob,omitempty"`
	Author    Author    `json:"author"`
	Resolved  bool      `json:"resolved"`
	CreatedAt time.Time `json:"createdAt"`
	// MovedFrom is where the note was written, kept when Line has been moved on to
	// where that code sits now. Zero when it has not moved. Worked out on read
	// and never stored.
	MovedFrom int `json:"-"`
	// Outdated reports that the code the note was written on is not in the file
	// any more — rewritten or deleted out from under it. Worked out on read and
	// never stored: a note is current again the moment its code comes back.
	Outdated bool `json:"-"`
	// Target scopes the comment to what was being reviewed: empty for the
	// working tree, or a pull request reference such as "github:cli/cli#123".
	Target string `json:"target,omitempty"`
}

// Validate reports whether the comment is well-formed enough to store.
func (c Comment) Validate() error {
	if strings.TrimSpace(c.File) == "" {
		return fmt.Errorf("comment: file is required")
	}
	if strings.TrimSpace(c.Body) == "" {
		return fmt.Errorf("comment: body is required")
	}
	if c.Line < 0 {
		return fmt.Errorf("comment: line %d is negative", c.Line)
	}
	if c.EndLine != 0 {
		if c.Line <= 0 {
			return fmt.Errorf("comment: end line %d with no line to run from", c.EndLine)
		}
		if c.EndLine < c.Line {
			return fmt.Errorf("comment: end line %d is before line %d", c.EndLine, c.Line)
		}
	}
	if c.Side != "" && !c.Side.Valid() {
		return fmt.Errorf("comment: unknown side %q", c.Side)
	}
	if c.Origin != "" && !c.Origin.Valid() {
		return fmt.Errorf("comment: unknown origin %q", c.Origin)
	}
	if c.Author != "" && !c.Author.Valid() {
		return fmt.Errorf("comment: unknown author %q", c.Author)
	}
	return nil
}

// Location renders the comment's anchor for display, e.g. "src/main.go:42", or
// "src/main.go:42-48" for a note written on a run of lines.
func (c Comment) Location() string {
	if c.Line == 0 {
		return c.File
	}
	if c.EndLine > c.Line {
		return fmt.Sprintf("%s:%d-%d", c.File, c.Line, c.EndLine)
	}
	return fmt.Sprintf("%s:%d", c.File, c.Line)
}

// Filter narrows which comments an operation applies to. A zero Filter matches
// everything.
type Filter struct {
	// File matches one path exactly.
	File string
	// Target matches the reviewed target. Use TargetFilter to distinguish
	// "any target" from "the working tree".
	Target string
	// MatchTarget makes Target significant, including when it is empty.
	MatchTarget bool
	// Unresolved restricts results to comments not yet resolved.
	Unresolved bool
	// Author restricts results to one author.
	Author Author
}

// Matches reports whether c satisfies the filter.
func (f Filter) Matches(c Comment) bool {
	if f.File != "" && c.File != f.File {
		return false
	}
	if f.MatchTarget && c.Target != f.Target {
		return false
	}
	if f.Unresolved && c.Resolved {
		return false
	}
	if f.Author != "" && c.Author != f.Author {
		return false
	}
	return true
}

// What follows is what a comment store does, written against a slice rather
// than against a file: the repository's own store and the file holding one pull
// request's review both hold a list of comments and both answer the same
// questions of it, so the answers live here and each store only has to say how
// its list is read and written.

// filterComments picks out the comments matching f, oldest first.
func filterComments(all []Comment, f Filter) []Comment {
	out := make([]Comment, 0, len(all))
	for _, c := range all {
		if f.Matches(c) {
			out = append(out, c)
		}
	}
	sortComments(out)
	return out
}

// findComment returns the comment with the given ID.
func findComment(all []Comment, id string) (Comment, error) {
	for _, c := range all {
		if c.ID == id {
			return c, nil
		}
	}
	return Comment{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// prepareComment fills in what every stored comment carries and the caller
// usually leaves out: which side it is anchored to, who wrote it, and when.
func prepareComment(c Comment, p params) (Comment, error) {
	if err := c.Validate(); err != nil {
		return Comment{}, err
	}
	if c.Side == "" {
		c.Side = SideNew
	}
	if c.Author == "" {
		c.Author = AuthorUser
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = p.now().UTC()
	}
	return c, nil
}

// addComment appends c, giving it an ID no comment already there holds.
func addComment(all []Comment, c Comment, p params) ([]Comment, Comment, error) {
	if c.ID == "" {
		c.ID = uniqueID(all, p.newID)
	}
	for _, existing := range all {
		if existing.ID == c.ID {
			return nil, Comment{}, fmt.Errorf("comment id %s already exists", c.ID)
		}
	}
	return append(all, c), c, nil
}

// updateComment applies apply to one comment and returns the list holding the
// result.
func updateComment(all []Comment, id string, apply func(*Comment)) ([]Comment, Comment, error) {
	for i := range all {
		if all[i].ID != id {
			continue
		}
		candidate := all[i]
		apply(&candidate)
		// The ID is the store's to control, not the caller's.
		candidate.ID = id
		if err := candidate.Validate(); err != nil {
			return nil, Comment{}, err
		}
		all[i] = candidate
		return all, candidate, nil
	}
	return nil, Comment{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// removeComment drops one comment by ID.
func removeComment(all []Comment, id string) ([]Comment, error) {
	for i, c := range all {
		if c.ID == id {
			return append(all[:i:i], all[i+1:]...), nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// clearComments drops every comment matching f, and reports how many went.
func clearComments(all []Comment, f Filter) ([]Comment, int) {
	kept := make([]Comment, 0, len(all))
	removed := 0
	for _, c := range all {
		if f.Matches(c) {
			removed++
			continue
		}
		kept = append(kept, c)
	}
	return kept, removed
}

// uniqueID returns an ID not already present in all.
func uniqueID(all []Comment, newID func() string) string {
	taken := make(map[string]bool, len(all))
	for _, c := range all {
		taken[c.ID] = true
	}
	for range 100 {
		if id := newID(); !taken[id] {
			return id
		}
	}
	// Deterministic fallback so a poor generator cannot loop forever.
	return fmt.Sprintf("c%d", len(all)+1)
}

// CommentStore persists review comments.
//
// The interface exists so the CLI, the TUI and any future frontend all depend
// on the behaviour rather than on the JSON file underneath it.
type CommentStore interface {
	// List returns comments matching the filter, oldest first.
	List(f Filter) ([]Comment, error)
	// Add stores a new comment, assigning its ID and timestamp.
	Add(c Comment) (Comment, error)
	// Update applies mutate to the comment with the given ID.
	Update(id string, mutate func(*Comment)) (Comment, error)
	// Remove deletes one comment by ID.
	Remove(id string) error
	// Clear deletes every comment matching the filter and reports how many
	// were removed.
	Clear(f Filter) (int, error)
}
