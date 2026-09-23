package tui

import (
	"context"
	"fmt"
	"slices"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ziadalzarka/peel/internal/store"
)

const remembered = 100

type recorded struct {
	undo, redo func(context.Context) error
}

type entry struct {
	what        string
	back, forth func(*Model)

	mu      sync.Mutex
	inverse recorded
	held    any
	ready   bool
	dead    bool
}

func drawn(what string, back, forth func(*Model)) *entry {
	return &entry{what: what, back: back, forth: forth, ready: true}
}

func written(what string, back, forth func(*Model)) *entry {
	return &entry{what: what, back: back, forth: forth}
}

func (e *entry) wrote(r recorded) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r.undo == nil {
		e.dead = true
		return
	}
	e.inverse, e.ready = r, true
}

func (e *entry) carry(v any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.held = v
}

func (e *entry) carried() any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.held
}

func (e *entry) failed() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dead = true
}

func (e *entry) taken() (inverse recorded, ready, dead bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inverse, e.ready, e.dead
}

type history struct {
	done   []*entry
	undone []*entry
}

func (h *history) add(e *entry) {
	h.done = append(h.done, e)
	if len(h.done) > remembered {
		h.done = h.done[len(h.done)-remembered:]
	}
	h.undone = nil
}

func (h *history) forget(e *entry) {
	h.done = dropEntry(h.done, e)
	h.undone = dropEntry(h.undone, e)
}

func dropEntry(list []*entry, e *entry) []*entry {
	for i, got := range list {
		if got == e {
			return append(list[:i:i], list[i+1:]...)
		}
	}
	return list
}

func (m *Model) undoLast() tea.Cmd {
	e, inverse, ok := take(&m.hist.done, m, "undo")
	if !ok {
		return nil
	}
	m.hist.undone = append(m.hist.undone, e)
	return m.stepBack(e, e.back, inverse.undo, "undone: "+e.what, &m.hist.undone, &m.hist.done)
}

func (m *Model) redoLast() tea.Cmd {
	e, inverse, ok := take(&m.hist.undone, m, "redo")
	if !ok {
		return nil
	}
	m.hist.done = append(m.hist.done, e)
	return m.stepBack(e, e.forth, inverse.redo, "redone: "+e.what, &m.hist.done, &m.hist.undone)
}

func take(stack *[]*entry, m *Model, doing string) (*entry, recorded, bool) {
	for len(*stack) > 0 {
		e := (*stack)[len(*stack)-1]
		*stack = (*stack)[:len(*stack)-1]
		inverse, ready, dead := e.taken()
		switch {
		case dead:
		case ready:
			return e, inverse, true
		default:
			*stack = append(*stack, e)
			m.status = "still writing " + e.what + " — try that again in a moment"
			return nil, recorded{}, false
		}
	}
	m.status = "nothing to " + doing
	return nil, recorded{}, false
}

func (m *Model) stepBack(e *entry, draw func(*Model), write func(context.Context) error, say string, from, to *[]*entry) tea.Cmd {
	if write == nil {
		draw(m)
		m.status = say
		return nil
	}
	return m.reapply(func() {
		draw(m)
		m.status = say
	}, write, func() {
		*from = dropEntry(*from, e)
		*to = append(*to, e)
	})
}

func indexWrite(backend Backend, op func(context.Context) error) writeOp {
	return func(ctx context.Context) (recorded, error) {
		before, err := backend.IndexTree(ctx)
		if err != nil {
			return recorded{}, op(ctx)
		}
		if err := op(ctx); err != nil {
			return recorded{}, err
		}
		after, err := backend.IndexTree(ctx)
		if err != nil || after == before {
			return recorded{}, nil
		}
		return recorded{
			undo: func(ctx context.Context) error { return backend.RestoreIndex(ctx, after, before) },
			redo: func(ctx context.Context) error { return backend.RestoreIndex(ctx, before, after) },
		}, nil
	}
}

func storedComments(backend Backend, ids ...string) ([]store.Comment, error) {
	all, err := backend.StoredComments()
	if err != nil {
		return nil, err
	}
	out := make([]store.Comment, 0, len(ids))
	for _, c := range all {
		if slices.Contains(ids, c.ID) {
			out = append(out, c)
		}
	}
	if len(out) != len(ids) {
		return nil, fmt.Errorf("the store has no note by that name any more")
	}
	return out, nil
}

func writeBack(backend Backend, notes ...store.Comment) func(context.Context) error {
	return func(ctx context.Context) error {
		for _, c := range notes {
			if _, err := backend.AddComment(ctx, c); err != nil {
				return err
			}
		}
		return nil
	}
}

func rewrite(backend Backend, id, expect, want string) func(context.Context) error {
	return func(ctx context.Context) error {
		stored, err := storedComments(backend, id)
		if err != nil {
			return err
		}
		if stored[0].Body != expect {
			return fmt.Errorf("that note has been rewritten since — reload and change it by hand")
		}
		return backend.EditComment(id, want)
	}
}

func fileFold(path string, collapsed, staged bool, v viewport) func(*Model) {
	return func(m *Model) {
		m.setCollapsed(map[string]bool{path: collapsed})
		setFlag(m.stagedFolds, path, staged, staged)
		m.rebuild()
		m.putViewport(v)
	}
}

func descriptionFold(folded bool, v viewport) func(*Model) {
	return func(m *Model) {
		m.setDescriptionFolded(folded)
		m.putViewport(v)
	}
}

func sideFold(path string, folded, had bool, v viewport) func(*Model) {
	return func(m *Model) {
		setFlag(m.sideFolds, path, folded, had)
		m.rebuild()
		m.putViewport(v)
	}
}

func stepFold(step int, folded, had bool, v viewport) func(*Model) {
	return func(m *Model) {
		setFlag(m.walkFolded, step, folded, had)
		m.rebuild()
		m.putViewport(v)
	}
}

func commentFold(id string, folded, had bool, v viewport) func(*Model) {
	return func(m *Model) {
		setFlag(m.commentFolds, id, folded, had)
		m.rebuild()
		m.putViewport(v)
	}
}

func setFlag[K comparable](into map[K]bool, key K, value, held bool) {
	if held {
		into[key] = value
		return
	}
	delete(into, key)
}
