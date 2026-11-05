package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

const (
	jumpPollEvery = 200 * time.Millisecond
	jumpMaxAge    = 30 * time.Second
)

type jumpMsg struct{ store.Jump }

func withJumps() Option { return func(o *options) { o.jumps = true } }

func (m *Model) jumpCmd() tea.Cmd {
	backend, ctx, seen := m.backend, m.ctx, m.jumpSeen
	return func() tea.Msg {
		for {
			if jump, err := backend.PendingJump(); err == nil && jump.At.After(seen) {
				return jumpMsg{jump}
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(jumpPollEvery):
			}
		}
	}
}

func (m *Model) applyJump(msg jumpMsg) tea.Cmd {
	m.jumpSeen = msg.At
	m.goTo(msg.File, msg.Line)
	return m.jumpCmd()
}

func (m *Model) goTo(path string, line int) {
	entry, ok := m.session.Entry(path)
	if !ok {
		m.status = path + " is not in this diff"
		return
	}
	m.setCollapsed(map[string]bool{path: false})
	m.openRunsOver(path, line)
	m.rebuild()

	row, exact := m.doc.rowOfNewLine(path, line)
	if !exact && entry.State() == git.StatePartial {
		m.sideFolds[path] = false
		m.rebuild()
		row, exact = m.doc.rowOfNewLine(path, line)
	}
	if row < 0 {
		row = m.doc.rowOfPath(path)
	}
	m.showRow(row)
	m.status = "went to " + placeName(path, line)
}

func (m *Model) openRunsOver(path string, line int) {
	for _, r := range m.runsInForce() {
		if r.File != path {
			continue
		}
		if r.WholeFile() || (line >= r.Line && line <= r.LastLine()) {
			m.openRuns[r.ID] = true
		}
	}
}

func (m *Model) showRow(row int) {
	if row < 0 || row >= m.doc.Len() {
		return
	}
	m.cursor = row
	if height := m.bodyHeight(); row < m.top || row >= m.top+height {
		m.top = row - height/3
	}
	m.clampTop()
	m.ensureFileVisible()
}

func (d Document) rowOfNewLine(path string, line int) (int, bool) {
	best, bestDistance := -1, 0
	if line <= 0 {
		return best, false
	}
	for i, r := range d.Rows {
		if r.Kind != RowLine || r.Hunk < 0 || d.Hunks[r.Hunk].Path != path {
			continue
		}
		lines := d.Hunks[r.Hunk].Hunk.Lines
		for _, at := range []int{r.Left, r.Right} {
			if at < 0 || at >= len(lines) || lines[at].NewLine <= 0 {
				continue
			}
			distance := lines[at].NewLine - line
			if distance < 0 {
				distance = -distance
			}
			if best < 0 || distance < bestDistance {
				best, bestDistance = i, distance
			}
		}
	}
	return best, best >= 0 && bestDistance == 0
}

func (d Document) rowOfPath(path string) int {
	for i, f := range d.Files {
		if f.Entry.Path == path {
			return d.RowOfFile(i)
		}
	}
	return -1
}

func placeName(path string, line int) string {
	if line <= 0 {
		return path
	}
	return fmt.Sprintf("%s:%d", path, line)
}
