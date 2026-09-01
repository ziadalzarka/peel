package app

import (
	"context"
	"runtime"
	"sort"
	"sync"

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

// versionOf returns the copy a note on this half of this file counts lines in.
func versionOf(s *Session, file string, origin store.Origin, side store.Side) version {
	switch {
	case origin == store.OriginIndex && side == store.SideOld:
		return version{path: file, rev: baseOr(s)}
	case origin == store.OriginIndex:
		return version{path: file, index: true}
	case side == store.SideOld:
		// The unstaged diff runs index → working tree, so its old side is what
		// is staged rather than what was committed.
		return version{path: file, index: true}
	default:
		return version{path: file, disk: true}
	}
}

// homeOf names the half of the file a note is on now, given what each half's
// copy holds at this moment.
//
// Staging a file moves its change out of the working tree and into the index,
// and unstaging moves it back. Neither rewrites a line: the same code is read
// from the other side of the index afterwards, under the other half's numbering.
// A note left on the half it was written on is then measured against a copy that
// no longer holds it, and comes back outdated for the one thing that cannot
// outdate a note — which is the bug this exists to stop.
//
// What says a note has been carried across is that its half is no longer drawn
// while the other one is, which is exactly what staging a whole file does and
// what nothing else does. A note whose own half is still on screen has not moved
// however the file has changed under it: the line it was written on going away
// is an edit, and an edit is what outdated is for.
//
// The content is asked only to stop a move that would lose the note. A half that
// was never drawn — an agent naming the index on a file nobody staged — is not
// somewhere a note came from, and sending it to a copy that does not hold it
// when its own still does would rewrite an anchor rather than follow one.
//
// A note naming no origin predates the distinction and goes where it always
// went.
func homeOf(e git.FileEntry, c store.Comment, mine, other string) store.Origin {
	if c.Origin == "" {
		return c.Origin
	}
	away := across(c.Origin)
	if drawn(e, c.Origin) || !drawn(e, away) {
		return c.Origin
	}
	if mine == c.Blob && other != c.Blob {
		return c.Origin
	}
	return away
}

// drawn reports that the review still shows this half of the file.
func drawn(e git.FileEntry, o store.Origin) bool {
	if o == store.OriginIndex {
		return e.Staged != nil
	}
	return e.Unstaged != nil
}

// across is the half on the other side of the index.
func across(o store.Origin) store.Origin {
	if o == store.OriginIndex {
		return store.OriginWorktree
	}
	return store.OriginIndex
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
//
// A pull request has no anchor to take: the code is the host's rather than this
// checkout's, nothing here can move under the note, and there may not be a
// repository to write an object into at all.
func (a *App) Snapshot(ctx context.Context, s *Session, c store.Comment) (string, error) {
	if c.Line <= 0 || !a.HasRepo() || (s != nil && s.PR != nil) {
		return "", nil
	}
	v := versionOf(s, c.File, c.Origin, c.Side)
	switch {
	case v.disk:
		return a.Repo.SnapshotFile(ctx, v.path)
	case v.index:
		return a.Repo.IndexBlob(ctx, v.path)
	default:
		return a.Repo.TreeBlob(ctx, v.rev, v.path)
	}
}

// halves are the two copies a note might be measured against: the one its origin
// names, and the one on the other side of the index. on marks a note there is
// anything to work out for at all.
type halves struct {
	on          bool
	mine, other version
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
// Which copy of the file that diff runs against is the note's half of it, which
// is not always the half it was written on: staging carries a change across the
// index without touching a line of it, and homeOf follows.
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
	// file that has left the diff — committed, or put back — is drawn under a
	// header of its own rather than on a line, so there is no line to move it
	// onto; working one out anyway would cost two git calls on every refresh in a
	// mode that refreshes continuously, to place a note that hangs off no line.
	shown := make(map[string]git.FileEntry, len(s.Files))
	for _, f := range s.Files {
		shown[f.Path] = f
	}

	out := make([]store.Comment, len(comments))
	copy(out, comments)

	// Both halves of every note's file are named before git is asked anything,
	// so the whole review costs the two calls that resolve them rather than a
	// pair of calls per note.
	sides := make([]halves, len(out))
	versions := map[version]bool{}
	for i := range out {
		c := &out[i]
		if _, ok := shown[c.File]; !ok || c.Blob == "" || c.Line <= 0 {
			continue
		}
		// Line is measured from the snapshot, so working it out a second time
		// would measure the answer instead of the note and walk the line further
		// on every pass. A note that has already been placed is left alone;
		// picking up a later change means reading the note again, not the result.
		if c.MovedFrom != 0 || c.Outdated {
			continue
		}
		h := halves{on: true, mine: versionOf(s, c.File, c.Origin, c.Side)}
		versions[h.mine] = true
		if c.Origin != "" {
			h.other = versionOf(s, c.File, across(c.Origin), c.Side)
			versions[h.other] = true
		}
		sides[i] = h
	}

	now := a.currentBlobs(ctx, versions)

	// A note with no key is one there is nothing to work out for.
	keys := make([]anchorKey, len(out))
	for i := range out {
		c := &out[i]
		if !sides[i].on {
			continue
		}
		c.Origin = homeOf(shown[c.File], *c, now[sides[i].mine], now[sides[i].other])
		keys[i] = anchorKey{version: versionOf(s, c.File, c.Origin, c.Side), blob: c.Blob}
	}

	maps := a.lineMaps(ctx, keys, now)
	for i := range out {
		c := &out[i]
		if keys[i].blob == "" {
			continue
		}
		m, ok := maps[keys[i]]
		if !ok {
			continue
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

// lineMaps answers, for every snapshot some note is measured from, how numbering
// has moved since. A snapshot peel cannot work out is left out, and the notes
// measured from it are left as stored.
//
// One mapping serves every note taken from the same snapshot of the same file,
// which is the usual shape: several notes, one file, one pass. Beyond that, two
// things keep this off the critical path of a review that re-reads continuously.
//
// The copy each note names is resolved for all of them at once, so notes on
// forty files ask two questions rather than forty. And a copy that still holds
// exactly the content its note was written on has not moved by definition, so it
// is answered without a diff — which is every note on every re-read where
// nothing has changed, and every note carried across the index by staging. What
// is left is one diff per snapshot that really did move, and those run together.
func (a *App) lineMaps(ctx context.Context, keys []anchorKey, now map[version]string) map[anchorKey]git.LineMap {
	want := map[anchorKey]bool{}
	for _, k := range keys {
		if k.blob != "" {
			want[k] = true
		}
	}
	if len(want) == 0 {
		return nil
	}

	out := make(map[anchorKey]git.LineMap, len(want))
	todo := make([]anchorKey, 0, len(want))
	for k := range want {
		if at, ok := now[k.version]; ok && at == k.blob {
			out[k] = git.LineMap{}
			continue
		}
		todo = append(todo, k)
	}
	if len(todo) == 0 {
		return out
	}

	got := make([]git.LineMap, len(todo))
	read := make([]bool, len(todo))
	slots := make(chan struct{}, min(len(todo), max(runtime.NumCPU(), 4)))
	var wg sync.WaitGroup
	for i, k := range todo {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			m, err := a.lineMap(ctx, k, now[k.version])
			if err != nil {
				return
			}
			got[i], read[i] = m, true
		}()
	}
	wg.Wait()

	for i, k := range todo {
		if read[i] {
			out[k] = got[i]
		}
	}
	return out
}

// currentBlobs is what each copy a note names holds right now: one call for
// every copy git already has as an object, and one for every file on disk.
//
// A copy git cannot produce — a file the index does not hold, one deleted since
// the note was written — is left out, and the note measured against it falls
// through to a diff that will say the same thing.
func (a *App) currentBlobs(ctx context.Context, want map[version]bool) map[version]string {
	specs := map[string]version{}
	var paths []string
	for v := range want {
		switch {
		case v.disk:
			paths = append(paths, v.path)
		case v.index:
			specs[":"+v.path] = v
		default:
			specs[v.rev+":"+v.path] = v
		}
	}

	out := make(map[version]string, len(want))
	if len(specs) > 0 {
		ask := make([]string, 0, len(specs))
		for spec := range specs {
			ask = append(ask, spec)
		}
		sort.Strings(ask)
		if blobs, err := a.Repo.Blobs(ctx, ask); err == nil {
			for spec, blob := range blobs {
				out[specs[spec]] = blob
			}
		}
	}
	if blobs, err := a.Repo.HashFiles(ctx, paths); err == nil {
		for path, blob := range blobs {
			out[version{path: path, disk: true}] = blob
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
//
// now is what that copy holds, when it has already been resolved; empty means
// it has not, and this asks for it.
func (a *App) lineMap(ctx context.Context, key anchorKey, now string) (git.LineMap, error) {
	if key.disk {
		return a.Repo.MapLinesOnto(ctx, key.blob, key.path)
	}
	if now == "" {
		var err error
		if key.index {
			now, err = a.Repo.IndexBlob(ctx, key.path)
		} else {
			now, err = a.Repo.TreeBlob(ctx, key.rev, key.path)
		}
		if err != nil {
			return git.LineMap{}, err
		}
	}
	return a.Repo.MapLinesBetween(ctx, key.blob, now)
}

// KeepAnchors makes the snapshots peel holds open exactly the ones its stored
// notes name, so an object cannot outlive the note it was taken for.
//
// Every path that writes a comment ends here. What peel costs the repository is
// one blob per file version somebody commented on, and removing the last note
// naming one hands it straight back to git's own collector.
//
// Only this repository's own notes are counted. A pull request's are filed
// outside it and anchored to nothing here, so an object of theirs is not one
// this repository is holding open.
func (a *App) KeepAnchors(ctx context.Context) error {
	if !a.HasRepo() {
		return nil
	}
	all, err := a.Local.Comments.List(store.Filter{})
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
