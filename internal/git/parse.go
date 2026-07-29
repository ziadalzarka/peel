package git

import (
	"fmt"
	"strconv"
	"strings"
)

// devNull is the path git uses for the absent side of an add or delete.
const devNull = "/dev/null"

// ParseDiff parses the output of `git diff` in default unified format.
//
// It tolerates the headers git emits for renames, copies, mode changes and
// binary files. Line endings are preserved as they appear: the parser splits on
// "\n" only, so a CRLF file keeps its "\r" as part of the line text and round
// trips byte-for-byte through Patch.
func ParseDiff(out string) (Diff, error) {
	p := &diffParser{lines: splitLines(out)}
	return p.parse()
}

type diffParser struct {
	lines []string
	pos   int
}

// splitLines splits on "\n" and drops the trailing empty element produced by a
// final newline, so that "a\n" yields ["a"] rather than ["a", ""].
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func (p *diffParser) done() bool      { return p.pos >= len(p.lines) }
func (p *diffParser) peek() string    { return p.lines[p.pos] }
func (p *diffParser) advance() string { l := p.lines[p.pos]; p.pos++; return l }

func (p *diffParser) parse() (Diff, error) {
	var d Diff
	for !p.done() {
		if !strings.HasPrefix(p.peek(), "diff --git ") {
			// Skip any preamble (commit headers from `git show`, stray text).
			p.advance()
			continue
		}
		f, err := p.parseFile()
		if err != nil {
			return Diff{}, err
		}
		d.Files = append(d.Files, f)
	}
	return d, nil
}

func (p *diffParser) parseFile() (FileDiff, error) {
	header := p.advance()
	f := FileDiff{Status: StatusModified}

	if old, new, ok := parseDiffGitPaths(header); ok {
		f.OldPath, f.NewPath = old, new
	}

	if err := p.parseFileHeaders(&f); err != nil {
		return FileDiff{}, err
	}

	for !p.done() && strings.HasPrefix(p.peek(), "@@") {
		h, err := p.parseHunk()
		if err != nil {
			return FileDiff{}, fmt.Errorf("%s: %w", f.Path(), err)
		}
		f.Hunks = append(f.Hunks, h)
	}
	return f, nil
}

// parseFileHeaders consumes the extended header lines between `diff --git` and
// the first hunk.
func (p *diffParser) parseFileHeaders(f *FileDiff) error {
	for !p.done() {
		line := p.peek()
		switch {
		case strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "diff --git "):
			return nil

		case strings.HasPrefix(line, "new file mode "):
			f.Status = StatusAdded
			f.NewMode = strings.TrimPrefix(line, "new file mode ")

		case strings.HasPrefix(line, "deleted file mode "):
			f.Status = StatusDeleted
			f.OldMode = strings.TrimPrefix(line, "deleted file mode ")

		case strings.HasPrefix(line, "old mode "):
			f.OldMode = strings.TrimPrefix(line, "old mode ")

		case strings.HasPrefix(line, "new mode "):
			f.NewMode = strings.TrimPrefix(line, "new mode ")

		case strings.HasPrefix(line, "rename from "):
			f.Status = StatusRenamed
			f.OldPath = unquotePath(strings.TrimPrefix(line, "rename from "))

		case strings.HasPrefix(line, "rename to "):
			f.Status = StatusRenamed
			f.NewPath = unquotePath(strings.TrimPrefix(line, "rename to "))

		case strings.HasPrefix(line, "copy from "):
			f.Status = StatusCopied
			f.OldPath = unquotePath(strings.TrimPrefix(line, "copy from "))

		case strings.HasPrefix(line, "copy to "):
			f.Status = StatusCopied
			f.NewPath = unquotePath(strings.TrimPrefix(line, "copy to "))

		case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
			f.IsBinary = true

		case strings.HasPrefix(line, "--- "):
			if path := strings.TrimPrefix(line, "--- "); path != devNull {
				f.OldPath = stripPathPrefix(path)
			} else if f.Status == StatusModified {
				f.Status = StatusAdded
			}

		case strings.HasPrefix(line, "+++ "):
			if path := strings.TrimPrefix(line, "+++ "); path != devNull {
				f.NewPath = stripPathPrefix(path)
			} else if f.Status == StatusModified {
				f.Status = StatusDeleted
			}
		}
		p.advance()
	}
	return nil
}

func (p *diffParser) parseHunk() (Hunk, error) {
	h, err := parseHunkHeader(p.advance())
	if err != nil {
		return Hunk{}, err
	}

	oldLine, newLine := h.OldStart, h.NewStart
	for !p.done() {
		line := p.peek()
		// A hunk body ends at the next hunk or the next file.
		if strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "diff --git ") {
			break
		}

		p.advance()
		switch {
		case line == "":
			// git emits a bare "" for an empty context line rather than " ".
			h.Lines = append(h.Lines, Line{Kind: LineContext, OldLine: oldLine, NewLine: newLine})
			oldLine++
			newLine++

		case line[0] == '+':
			h.Lines = append(h.Lines, Line{Kind: LineAdded, Text: line[1:], NewLine: newLine})
			newLine++

		case line[0] == '-':
			h.Lines = append(h.Lines, Line{Kind: LineRemoved, Text: line[1:], OldLine: oldLine})
			oldLine++

		case line[0] == '\\':
			h.Lines = append(h.Lines, Line{Kind: LineNoNewline, Text: line[1:]})

		case line[0] == ' ':
			h.Lines = append(h.Lines, Line{Kind: LineContext, Text: line[1:], OldLine: oldLine, NewLine: newLine})
			oldLine++
			newLine++

		default:
			return Hunk{}, fmt.Errorf("unrecognized line in hunk body: %q", line)
		}
	}
	return h, nil
}

