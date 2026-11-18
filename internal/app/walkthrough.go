package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

func (a *App) SetWalkthrough(s *Session, author store.Author, steps []store.WalkthroughStep) error {
	if len(steps) == 0 {
		return fmt.Errorf("a walkthrough needs at least one step")
	}
	for i, step := range steps {
		if strings.TrimSpace(step.Title) == "" || strings.ContainsAny(step.Title, "\r\n") || strings.TrimSpace(step.Text) == "" {
			return fmt.Errorf("step %d needs a single-line title and text", i+1)
		}
		entry, ok := s.Entry(step.File)
		if !ok || !hasWalkthroughLine(entry.Primary(), step.Line) {
			return fmt.Errorf("step %d: %s:%d is not a line on the new side of this diff", i+1, step.File, step.Line)
		}
	}
	return a.authoredWalkthroughStore(s).Walkthroughs().Save(store.Walkthrough{
		Target: s.Target, Fingerprint: s.WalkthroughFingerprint(), Author: author, Written: steps,
	})
}

func (a *App) ClearWalkthrough(s *Session, author store.Author) error {
	return a.authoredWalkthroughStore(s).ClearWalkthrough(author)
}

func (a *App) authoredWalkthroughStore(s *Session) *store.ReviewStore {
	path := filepath.Join(a.StateDir, "authored-walkthrough.json")
	if s.PR != nil {
		path = strings.TrimSuffix(a.ReviewPath(s.Target), ".json") + ".walkthrough.json"
	} else if s.Base != "" {
		path = filepath.Join(a.StateDir, "walkthroughs", s.Base+".json")
	}
	return store.NewReviewStore(path, s.Target, a.storeOpts...)
}

func (s *Session) WalkthroughFingerprint() string {
	var diff strings.Builder
	diff.WriteString(s.DiffText)
	for _, entry := range s.Files {
		if !entry.Untracked || entry.Unstaged == nil {
			continue
		}
		fmt.Fprintf(&diff, "\x00%s\x00", entry.Path)
		for _, hunk := range entry.Unstaged.Hunks {
			for _, line := range hunk.Lines {
				fmt.Fprintln(&diff, line.Render())
			}
		}
	}
	return store.Fingerprint(diff.String())
}

func hasWalkthroughLine(diff *git.FileDiff, number int) bool {
	if diff == nil || number <= 0 {
		return false
	}
	for _, hunk := range diff.Hunks {
		for _, line := range hunk.Lines {
			if line.NewLine == number {
				return true
			}
		}
	}
	return false
}
