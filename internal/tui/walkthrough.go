package tui

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// Groups is a walkthrough laid over the changeset it describes.
//
// It is not a view of its own. The steps reorder the diff's files into the
// order the narrative reads them and put each step's explanation in front of
// the files it covers, so what the reviewer scrolls through is the same diff
// with the walkthrough's notes in it.
type Groups struct {
	// Steps are the narrative's groups, in reading order.
	Steps []store.Step
	// Folded hides a step's explanation, by its index in Document.Steps.
	Folded map[int]bool
	// Width is the room an explanation is wrapped to.
	Width int
}

// WithGroups lays the diff out in the walkthrough's reading order, with each
// step's explanation above the files it covers.
func WithGroups(g Groups) BuildOption { return func(c *buildConfig) { c.groups = g } }

// leftoverTitle heads the group holding whatever the narrative never placed.
const leftoverTitle = "Not grouped"

// leftoverBody says why that group exists, so a reviewer does not read it as a
// step the model wrote.
const leftoverBody = "The walkthrough did not place these files in a step."

// fileGroup is one run of files the document shows together.
type fileGroup struct {
	// step is the walkthrough group introducing the files, or nil when there is
	// no walkthrough and the diff is simply the diff.
	step *store.Step
	// files indexes into the session's file list, in display order.
	files []int
}

// groupFiles lays the changed files out in the order the walkthrough reads them.
//
// Every changed file ends up in a group: the ones the narrative placed sit
// under their step, and anything it missed is collected into a final group
// rather than quietly dropped, since a reviewer trusting the walkthrough as a
// map of the change would otherwise never be told about them. A step that names
// a path outside the changeset, or one an earlier step already took, contributes
// nothing — so every file is shown exactly once.
func groupFiles(files []git.FileEntry, steps []store.Step) []fileGroup {
	if len(steps) == 0 {
		all := make([]int, len(files))
		for i := range all {
			all[i] = i
		}
		return []fileGroup{{files: all}}
	}

	placed := make([]bool, len(files))
	out := make([]fileGroup, 0, len(steps)+1)
	for i := range steps {
		group := fileGroup{step: &steps[i]}
		for _, path := range steps[i].Files {
			fi := fileIndex(files, path)
			if fi < 0 || placed[fi] {
				continue
			}
			placed[fi] = true
			group.files = append(group.files, fi)
		}
		out = append(out, group)
	}

	var missing []int
	for i := range files {
		if !placed[i] {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		out = append(out, fileGroup{
			step:  &store.Step{Title: leftoverTitle, Body: leftoverBody},
			files: missing,
		})
	}
	return out
}

// fileIndex looks a walkthrough's path up in the changed files.
//
// A provider writes the paths it saw in the diff, so an exact match is the
// normal case; the fallbacks catch a path quoted with a leading directory
// trimmed or added, which is not worth losing a file's place in the reading
// order over.
func fileIndex(files []git.FileEntry, path string) int {
	path = strings.TrimSpace(strings.Trim(path, "/"))
	if path == "" {
		return -1
	}
	for i := range files {
		if files[i].Path == path {
			return i
		}
	}

	found := -1
	for i := range files {
		if !strings.HasSuffix(files[i].Path, "/"+path) && !strings.HasSuffix(path, "/"+files[i].Path) {
			continue
		}
		if found >= 0 {
			return -1
		}
		found = i
	}
	return found
}

// wrapBody folds an explanation to the given width, keeping its paragraphs
// apart and stripping the markdown emphasis that only reads as noise here.
func wrapBody(body string, width int) []string {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}

	var out []string
	for _, para := range strings.Split(body, "\n\n") {
		para = strings.TrimSpace(collapseSpace(stripEmphasis(para)))
		if para == "" {
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, strings.Split(ansi.Wrap(para, max(width, 20), " -"), "\n")...)
	}
	return out
}

// italic matches a single-asterisk emphasis span.
var italic = regexp.MustCompile(`\*([^*\n]+)\*`)

// stripEmphasis removes markdown bold and italic markers. Backticks are left
// alone: the renderer styles what is inside them, and they read as identifier
// quotes even when it cannot.
func stripEmphasis(s string) string {
	return italic.ReplaceAllString(strings.NewReplacer("**", "", "__", "").Replace(s), "$1")
}

// collapseSpace joins a paragraph's own line breaks so wrapping controls the
// line length, while leaving list items on their own lines.
func collapseSpace(para string) string {
	lines := strings.Split(para, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if len(out) > 0 && !isListItem(l) && !isListItem(out[len(out)-1]) {
			out[len(out)-1] += " " + l
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func isListItem(l string) bool {
	if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") {
		return true
	}
	digits := 0
	for digits < len(l) && l[digits] >= '0' && l[digits] <= '9' {
		digits++
	}
	return digits > 0 && digits < len(l) && (l[digits] == '.' || l[digits] == ')')
}

// stepIndent is the column a step's explanation starts at: far enough in to sit
// under the heading's text rather than under its number.
const stepIndent = 7

func (r *Renderer) step(d Document, row Row, st RowState) string {
	step := d.Steps[row.Step]

	arrow := "▾"
	if step.Folded {
		arrow = "▸"
	}
	label := "  "
	if step.Number > 0 {
		label = pad(strconv.Itoa(step.Number), 2)
	}
	title := step.Title
	if title == "" {
		title = "Walkthrough"
	}

	styled := r.theme.Header.Render(title)
	if st.Cursor {
		styled = r.theme.Cursor.Render(title)
	}
	return r.fit(r.marker(st) + " " + r.theme.Title.Render(label) + " " +
		r.theme.Dim.Render(arrow) + " " + styled)
}

func (r *Renderer) stepText(row Row) string {
	if row.Text == "" {
		return r.fit("")
	}
	return r.fit(strings.Repeat(" ", stepIndent) + r.inline(row.Text))
}

// inline styles the `code spans` in a line of prose. An unpaired backtick is
// left as it is, since wrapping can split a span across two lines and half a
// styled span is worse than none.
func (r *Renderer) inline(text string) string {
	if strings.Count(text, "`") < 2 {
		return r.theme.Context.Render(text)
	}
	parts := strings.Split(text, "`")
	var b strings.Builder
	for i, part := range parts {
		if i%2 == 1 && i < len(parts)-1 {
			b.WriteString(r.theme.HunkHead.Render(part))
			continue
		}
		if i%2 == 1 {
			b.WriteString(r.theme.Context.Render("`" + part))
			continue
		}
		b.WriteString(r.theme.Context.Render(part))
	}
	return b.String()
}

// StepSummary describes the walkthrough on screen, for the header.
func (d Document) StepSummary() string {
	numbered := 0
	for _, s := range d.Steps {
		if s.Number > 0 {
			numbered++
		}
	}
	if numbered == 0 {
		return plural(len(d.Steps), "section")
	}
	return plural(numbered, "step")
}
