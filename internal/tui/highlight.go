package tui

import (
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Highlighter colours diff line content by language.
//
// Lines are lexed one at a time rather than whole files: peel only ever holds
// the diff, not the surrounding source. That misreads constructs spanning
// several lines, such as block comments, which is the same trade every diff
// pager makes.
type Highlighter struct {
	formatter chroma.Formatter
	style     *chroma.Style

	mu     sync.Mutex
	lexers map[string]chroma.Lexer
}

// NewHighlighter returns a highlighter, or nil when highlighting is off.
func NewHighlighter() *Highlighter {
	formatter := formatters.Get("terminal256")
	style := styles.Get("github-dark")
	if formatter == nil || style == nil {
		return nil
	}
	return &Highlighter{
		formatter: formatter,
		style:     style,
		lexers:    map[string]chroma.Lexer{},
	}
}

// Active reports whether highlighting will do anything.
func (h *Highlighter) Active() bool { return h != nil }

// Line returns text with ANSI colour applied, or text unchanged when the
// language is unknown or lexing fails.
func (h *Highlighter) Line(path, text string) string {
	if h == nil || strings.TrimSpace(text) == "" {
		return text
	}
	lexer := h.lexerFor(path)
	if lexer == nil {
		return text
	}
	iterator, err := lexer.Tokenise(nil, text)
	if err != nil {
		return text
	}
	var b strings.Builder
	if err := h.formatter.Format(&b, h.style, iterator); err != nil {
		return text
	}
	// A trailing newline would break the caller's single-line layout.
	return strings.TrimRight(b.String(), "\n")
}

// lexerFor resolves and caches a lexer per file extension, since matching by
// filename is not free and a diff revisits the same file many times.
func (h *Highlighter) lexerFor(path string) chroma.Lexer {
	key := extensionOf(path)

	h.mu.Lock()
	defer h.mu.Unlock()
	if lexer, ok := h.lexers[key]; ok {
		return lexer
	}

	lexer := lexers.Match(path)
	if lexer != nil {
		lexer = chroma.Coalesce(lexer)
	}
	h.lexers[key] = lexer
	return lexer
}

// extensionOf returns the cache key for a path: its extension, or the base name
// when there is none, so Makefile and Dockerfile still cache.
func extensionOf(path string) string {
	base := path
	if i := strings.LastIndexAny(path, "/\\"); i >= 0 {
		base = path[i+1:]
	}
	if i := strings.LastIndex(base, "."); i > 0 {
		return base[i:]
	}
	return base
}
