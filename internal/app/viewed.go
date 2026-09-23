package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

type Viewer struct {
	viewed store.ViewedStore
	files  []git.FileEntry
}

func (a *App) Viewer(s *Session) *Viewer {
	viewed := a.StateFor(s).Viewed
	if s == nil || viewed == nil {
		return nil
	}
	files := make([]git.FileEntry, len(s.Files))
	for i, f := range s.Files {
		files[i] = FileViewed(f, false)
	}
	return &Viewer{viewed: viewed, files: files}
}

func (v *Viewer) Session(s *Session) (*Session, error) {
	keys, err := v.viewed.Load()
	if err != nil {
		return nil, err
	}
	viewed := make(map[string]bool, len(keys))
	for _, key := range keys {
		viewed[key] = true
	}
	out := *s
	out.Files = make([]git.FileEntry, len(v.files))
	for i, f := range v.files {
		out.Files[i] = withViewed(f, viewed)
	}
	return &out, nil
}

func (v *Viewer) StageFile(_ context.Context, path string) error {
	keys, err := v.keysOf(path)
	if err != nil {
		return err
	}
	return v.mark(keys, true)
}

func (v *Viewer) StageHunk(_ context.Context, id git.HunkID) error {
	keys, err := v.keysOf(id.Path)
	if err != nil {
		return err
	}
	f, _ := v.file(id.Path)
	for i, h := range f.Unstaged.Hunks {
		if id.Matches(*f.Unstaged, h) {
			return v.mark(keys[i:i+1], true)
		}
	}
	return fmt.Errorf("%s is not a hunk of this pull request", id)
}

func (v *Viewer) UnstageFile(_ context.Context, path string) error {
	keys, err := v.keysOf(path)
	if err != nil {
		return err
	}
	return v.mark(keys, false)
}

func (v *Viewer) StageAll(context.Context) error { return v.mark(v.allKeys(), true) }

func (v *Viewer) UnstageAll(context.Context) error { return v.mark(v.allKeys(), false) }

func (v *Viewer) IndexTree(context.Context) (string, error) {
	keys, err := v.viewed.Load()
	if err != nil {
		return "", err
	}
	return strings.Join(slices.Sorted(slices.Values(keys)), "\n"), nil
}

func (v *Viewer) RestoreIndex(_ context.Context, from, to string) error {
	return v.viewed.Update(func(viewed map[string]bool) error {
		if listed(viewed) != from {
			return fmt.Errorf("what is marked viewed has changed since that press — mark it again by hand")
		}
		clear(viewed)
		for _, key := range strings.Split(to, "\n") {
			if key != "" {
				viewed[key] = true
			}
		}
		return nil
	})
}

func (v *Viewer) mark(keys []string, on bool) error {
	return v.viewed.Update(func(viewed map[string]bool) error {
		for _, key := range keys {
			if on {
				viewed[key] = true
			} else {
				delete(viewed, key)
			}
		}
		return nil
	})
}

func (v *Viewer) file(path string) (git.FileEntry, bool) {
	for _, f := range v.files {
		if f.Path == path && f.Unstaged != nil {
			return f, true
		}
	}
	return git.FileEntry{}, false
}

func (v *Viewer) keysOf(path string) ([]string, error) {
	f, ok := v.file(path)
	if !ok {
		return nil, fmt.Errorf("%s is not in this pull request", path)
	}
	return viewKeys(*f.Unstaged), nil
}

func (v *Viewer) allKeys() []string {
	var out []string
	for _, f := range v.files {
		if f.Unstaged != nil {
			out = append(out, viewKeys(*f.Unstaged)...)
		}
	}
	return out
}

func listed(viewed map[string]bool) string {
	keys := make([]string, 0, len(viewed))
	for key, on := range viewed {
		if on {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return strings.Join(keys, "\n")
}

func viewKeys(d git.FileDiff) []string {
	if len(d.Hunks) == 0 {
		return []string{viewKey(d.Path(), fmt.Sprintf("%s %s %t %s %s", d.Status, d.OldPath, d.IsBinary, d.OldMode, d.NewMode), 0)}
	}
	seen := map[string]int{}
	keys := make([]string, len(d.Hunks))
	for i, h := range d.Hunks {
		var body strings.Builder
		for _, l := range h.Lines {
			body.WriteByte(l.Kind.Origin())
			body.WriteString(l.Text)
			body.WriteByte('\n')
		}
		keys[i] = viewKey(d.Path(), body.String(), seen[body.String()])
		seen[body.String()]++
	}
	return keys
}

func viewKey(path, body string, nth int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d\x00%s", path, nth, body))
	return path + "@" + hex.EncodeToString(sum[:8])
}

func withViewed(e git.FileEntry, viewed map[string]bool) git.FileEntry {
	d := e.Unstaged
	if d == nil {
		return e
	}
	keys := viewKeys(*d)
	if len(d.Hunks) == 0 {
		if viewed[keys[0]] {
			e.Staged, e.Unstaged = d, nil
		}
		return e
	}
	seen, left := *d, *d
	seen.Hunks, left.Hunks = nil, nil
	for i, h := range d.Hunks {
		if viewed[keys[i]] {
			seen.Hunks = append(seen.Hunks, h)
		} else {
			left.Hunks = append(left.Hunks, h)
		}
	}
	e.Staged, e.Unstaged = nil, nil
	if len(seen.Hunks) > 0 {
		e.Staged = &seen
	}
	if len(left.Hunks) > 0 {
		e.Unstaged = &left
	}
	return e
}

func FileViewed(e git.FileEntry, viewed bool) git.FileEntry {
	var whole *git.FileDiff
	var hunks []git.Hunk
	for _, d := range []*git.FileDiff{e.Staged, e.Unstaged} {
		if d == nil {
			continue
		}
		if whole == nil {
			copied := *d
			whole = &copied
		}
		hunks = append(hunks, d.Hunks...)
	}
	e.Staged, e.Unstaged = nil, nil
	if whole == nil {
		return e
	}
	whole.Hunks = inOrder(hunks)
	if viewed {
		e.Staged = whole
	} else {
		e.Unstaged = whole
	}
	return e
}

func HunkViewed(e git.FileEntry, id git.HunkID) (git.FileEntry, bool) {
	if e.Unstaged == nil {
		return e, false
	}
	at := slices.IndexFunc(e.Unstaged.Hunks, func(h git.Hunk) bool { return id.Matches(*e.Unstaged, h) })
	if at < 0 {
		return e, false
	}
	moved := e.Unstaged.Hunks[at]

	left := *e.Unstaged
	left.Hunks = slices.Delete(slices.Clone(left.Hunks), at, at+1)
	seen := *e.Unstaged
	seen.Hunks = []git.Hunk{moved}
	if e.Staged != nil {
		seen = *e.Staged
		seen.Hunks = inOrder(append(slices.Clone(e.Staged.Hunks), moved))
	}

	e.Staged, e.Unstaged = &seen, nil
	if len(left.Hunks) > 0 {
		e.Unstaged = &left
	}
	return e, true
}

func inOrder(hunks []git.Hunk) []git.Hunk {
	return slices.SortedStableFunc(slices.Values(hunks), func(a, b git.Hunk) int {
		return cmp.Or(cmp.Compare(a.NewStart, b.NewStart), cmp.Compare(a.OldStart, b.OldStart))
	})
}
