package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/git"
)

const (
	tableBar  = "│"
	tableDash = "─"

	tableTopLeft  = "┌"
	tableTopJoin  = "┬"
	tableTopRight = "┐"

	tableLeft  = "├"
	tableCross = "┼"
	tableRight = "┤"

	tableFootLeft  = "└"
	tableFootJoin  = "┴"
	tableFootRight = "┘"
)

type cellAlign uint8

const (
	alignLeft cellAlign = iota
	alignCenter
	alignRight
)

type markdownLine struct {
	text   string
	lang   string
	code   bool
	marker bool
	table  int
}

type tableBlock struct {
	widths []int
	old    bool
	new    bool
}

type markdownHunk struct {
	lines  []markdownLine
	tables []tableBlock
}

func markdownHunkOf(path string, lines []git.Line) *markdownHunk {
	if !isMarkdown(path) {
		return nil
	}
	m := &markdownHunk{lines: make([]markdownLine, len(lines))}
	for i := range m.lines {
		m.lines[i].table = -1
	}
	m.readCodeBlocks(lines)
	m.readTables(lines)
	return m
}

func (m *markdownHunk) at(i int) markdownLine {
	if m == nil || i < 0 || i >= len(m.lines) {
		return markdownLine{table: -1}
	}
	return m.lines[i]
}

func (m *markdownHunk) tableOf(i int) int {
	return m.at(i).table
}

func (m *markdownHunk) tableAt(i int) (tableBlock, bool) {
	at := m.tableOf(i)
	if at < 0 || at >= len(m.tables) {
		return tableBlock{}, false
	}
	return m.tables[at], true
}

func (m *markdownHunk) textAt(i int, raw string) string {
	if line := m.at(i); line.text != "" {
		return line.text
	}
	return expandTabs(raw)
}

func (m *markdownHunk) readCodeBlocks(lines []git.Line) {
	for _, side := range []git.LineKind{git.LineRemoved, git.LineAdded} {
		var open bool
		var char byte
		var count int
		var lang string
		for i, l := range lines {
			if l.Kind != git.LineContext && l.Kind != side {
				continue
			}
			c, n, info, isMarker := blockMarker(l.Text)
			switch {
			case !open && isMarker:
				open, char, count, lang = true, c, n, blockLang(info)
				m.lines[i].marker = true
				m.lines[i].code = false
				m.lines[i].lang = ""
			case open && isMarker && c == char && n >= count && info == "":
				open, lang = false, ""
				m.lines[i].marker = true
				m.lines[i].code = false
				m.lines[i].lang = ""
			case open:
				m.lines[i].code = true
				m.lines[i].lang = lang
			default:
				m.lines[i].code = false
				m.lines[i].lang = ""
			}
		}
	}
}

func blockMarker(s string) (char byte, count int, info string, ok bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) || (s[i] != '`' && s[i] != '~') {
		return 0, 0, "", false
	}
	char = s[i]
	for i+count < len(s) && s[i+count] == char {
		count++
	}
	if count < 3 {
		return 0, 0, "", false
	}
	info = strings.TrimSpace(s[i+count:])
	if char == '`' && strings.Contains(info, "`") {
		return 0, 0, "", false
	}
	return char, count, info, true
}

func blockLang(info string) string {
	info = strings.TrimLeft(info, "{.")
	if i := strings.IndexAny(info, " \t,;}"); i >= 0 {
		info = info[:i]
	}
	return strings.ToLower(info)
}

func (m *markdownHunk) readTables(lines []git.Line) {
	for start := 0; start < len(lines); start++ {
		end := start
		for end < len(lines) && m.inTable(lines, end) {
			end++
		}
		if end-start >= 2 {
			m.layOutTable(lines, start, end)
		}
		if end > start {
			start = end - 1
		}
	}
}

