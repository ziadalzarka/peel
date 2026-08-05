package app

import (
	"context"

	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// version names which copy of a file a note's line numbers count lines in.
//
// A working tree holds three at once — the base commit, the index, and the file
// on disk — and the two diffs peel draws run between them. Which one a number
// counts in follows from the note's origin and side, and getting it wrong
// measures the note against a file it was never written on.
type version struct {
	path string
	// disk is set when the copy is the file in the working tree, which is the
	// only one that is not already an object.
	disk bool
	// index is set when the copy is the one git holds staged.
	index bool
	// rev is the commit the copy is read out of, when it comes from one.
	rev string
}

// versionOf returns the copy c's Line counts lines in.
func versionOf(s *Session, c store.Comment) version {
	switch {
	case c.Origin == store.OriginIndex && c.Side == store.SideOld:
		return version{path: c.File, rev: baseOr(s)}
	case c.Origin == store.OriginIndex:
		return version{path: c.File, index: true}
	case c.Side == store.SideOld:
		// The unstaged diff runs index → working tree, so its old side is what
		// is staged rather than what was committed.
		return version{path: c.File, index: true}
	default:
		return version{path: c.File, disk: true}
	}
}

func baseOr(s *Session) string {
	if s != nil && s.Base != "" {
		return s.Base
	}
	return "HEAD"
}

// Snapshot freezes the file version a note is about to be written against and
// returns the blob holding it, so the note's line number has something that
// cannot move to be a number in.
//
// Only the working tree's copy is written; the index's and a commit's are
// already objects git keeps for its own reasons.
func (a *App) Snapshot(ctx context.Context, s *Session, c store.Comment) (string, error) {
	if c.Line <= 0 {
		return "", nil
	}
	v := versionOf(s, c)
	switch {
	case v.disk:
		return a.Repo.SnapshotFile(ctx, v.path)
	case v.index:
		return a.Repo.IndexBlob(ctx, v.path)
	default:
		return a.Repo.TreeBlob(ctx, v.rev, v.path)
	}
}

// anchorKey identifies one mapping: this file version, measured from this blob.
type anchorKey struct {
	version
	blob string
}

// Relocate moves each note onto the line its code sits on now.
//
// The note recorded the file as it was, so where its line went is a diff between
// that and the file as it is — git's own answer rather than a search for
// something that looks similar. A line the diff has nowhere to map is one that
// was rewritten or deleted out from under the note, and it comes back marked
// outdated instead of placed on whatever moved into its number.
//
// Best effort: a note peel cannot work out is left exactly as stored, which is
// how every note behaved before anchors existed.
func (a *App) Relocate(ctx context.Context, s *Session, comments []store.Comment) []store.Comment {
	// A pull request is not in this working tree, so nothing can have moved
	// under it and there is no local copy to diff against.
	if s == nil || s.PR != nil || len(comments) == 0 {
		return comments
	}

	// Only the files this review is about are worth asking git about. A note on a
	// file that has left the diff — committed, or put back — is an orphan the UI
	// draws nowhere and the agent has nothing to do with, and re-reading it costs
	// two git calls on every refresh in a mode that refreshes continuously.
	shown := map[string]bool{}
	for _, f := range s.Files {
		shown[f.Path] = true
	}

	// One mapping serves every note measured from the same snapshot of the same
	// file, which is the usual shape: several notes, one file, one pass.
	maps := map[anchorKey]git.LineMap{}
	failed := map[anchorKey]bool{}

	out := make([]store.Comment, len(comments))
	copy(out, comments)
	for i := range out {
		c := &out[i]
		if c.Blob == "" || c.Line <= 0 || !shown[c.File] {
			continue
		}
		// Line is measured from the snapshot, so working it out a second time
		// would measure the answer instead of the note and walk the line further
		// on every pass. A note that has already been placed is left alone;
		// picking up a later change means reading the note again, not the result.
		if c.MovedFrom != 0 || c.Outdated {
			continue
		}

		key := anchorKey{version: versionOf(s, *c), blob: c.Blob}
		m, done := maps[key]
		if !done {
			if failed[key] {
				continue
			}
			var err error
			m, err = a.lineMap(ctx, key)
			if err != nil {
				failed[key] = true
				continue
			}
			maps[key] = m
		}

		// Line stays where it was written when the code is gone: there is no
		// current line to name, and the number it was written at is the only
		// true thing left to say about it.
		line, end, ok := relocateRun(m, c.Line, c.EndLine)
		if !ok {
			c.Outdated = true
			continue
		}
		if line != c.Line {
			c.MovedFrom = c.Line
		}
		c.Line = line
		if c.EndLine > 0 {
			c.EndLine = end
		}
	}
	return out
}

// relocateRun moves a note's run onto the file as it is now, and reports false
// when what it covers is no longer what was read.
//
// Every line of the run goes through the mapping, not just its two ends. A run
// is a claim about a continuous stretch of code, so it survives only if that
// stretch survives: a line rewritten inside it has gone the same way a rewritten
// end has, and lines inserted between the ends leave the two numbers still
// findable while the note quietly grows to cover code nobody wrote about.
//
// A note on a single line is a run of one, and asks the one question it always
// asked.
func relocateRun(m git.LineMap, line, end int) (int, int, bool) {
	first, ok := m.Lookup(line)
	if !ok {
		return 0, 0, false
	}
	last := first
	for l := line + 1; l <= end; l++ {
		at, ok := m.Lookup(l)
		if !ok || at != last+1 {
			return 0, 0, false
		}
		last = at
	}
	return first, last, true
}

// lineMap asks git how numbering moved from the note's blob to the file now.
func (a *App) lineMap(ctx context.Context, key anchorKey) (git.LineMap, error) {
	if key.disk {
		return a.Repo.MapLinesOnto(ctx, key.blob, key.path)
	}
	var (
		now string
		err error
	)
	if key.index {
		now, err = a.Repo.IndexBlob(ctx, key.path)
	} else {
		now, err = a.Repo.TreeBlob(ctx, key.rev, key.path)
	}
	if err != nil {
		return git.LineMap{}, err
	}
	return a.Repo.MapLinesBetween(ctx, key.blob, now)
}

// KeepAnchors makes the snapshots peel holds open exactly the ones its stored
// notes name, so an object cannot outlive the note it was taken for.
//
// Every path that writes a comment ends here. What peel costs the repository is
// one blob per file version somebody commented on, and removing the last note
// naming one hands it straight back to git's own collector.
func (a *App) KeepAnchors(ctx context.Context) error {
	all, err := a.Comments.List(store.Filter{})
	if err != nil {
		return err
	}
	blobs := make([]string, 0, len(all))
	for _, c := range all {
		if c.Blob != "" {
			blobs = append(blobs, c.Blob)
		}
	}
	return a.Repo.KeepAnchors(ctx, blobs)
}
