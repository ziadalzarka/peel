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
	Line int    `json:"line"`
	Side Side   `json:"side"`
	Body string `json:"body"`
	// Hunk optionally records the hunk the comment was written against, so the
	// TUI can still show it after line numbers move.
	Hunk      string    `json:"hunk,omitempty"`
	Author    Author    `json:"author"`
	Resolved  bool      `json:"resolved"`
	CreatedAt time.Time `json:"createdAt"`
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
	if c.Side != "" && !c.Side.Valid() {
		return fmt.Errorf("comment: unknown side %q", c.Side)
	}
	if c.Author != "" && !c.Author.Valid() {
		return fmt.Errorf("comment: unknown author %q", c.Author)
	}
	return nil
}

// Location renders the comment's anchor for display, e.g. "src/main.go:42".
func (c Comment) Location() string {
	if c.Line == 0 {
		return c.File
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
