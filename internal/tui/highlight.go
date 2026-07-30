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
// A frame redraws every visible row, and the wheel can ask for frames faster
// than chroma can lex them: a screenful costs milliseconds, which backs the
// event queue up until the review stops answering the keyboard. The text of a
// diff does not change under us, so a line is lexed once and kept.
type Highlighter struct {
	formatter chroma.Formatter
	style     *chroma.Style

	mu     sync.Mutex
	lexers map[string]chroma.Lexer
	lines  map[lineKey]string
}

// lineKey identifies a highlighted line by the lexer that coloured it rather
// than by path, so the same text in two files of one language is lexed once.
type lineKey struct{ lang, text string }

// maxHighlightCache bounds what a very large diff can hold on to. Past it the
// cache is dropped whole: the window is a screenful, so it refills at once, and
// the alternative is tracking recency on every row of every frame.
const maxHighlightCache = 50000

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
		lines:     map[lineKey]string{},
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
	lang := extensionOf(path)
	lexer := h.lexerFor(lang, path)
	if lexer == nil {
		return text
	}
	key := lineKey{lang: lang, text: text}
	if cached, ok := h.cached(key); ok {
		return cached
	}

	out := text
	if iterator, err := lexer.Tokenise(nil, text); err == nil {
		var b strings.Builder
		if err := h.formatter.Format(&b, h.style, iterator); err == nil {
			// A trailing newline would break the caller's single-line layout.
			out = strings.TrimRight(b.String(), "\n")
		}
	}
	h.remember(key, out)
	return out
}

func (h *Highlighter) cached(key lineKey) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out, ok := h.lines[key]
	return out, ok
}

func (h *Highlighter) remember(key lineKey, out string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.lines) >= maxHighlightCache {
		h.lines = map[lineKey]string{}
	}
	h.lines[key] = out
}

// lexerFor resolves and caches a lexer per file extension, since matching by
// filename is not free and a diff revisits the same file many times.
func (h *Highlighter) lexerFor(key, path string) chroma.Lexer {
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