// parseHunkHeader parses "@@ -10,6 +10,7 @@ optional section".
func parseHunkHeader(line string) (Hunk, error) {
	rest, ok := strings.CutPrefix(line, "@@ ")
	if !ok {
		return Hunk{}, fmt.Errorf("hunk header %q: missing \"@@ \" prefix", line)
	}
	end := strings.Index(rest, " @@")
	if end < 0 {
		return Hunk{}, fmt.Errorf("hunk header %q: missing closing \"@@\"", line)
	}

	ranges := strings.Fields(rest[:end])
	if len(ranges) != 2 {
		return Hunk{}, fmt.Errorf("hunk header %q: want two ranges, got %d", line, len(ranges))
	}
	oldSpec, ok := strings.CutPrefix(ranges[0], "-")
	if !ok {
		return Hunk{}, fmt.Errorf("hunk header %q: old range missing \"-\"", line)
	}
	newSpec, ok := strings.CutPrefix(ranges[1], "+")
	if !ok {
		return Hunk{}, fmt.Errorf("hunk header %q: new range missing \"+\"", line)
	}

	oldStart, oldCount, err := parseRange(oldSpec)
	if err != nil {
		return Hunk{}, fmt.Errorf("hunk header %q: %w", line, err)
	}
	newStart, newCount, err := parseRange(newSpec)
	if err != nil {
		return Hunk{}, fmt.Errorf("hunk header %q: %w", line, err)
	}

	return Hunk{
		OldStart: oldStart,
		OldCount: oldCount,
		NewStart: newStart,
		NewCount: newCount,
		Section:  strings.TrimPrefix(rest[end+3:], " "),
	}, nil
}

// parseDiffGitPaths extracts both paths from a `diff --git a/x b/y` line.
//
// The unquoted form is ambiguous when paths contain spaces, so this is only a
// fallback: the ---/+++ and rename headers are authoritative and overwrite
// whatever is recovered here.
func parseDiffGitPaths(line string) (old, new string, ok bool) {
	rest, ok := strings.CutPrefix(line, "diff --git ")
	if !ok {
		return "", "", false
	}

	if strings.HasPrefix(rest, `"`) {
		oldQuoted, remainder, ok := splitQuoted(rest)
		if !ok {
			return "", "", false
		}
		return stripPathPrefix(oldQuoted), stripPathPrefix(unquoteIfQuoted(strings.TrimSpace(remainder))), true
	}

	// Split at the midpoint: for the common case where both paths are equal,
	// "a/x b/x" divides cleanly at " b/".
	if idx := strings.Index(rest, " b/"); idx >= 0 {
		return stripPathPrefix(rest[:idx]), stripPathPrefix(rest[idx+1:]), true
	}
	fields := strings.Fields(rest)
	if len(fields) != 2 {
		return "", "", false
	}
	return stripPathPrefix(fields[0]), stripPathPrefix(fields[1]), true
}

// splitQuoted consumes a leading C-quoted token, returning it unquoted along
// with the remaining text.
func splitQuoted(s string) (token, rest string, ok bool) {
	if !strings.HasPrefix(s, `"`) {
		return "", s, false
	}
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			return unquotePath(s[:i+1]), s[i+1:], true
		}
	}
	return "", s, false
}

func unquoteIfQuoted(s string) string {
	if strings.HasPrefix(s, `"`) {
		return unquotePath(s)
	}
	return s
}

// stripPathPrefix removes the a/ or b/ prefix git adds to diff paths.
func stripPathPrefix(path string) string {
	path = unquoteIfQuoted(path)
	for _, prefix := range []string{"a/", "b/"} {
		if after, ok := strings.CutPrefix(path, prefix); ok {
			return after
		}
	}
	return path
}

// unquotePath reverses git's C-style quoting of paths containing special
// characters. On malformed input it returns the original string unchanged
// rather than failing the whole parse.
func unquotePath(s string) string {
	if !strings.HasPrefix(s, `"`) || !strings.HasSuffix(s, `"`) || len(s) < 2 {
		return s
	}
	if out, err := strconv.Unquote(s); err == nil {
		return out
	}
	// Git emits octal escapes (\303\251) that Go's Unquote rejects. Decode by
	// hand, treating each escape as a raw byte so UTF-8 reassembles correctly.
	body := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 >= len(body) {
			b.WriteByte(body[i])
			continue
		}
		i++
		switch c := body[i]; c {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '"', '\\':
			b.WriteByte(c)
		default:
			if c >= '0' && c <= '7' && i+2 < len(body) {
				if v, err := strconv.ParseUint(body[i:i+3], 8, 8); err == nil {
					b.WriteByte(byte(v))
					i += 2
					continue
				}
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}
