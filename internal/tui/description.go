package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/git"
)

const descriptionPath = "description.md"

const descriptionIndent = 3

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

var hangingPrefix = regexp.MustCompile(`^[ \t]*(?:(?:[-*+]|\d+[.)])[ \t]+(?:\[[ xX]\][ \t]+)?|>[ \t]?)?`)

func descriptionLines(body string) []git.Line {
	body = htmlComment.ReplaceAllString(strings.ReplaceAll(body, "\r\n", "\n"), "")
	var out []git.Line
	blank := true
	for _, text := range strings.Split(body, "\n") {
		text = strings.TrimRight(text, " \t")
		if text == "" && blank {
			continue
		}
		blank = text == ""
		out = append(out, git.Line{Kind: git.LineContext, Text: text})
	}
	for len(out) > 0 && out[len(out)-1].Text == "" {
		out = out[:len(out)-1]
	}
	return out
}

func (d *Document) addDescription(pr forge.PullRequest, folded bool) {
	lines := descriptionLines(pr.Body)
	if len(lines) == 0 {
		return
	}
	ref := &DescriptionRef{
		Lines:    lines,
		Base:     pr.BaseRef,
		Head:     pr.HeadRef,
		Row:      len(d.Rows),
		Folded:   folded,
		markdown: markdownHunkOf(descriptionPath, lines),
	}
	d.Description = ref
	row := Row{Kind: RowDescription, File: -1, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: -1, Expand: -1}
	d.add(row)
	if !folded {
		md := ref.markdown
		for i, l := range lines {
			table := md.tableOf(i)
			if table >= 0 && md.tableOf(i-1) != table {
				d.add(Row{Kind: RowDescriptionEdge, File: -1, Hunk: -1, Left: i, Right: -1, Step: -1, Side: -1, Expand: -1, Head: true})
			}
			for _, piece := range d.descriptionPieces(md, i, l.Text) {
				d.add(Row{Kind: RowDescriptionText, File: -1, Hunk: -1, Left: i, Right: -1, Step: -1, Side: -1, Expand: -1, Text: piece})
			}
			if table >= 0 && md.tableOf(i+1) != table {
				d.add(Row{Kind: RowDescriptionEdge, File: -1, Hunk: -1, Left: i, Right: -1, Step: -1, Side: -1, Expand: -1})
			}
		}
	}
	row.Kind = RowBlank
	d.add(row)
}

func (d Document) descriptionPieces(md *markdownHunk, i int, raw string) []string {
	text := md.textAt(i, raw)
	at := md.at(i)
	width := d.pane - 1 - descriptionIndent
	if at.code || at.marker || at.table >= 0 || text == "" || d.pane <= 0 {
		return []string{text}
	}
	prefix := hangingPrefix.FindString(text)
	body := text[len(prefix):]
	if body == "" {
		return []string{text}
	}
	hang := strings.Repeat(" ", ansi.StringWidth(prefix))
	if quote := strings.TrimLeft(prefix, " \t"); strings.HasPrefix(quote, ">") {
		hang = prefix
	}
	pieces := strings.Split(ansi.Wrap(body, max(width-len(hang), minCommentWidth), " -"), "\n")
	for i := range pieces {
		if i == 0 {
			pieces[i] = prefix + pieces[i]
		} else {
			pieces[i] = hang + pieces[i]
		}
	}
	return pieces
}

func (d Document) inDescription(row int) bool {
	if row < 0 || row >= len(d.Rows) {
		return false
	}
	switch d.Rows[row].Kind {
	case RowDescription, RowDescriptionText, RowDescriptionEdge:
		return true
	}
	return false
}

func (d Document) RowOfDescription(line int) int {
	if d.Description == nil {
		return -1
	}
	if line >= 0 {
		for i, r := range d.Rows {
			if r.Kind == RowDescriptionText && r.Left == line {
				return i
			}
		}
	}
	return d.Description.Row
}

func (r *Renderer) description(d Document, row Row, st RowState) string {
	ref := d.Description
	arrow := "▾"
	if ref.Folded {
		arrow = "▸"
	}
	title := r.theme.Header.Render("description")
	if st.Cursor {
		title = r.theme.Cursor.Render("description")
	}
	head := r.marker(st) + " " + r.theme.Dim.Render(arrow) + " " + title
	if ref.Base != "" && ref.Head != "" {
		head += "  " + r.theme.Dim.Render(ref.Base+" ← "+ref.Head)
	}
	return r.rule(head)
}

func (r *Renderer) descriptionText(d Document, row Row, st RowState) string {
	indent := r.marker(st) + strings.Repeat(" ", descriptionIndent)
	if row.Text == "" {
		return r.fit(indent)
	}
	at := d.Description.markdown.at(row.Left)
	var text string
	switch {
	case !r.syntax.Active():
		text = r.theme.Context.Render(row.Text)
	case at.code:
		text = r.syntax.Code(at.lang, row.Text)
	default:
		body := strings.TrimLeft(row.Text, " ")
		text = row.Text[:len(row.Text)-len(body)] + r.syntax.Line(descriptionPath, body)
	}
	return r.fit(indent + text)
}

func (r *Renderer) descriptionEdge(d Document, row Row) string {
	block, ok := d.Description.markdown.tableAt(row.Left)
	if !ok {
		return r.fit("")
	}
	edge := tableFoot(block.widths)
	if row.Head {
		edge = tableTop(block.widths)
	}
	return r.fit(" " + strings.Repeat(" ", descriptionIndent) + r.syntax.Line(descriptionPath, edge))
}
