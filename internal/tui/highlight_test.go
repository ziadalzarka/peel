package tui

import (
	"io"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/styles"
)

// countingFormatter records how often a line reaches chroma, so a test can tell
// a cache hit from a re-lex.
type countingFormatter struct {
	inner chroma.Formatter
	calls int
}

func (c *countingFormatter) Format(w io.Writer, s *chroma.Style, it chroma.Iterator) error {
	c.calls++
	return c.inner.Format(w, s, it)
}

func newCountingHighlighter(t *testing.T) (*Highlighter, *countingFormatter) {
	t.Helper()
	inner := formatters.Get("terminal256")
	style := styles.Get("github-dark")
	if inner == nil || style == nil {
		t.Fatal("chroma is missing its terminal256 formatter or github-dark style")
	}
	counter := &countingFormatter{inner: inner}
	return &Highlighter{
		formatter: counter,
		style:     style,
		lexers:    map[string]chroma.Lexer{},
		lines:     map[lineKey]string{},
	}, counter
}

// A screenful is redrawn on every frame, so the same line arrives over and over.
// Lexing it once is what keeps a frame cheap enough to answer the keyboard.
func TestHighlightingALineTwiceLexesItOnce(t *testing.T) {
	h, counter := newCountingHighlighter(t)

	first := h.Line("main.go", "func main() {}")
	second := h.Line("main.go", "func main() {}")

	if counter.calls != 1 {
		t.Fatalf("lexed %d times, want 1", counter.calls)
	}
	if first != second {
		t.Fatalf("cached line differs from the first:\n first = %q\nsecond = %q", first, second)
	}
	if first == "func main() {}" {
		t.Fatal("the line came back unhighlighted")
	}
}

func TestHighlightingCachesPerLanguageNotPerPath(t *testing.T) {
	h, counter := newCountingHighlighter(t)

	const text = "var x = 1"
	fromOne := h.Line("internal/tui/one.go", text)
	fromAnother := h.Line("internal/app/another.go", text)

	if counter.calls != 1 {
		t.Fatalf("lexed %d times, want 1 — two Go files share a lexer", counter.calls)
	}
	if fromOne != fromAnother {
		t.Fatalf("same text in two Go files highlighted differently:\n %q\n %q", fromOne, fromAnother)
	}
}

func TestHighlightingKeepsLanguagesApart(t *testing.T) {
	h, _ := newCountingHighlighter(t)

	const text = "class Thing:"
	asPython := h.Line("thing.py", text)
	asGo := h.Line("thing.go", text)

	if asPython == asGo {
		t.Fatalf("python and go both rendered %q — the cache key lost the language", asPython)
	}
}

// Dropping the cache whole is how it stays bounded; what it must not do is start
// handing back the wrong colours afterwards.
func TestHighlightingSurvivesTheCacheFillingUp(t *testing.T) {
	const text = "func main() {}"
	reference, _ := newCountingHighlighter(t)
	want := reference.Line("main.go", text)

	h, _ := newCountingHighlighter(t)
	for i := 0; i < maxHighlightCache; i++ {
		h.lines[lineKey{lang: ".fill", text: string(rune(i))}] = ""
	}
	got := h.Line("main.go", text)

	if got != want {
		t.Fatalf("a line lexed after the cache was dropped came back wrong:\nwant = %q\n got = %q", want, got)
	}
	if len(h.lines) != 1 {
		t.Fatalf("cache held %d entries after the drop, want just the new line", len(h.lines))
	}
}