func (m *markdownHunk) inTable(lines []git.Line, i int) bool {
	l := lines[i]
	if l.Kind != git.LineContext && l.Kind != git.LineAdded && l.Kind != git.LineRemoved {
		return false
	}
	if m.lines[i].code || m.lines[i].marker {
		return false
	}
	return strings.Contains(l.Text, "|") && strings.TrimSpace(l.Text) != ""
}

func (m *markdownHunk) layOutTable(lines []git.Line, start, end int) {
	rows := make([][]string, 0, end-start)
	rules := make([]bool, 0, end-start)
	aligns := []cellAlign(nil)
	seen := false
	for i := start; i < end; i++ {
		cells := splitTableRow(expandTabs(lines[i].Text))
		rule := isDelimiterRow(cells)
		if rule {
			seen = true
			aligns = alignmentsOf(cells)
		}
		rows = append(rows, cells)
		rules = append(rules, rule)
	}
	if !seen {
		return
	}

	var widths []int
	for i, cells := range rows {
		if rules[i] {
			continue
		}
		for j, cell := range cells {
			for len(widths) <= j {
				widths = append(widths, 1)
			}
			widths[j] = max(widths[j], ansi.StringWidth(cell))
		}
	}
	if len(widths) == 0 {
		return
	}

	block := tableBlock{widths: widths}
	rule := tableRule(widths)
	at := len(m.tables)
	for i, cells := range rows {
		switch lines[start+i].Kind {
		case git.LineAdded:
			block.new = true
		case git.LineRemoved:
			block.old = true
		default:
			block.old, block.new = true, true
		}
		m.lines[start+i].table = at
		if rules[i] {
			m.lines[start+i].text = rule
			continue
		}
		m.lines[start+i].text = tableRow(cells, widths, aligns)
	}
	m.tables = append(m.tables, block)
}

func splitTableRow(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}

	var cells []string
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '|':
			b.WriteString(`\|`)
			i++
		case s[i] == '|':
			cells = append(cells, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteByte(s[i])
		}
	}
	return append(cells, strings.TrimSpace(b.String()))
}

func isDelimiterRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		c = strings.TrimPrefix(strings.TrimSuffix(c, ":"), ":")
		if c == "" || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

func alignmentsOf(cells []string) []cellAlign {
	out := make([]cellAlign, len(cells))
	for i, c := range cells {
		left, right := strings.HasPrefix(c, ":"), strings.HasSuffix(c, ":")
		switch {
		case left && right:
			out[i] = alignCenter
		case right:
			out[i] = alignRight
		default:
			out[i] = alignLeft
		}
	}
	return out
}

func tableRule(widths []int) string {
	return tableBorder(widths, tableLeft, tableCross, tableRight)
}

func tableTop(widths []int) string {
	return tableBorder(widths, tableTopLeft, tableTopJoin, tableTopRight)
}

func tableFoot(widths []int) string {
	return tableBorder(widths, tableFootLeft, tableFootJoin, tableFootRight)
}

func tableBorder(widths []int, left, join, right string) string {
	var b strings.Builder
	b.WriteString(left)
	for i, w := range widths {
		if i > 0 {
			b.WriteString(join)
		}
		b.WriteString(strings.Repeat(tableDash, w+2))
	}
	b.WriteString(right)
	return b.String()
}

func tableRow(cells []string, widths []int, aligns []cellAlign) string {
	var b strings.Builder
	b.WriteString(tableBar)
	for i, w := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		align := alignLeft
		if i < len(aligns) {
			align = aligns[i]
		}
		b.WriteString(" " + padCell(cell, w, align) + " " + tableBar)
	}
	return b.String()
}

func padCell(s string, w int, a cellAlign) string {
	gap := w - ansi.StringWidth(s)
	if gap <= 0 {
		return s
	}
	switch a {
	case alignRight:
		return strings.Repeat(" ", gap) + s
	case alignCenter:
		left := gap / 2
		return strings.Repeat(" ", left) + s + strings.Repeat(" ", gap-left)
	default:
		return s + strings.Repeat(" ", gap)
	}
}
