package store

import "strings"

func (s *ReviewStore) Import(remote []Comment) error {
	return s.mutate(func(f *reviewFile) error {
		if f.Imported == nil {
			f.Imported = map[string]bool{}
		}
		seen := make(map[string]bool, len(remote))
		for _, c := range remote {
			seen[c.ID] = true
			wasResolved, imported := f.Imported[c.ID]
			f.Imported[c.ID] = c.Resolved
			if imported {
				if wasResolved != c.Resolved {
					setResolved(f.Comments, c.ID, c.Resolved)
				}
				continue
			}
			if postedFromHere(f.Comments, c) {
				continue
			}
			c.Target = s.target
			prepared, err := prepareComment(c, s.params)
			if err != nil {
				continue
			}
			if next, _, err := addComment(f.Comments, prepared, s.params); err == nil {
				f.Comments = next
			}
		}
		for id := range f.Imported {
			if !seen[id] {
				delete(f.Imported, id)
				f.Comments = withoutRemote(f.Comments, id)
			}
		}
		return nil
	})
}

func setResolved(all []Comment, id string, resolved bool) {
	for i := range all {
		if all[i].ID == id {
			all[i].Resolved = resolved
		}
	}
}

func postedFromHere(all []Comment, remote Comment) bool {
	last := max(remote.Line, remote.EndLine)
	for _, c := range all {
		if c.Remote != "" || c.File != remote.File || sideOf(c) != sideOf(remote) {
			continue
		}
		if strings.TrimSpace(c.Body) != strings.TrimSpace(remote.Body) {
			continue
		}
		if last >= c.Line && last <= max(c.Line, c.EndLine) {
			return true
		}
	}
	return false
}

func sideOf(c Comment) Side {
	if c.Side == "" {
		return SideNew
	}
	return c.Side
}

func withoutRemote(all []Comment, id string) []Comment {
	kept := all[:0]
	for _, c := range all {
		if c.ID == id && c.Remote != "" {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}
