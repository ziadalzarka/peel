package git

// Saying what git will say, before git has been asked.
//
// peel draws a staging decision on the keypress and reads the truth back behind
// it, so it has to know what the two diffs will hold once a change has moved. A
// whole file needs no arithmetic — its changes swap sides entire. One hunk out of
// several needs what is here: the hunk leaves the working tree's diff, arrives in
// the index's, and everything the move renumbers is renumbered.
//
// This is the same arithmetic the patch builder does, read the other way, and it
// is display only. The read-back behind the change is what the reviewer ends up
// looking at; being wrong here costs a redraw, never an index.

// WithHunkStaged returns the file as it will read once one of the hunks still out
// of the index has gone into it, and false when the hunk is not one of those —
// which is the screen being older than the tree, and nothing to guess about.
//
// Both sides move. The working tree's diff loses the hunk, and the hunks under it
// are measured from an index that has just grown or shrunk by what the hunk
// carried. The index's diff gains it, moved back by however much the changes
// already staged above it grew the file, since that diff is numbered against
// HEAD and the hunk arrived numbered against the index.
//
// Where the hunk lands against one already staged rather than clear of it, git
// prints the two as one hunk and this prints them as two: the same index, drawn a
// row differently for as long as it takes the read-back to land.
func (e FileEntry) WithHunkStaged(id HunkID) (FileEntry, bool) {
	if e.Unstaged == nil || e.Untracked {
		return e, false
	}
	work := *e.Unstaged

	at := -1
	for i, h := range work.Hunks {
		if id.Matches(work, h) {
			at = i
			break
		}
	}
	if at < 0 {
		return e, false
	}
	moved := work.Hunks[at]
	delta := moved.NewCount - moved.OldCount

	left := make([]Hunk, 0, len(work.Hunks)-1)
	for i, h := range work.Hunks {
		switch {
		case i == at:
			continue
		case i > at:
			// Below the change that has just landed in the index, so every line it
			// is measured from has moved by what the change carried.
			h = shiftHunk(h, delta, 0)
		}
		left = append(left, h)
	}

	out := e
	if len(left) == 0 {
		out.Unstaged = nil
	} else {
		work.Hunks = left
		out.Unstaged = &work
	}
	out.Staged = stagedWith(e.Staged, *e.Unstaged, moved)
	return out, true
}

// stagedWith returns the index's diff once one hunk of the working tree's has
// landed in it. from is the diff the hunk came out of, for the paths and the
// status a file staged for the first time takes.
func stagedWith(staged *FileDiff, from FileDiff, h Hunk) *FileDiff {
	out := from
	out.Hunks = nil
	if staged != nil {
		out = *staged
	}

	first := firstLine(h.OldStart, h.OldCount)
	delta := h.NewCount - h.OldCount

	// above is how much the changes already staged ahead of this one have grown
	// the index, which is the distance between where the hunk is numbered from
	// and where HEAD has it.
	above := 0
	hunks := make([]Hunk, 0, len(out.Hunks)+1)
	landed := false
	for _, s := range out.Hunks {
		if newSideEnd(s) <= first {
			above += s.NewCount - s.OldCount
			hunks = append(hunks, s)
			continue
		}
		if !landed {
			hunks = append(hunks, hunkInIndex(h, first, above))
			landed = true
		}
		hunks = append(hunks, shiftHunk(s, 0, delta))
	}
	if !landed {
		hunks = append(hunks, hunkInIndex(h, first, above))
	}

	out.Hunks = hunks
	return &out
}

// hunkInIndex is one hunk of the working tree's diff as the index's diff holds
// it: the same body against HEAD on one side and the index it has just changed on
// the other.
func hunkInIndex(h Hunk, first, above int) Hunk {
	return renumber(Hunk{
		OldStart: lineStart(first-above, h.OldCount),
		OldCount: h.OldCount,
		NewStart: lineStart(first, h.NewCount),
		NewCount: h.NewCount,
		Section:  h.Section,
		Lines:    h.Lines,
	})
}

// firstLine is the line a side of a hunk actually starts at. A side with no lines
// is written as the line it comes after, which is why creating a file reads
// `@@ -0,0 +1,2 @@`, so it begins one line further down than it is written.
func firstLine(start, count int) int {
	if count == 0 {
		return start + 1
	}
	return start
}

// lineStart is firstLine backwards: how a side beginning at first is written.
func lineStart(first, count int) int {
	if count == 0 {
		return first - 1
	}
	return first
}

// newSideEnd is the line a hunk's new side stops before, so a hunk that ends
// above a line is one that cannot have moved it.
func newSideEnd(h Hunk) int {
	return firstLine(h.NewStart, h.NewCount) + h.NewCount
}

// shiftHunk moves a hunk's sides by whole lines and renumbers its body to match.
func shiftHunk(h Hunk, oldBy, newBy int) Hunk {
	h.OldStart += oldBy
	h.NewStart += newBy
	return renumber(h)
}

// renumber recomputes a body's per-line numbers from the header above it, the
// way the parser numbers them when it reads a hunk in. A hunk moved to other
// coordinates carries line numbers that would otherwise still name where it was
// — and those numbers are what a note is anchored to.
func renumber(h Hunk) Hunk {
	lines := make([]Line, len(h.Lines))
	old, new := h.OldStart, h.NewStart
	for i, l := range h.Lines {
		switch l.Kind {
		case LineContext:
			l.OldLine, l.NewLine = old, new
			old++
			new++
		case LineRemoved:
			l.OldLine, l.NewLine = old, 0
			old++
		case LineAdded:
			l.OldLine, l.NewLine = 0, new
			new++
		case LineNoNewline:
			l.OldLine, l.NewLine = 0, 0
		}
		lines[i] = l
	}
	h.Lines = lines
	return h
}
