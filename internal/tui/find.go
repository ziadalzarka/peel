package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// finder is the file search: the letters typed to name a file, and where the
// choice stands in what they match.
//
// It names files that are already in the diff — the tree on the left, asked for
// by name instead of scrolled to. A review knows which file it is about to look
// at more often than it knows how far down the diff that file is.
type finder struct {
	query string
	// at is the match the choice is on and top the first one drawn. The list has
	// a window of its own, since a diff holds more files than the panel has rows.
	at, top int
}

// findHit is one file the query names: where it is in the document, how well it
// fits, and which letters of its path were matched — so the panel can show why
// the file is in the list.
type findHit struct {
	file  int
	path  string
	score int
	// hits are the positions in the path the query matched, in order.
	hits []int
}

// findRows is the most matches the panel shows at once. Enough that a query
// narrowed to a handful of files shows all of them, few enough that the diff
// being searched is still on screen behind it.
const findRows = 10

// openFind puts the search up with the choice on the file being read, so a
// finder opened and dismissed leaves the reviewer exactly where they were, and
// one opened on purpose starts from where they are rather than from the top of
// the diff.
func (m *Model) openFind() {
	if len(m.doc.Files) == 0 {
		m.status = "no files to go to"
		return
	}
	m.mode = modeFind
	m.find = finder{}
	m.find.at = max(m.hitOf(m.markedFile()), 0)
	m.scrollFind()
	m.status = ""
}

func (m *Model) closeFind() {
	m.mode = modeBrowse
	m.find = finder{}
}

// findKey drives the search: what is typed narrows the list, the arrows move
// the choice through it, and enter goes to the file chosen.
func (m *Model) findKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		m.closeFind()
		return nil
	case "enter":
		m.goToMatch()
		return nil
	case "up":
		m.moveFind(-1)
		return nil
	case "down":
		m.moveFind(1)
		return nil
	case "backspace":
		m.retype(withoutLast(m.find.query))
		return nil
	case "ctrl+u":
		m.retype("")
		return nil
	}
	// A rune with alt held is a chord rather than a letter of a path, and the
	// keys that are not letters at all have nothing to type.
	if len(msg.Runes) > 0 && !msg.Alt {
		m.retype(m.find.query + string(msg.Runes))
	}
	return nil
}

// retype narrows or widens the list, putting the choice back on the best match.
// A letter typed changes what the list is ranked by, so leaving the choice at
// the row it was on would leave it on whichever file has since moved there.
func (m *Model) retype(query string) {
	m.find.query = query
	m.find.at, m.find.top = 0, 0
}

func (m *Model) moveFind(delta int) {
	hits := m.findHits()
	if len(hits) == 0 {
		return
	}
	m.find.at = min(max(m.find.at+delta, 0), len(hits)-1)
	m.scrollFind()
}

// scrollFind moves the panel's window as far as it must to keep the choice in
// it, and no further.
func (m *Model) scrollFind() {
	rows := m.findHeight()
	if m.find.at < m.find.top {
		m.find.top = m.find.at
	}
	if m.find.at >= m.find.top+rows {
		m.find.top = m.find.at - rows + 1
	}
	m.find.top = max(m.find.top, 0)
}

// goToMatch leaves the search on the file chosen, with the window opened on it
// the way `opt`+`↓` opens one.
//
// A file folded away is gone to and left folded: the fold says the reviewer was
// done with it, and a search that quietly opened one would put a diff back on
// screen that had been dealt with. The footer says which key opens it.
func (m *Model) goToMatch() {
	hits := m.findHits()
	if m.find.at >= len(hits) {
		m.closeFind()
		m.status = "no file matches that"
		return
	}
	hit := hits[m.find.at]
	m.closeFind()
	m.showFile(m.doc.topOf(m.doc.RowOfFile(hit.file)))
	m.status = "went to " + hit.path
	if m.collapsed[hit.path] {
		m.status += " — it is folded away; space opens it"
	}
}

// findHits is the files the query names, best fit first.
//
// An empty query names every one of them, in the order the diff reads them, so
// the search opens as the list of what is under review and typing narrows it
// from there.
func (m *Model) findHits() []findHit {
	out := make([]findHit, 0, len(m.doc.Files))
	for i, f := range m.doc.Files {
		path := f.Entry.Path
		score, hits, ok := matchPath(path, m.find.query)
		if !ok {
			continue
		}
		out = append(out, findHit{file: i, path: path, score: score, hits: hits})
	}
	// Ties keep the diff's own order, so a query that says nothing between two
	// files leaves them in the order they are read.
	sort.SliceStable(out, func(a, b int) bool { return out[a].score > out[b].score })
	return out
}

// hitOf is where a file sits in what the query matches, and -1 when the query
// leaves it out.
func (m *Model) hitOf(file int) int {
	for i, hit := range m.findHits() {
		if hit.file == file {
			return i
		}
	}
	return -1
}

// matchPath matches the letters typed against a path and scores how well they
// fit it, the way a file finder does: the letters have to turn up in order, but
// not next to each other, so both `tuiview` and `tvw` reach
// `internal/tui/view.go`.
//
// A letter counts for more where it starts a directory, a file name or a word
// inside one, and for more again where it follows the letter before it — so a
// query that reads like the name of a file beats the same letters scattered
// through a path that happens to hold them. Every place the first letter turns
// up is tried, because the first of them is not always the one that leads to the
// best run: the `t` of `tui` is in `internal` as well, three segments before the
// directory being asked for.
func matchPath(path, query string) (score int, hits []int, ok bool) {
	want := []rune(strings.ToLower(query))
	if len(want) == 0 {
		return 0, nil, true
	}
	runes := []rune(path)
	for start := range runes {
		if unicode.ToLower(runes[start]) != want[0] {
			continue
		}
		if s, h := matchFrom(runes, want, start); h != nil && (!ok || s > score) {
			score, hits, ok = s, h, true
		}
	}
	return score, hits, ok
}

// matchFrom walks the query along the path from one place its first letter
// turns up, taking each letter the first time it comes round again, and scores
// what it took. It gives back nothing at all when the path runs out first.
func matchFrom(runes, want []rune, start int) (int, []int) {
	hits := make([]int, 0, len(want))
	score, at, last := 0, 0, -2
	for i := start; i < len(runes) && at < len(want); i++ {
		if unicode.ToLower(runes[i]) != want[at] {
			continue
		}
		score++
		switch {
		case at == 0:
			// How far into the path the first letter sits is not held against it:
			// every place it turns up is tried, and the run that follows is what
			// says which of them the query meant.
		case i == last+1:
			score += runBonus
		default:
			score -= gapPenalty + (i - last - 1)
		}
		if i == 0 || boundary(runes[i-1]) {
			score += boundaryBonus
		}
		hits = append(hits, i)
		last, at = i, at+1
	}
	if at < len(want) {
		return 0, nil
	}
	return score, hits
}

// boundary reports the characters a new part of a path starts after: the
// separator between directories, and the punctuation names are built out of.
func boundary(r rune) bool { return strings.ContainsRune("/._-", r) }

const (
	// boundaryBonus and runBonus are what put a query reading like the name of a
	// file above the same letters found scattered through a path, and gapPenalty
	// — charged once for a break plus once per letter skipped — is what keeps a
	// path made of the right initials from beating the name itself.
	boundaryBonus = 8
	runBonus      = 6
	gapPenalty    = 3
)

func withoutLast(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return s
	}
	return string(runes[:len(runes)-1])
}

// findHeight is how many matches the panel has room for.
func (m *Model) findHeight() int {
	return max(min(findRows, m.bodyHeight()-findHeadRows), 1)
}

// findHeadRows is what the panel spends before its first match: the rule that
// parts it from the diff, and the row the query is typed on.
const findHeadRows = 2

// findLines draws the search — the query, and the files it names under it.
//
// It is drawn over the foot of the body rather than on a screen of its own, so
// the diff being searched stays in front of the reviewer while they look for
// somewhere else to be.
func (m *Model) findLines(width int) []string {
	hits := m.findHits()
	head := " " + m.theme.Title.Render("go to file") + "  " + m.find.query + m.theme.Cursor.Render("▌")
	count := m.theme.Dim.Render(plural(len(hits), "file")) + " "
	if len(hits) == 0 {
		count = m.theme.Note.Render("nothing matches") + " "
	}

	lines := []string{
		m.theme.Dim.Render(strings.Repeat("─", max(width, 0))),
		spread(head, count, width),
	}
	for i := m.find.top; i < m.find.top+m.findHeight() && i < len(hits); i++ {
		lines = append(lines, m.findRow(hits[i], i == m.find.at, width))
	}
	return lines
}

// findRow draws one file of the search: its state, its path with the letters
// the query matched picked out of it, and the counts the file tree carries.
func (m *Model) findRow(hit findHit, chosen bool, width int) string {
	marker, style := " ", m.theme.Dim
	if chosen {
		marker, style = m.theme.Cursor.Render("▌"), m.theme.FileHead
	}

	entry := m.doc.Files[hit.file].Entry
	added, removed := entry.Stats()
	counts := fmt.Sprintf("+%d -%d", added, removed)
	room := width - filePaneGutter - ansi.StringWidth(counts) - 1

	line := marker + m.renderer.stateSymbol(entry.State()) + " " + m.highlight(hit, style, room)
	return fit(line, width-ansi.StringWidth(counts)-1) + m.theme.Dim.Render(counts) + " "
}

// highlight renders a path with the letters the query matched picked out of it.
// A path too long for the panel is trimmed from the left the way the file tree
// trims one, and loses the highlighting along with the letters that have gone.
func (m *Model) highlight(hit findHit, style lipgloss.Style, room int) string {
	runes := []rune(hit.path)
	if len(runes) > room {
		return style.Render(shorten(hit.path, max(room, 4)))
	}
	var b strings.Builder
	at := 0
	for i, r := range runes {
		if at < len(hit.hits) && hit.hits[at] == i {
			b.WriteString(m.theme.Key.Render(string(r)))
			at++
			continue
		}
		b.WriteString(style.Render(string(r)))
	}
	return b.String()
}

// overlay puts a panel over the foot of the body, so what a search covers is
// the bottom of the diff rather than the whole of it.
func overlay(rows, panel []string) []string {
	if len(panel) > len(rows) {
		panel = panel[:len(rows)]
	}
	copy(rows[len(rows)-len(panel):], panel)
	return rows
}
