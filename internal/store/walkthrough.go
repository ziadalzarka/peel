package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Walkthrough is a cached AI narrative of a changeset.
type Walkthrough struct {
	// Target is what was summarised: empty for the working tree, or a pull
	// request reference.
	Target string `json:"target"`
	// Fingerprint identifies the diff this narrative describes. A changed
	// fingerprint means the cache is stale.
	Fingerprint string `json:"fingerprint"`
	// Provider records which AI provider produced it.
	Provider  string    `json:"provider"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// Fresh reports whether the cached narrative still describes the given diff.
func (w Walkthrough) Fresh(target, fingerprint string) bool {
	return w.Body != "" && w.Target == target && w.Fingerprint == fingerprint
}

// Steps splits the narrative into the groups it is written as.
//
// It parses rather than stores, so a narrative cached by an older build reads
// back grouped without a migration, and the body on disk stays the markdown a
// person or an agent would want to read.
func (w Walkthrough) Steps() []Step { return ParseSteps(w.Body) }

// Step is one group of a walkthrough: a coherent piece of the change, the files
// it touches, and the explanation tying them together.
type Step struct {
	// Number is the step's place in the reading order. It is 0 for a section
	// that closes the walkthrough rather than advancing it, such as "Worth a
	// close look".
	Number int
	Title  string
	// Files are the paths the step covers, in the order the narrative lists
	// them. A closing section usually has none.
	Files []string
	// Body is the explanation, as markdown.
	Body string
}

// backticked matches one `code span`.
var backticked = regexp.MustCompile("`([^`]+)`")

// ParseSteps reads the shape the default instruction asks a provider for: a
// `## 1. Title` heading, a line of backticked paths, then the explanation.
//
// It is deliberately forgiving. A provider that ignores the numbering, drops
// the path line, or was handed a custom instruction still yields something —
// worst case a single untitled step holding the whole narrative — because a
// walkthrough that renders as prose is better than one that renders as nothing.
func ParseSteps(body string) []Step {
	var (
		steps []Step
		cur   *Step
		buf   []string
	)

	flush := func() {
		if cur == nil {
			return
		}
		cur.Files, buf = splitFileLine(buf)
		cur.Body = strings.TrimSpace(strings.Join(buf, "\n"))
		if cur.Title != "" || cur.Body != "" || len(cur.Files) > 0 {
			steps = append(steps, *cur)
		}
		cur, buf = nil, nil
	}

	for _, line := range strings.Split(body, "\n") {
		if title, ok := heading(line); ok {
			flush()
			number, rest := leadingNumber(title)
			cur = &Step{Number: number, Title: rest}
			continue
		}
		if cur == nil {
			// Anything before the first heading is a step of its own, so a
			// narrative with no headings at all is still one readable group.
			cur = &Step{}
		}
		buf = append(buf, line)
	}
	flush()
	return steps
}

// heading returns the text of a markdown heading line.
func heading(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	hashes := 0
	for hashes < len(trimmed) && trimmed[hashes] == '#' {
		hashes++
	}
	if hashes == 0 || hashes == len(trimmed) || trimmed[hashes] != ' ' {
		return "", false
	}
	return strings.TrimSpace(trimmed[hashes:]), true
}

// leadingNumber splits "1. Title" into its number and the title after it.
func leadingNumber(title string) (int, string) {
	digits := 0
	for digits < len(title) && title[digits] >= '0' && title[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits >= len(title) {
		return 0, title
	}
	if title[digits] != '.' && title[digits] != ')' {
		return 0, title
	}
	number, err := strconv.Atoi(title[:digits])
	if err != nil {
		return 0, title
	}
	return number, strings.TrimSpace(title[digits+1:])
}

// splitFileLine takes the path line off the front of a step's lines, returning
// the paths and whatever is left for the body.
func splitFileLine(lines []string) ([]string, []string) {
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if files, ok := parseFileLine(line); ok {
			return files, lines[i+1:]
		}
		return nil, lines[i:]
	}
	return nil, nil
}

// parseFileLine reads a line that is nothing but backticked paths, so a line of
// prose that happens to open with an identifier is not mistaken for one.
func parseFileLine(line string) ([]string, bool) {
	spans := backticked.FindAllStringSubmatch(line, -1)
	if len(spans) == 0 {
		return nil, false
	}
	if strings.TrimFunc(backticked.ReplaceAllString(line, ""), isPathSeparator) != "" {
		return nil, false
	}

	out := make([]string, 0, len(spans))
	for _, span := range spans {
		if path := strings.TrimSpace(span[1]); path != "" {
			out = append(out, path)
		}
	}
	return out, len(out) > 0
}

func isPathSeparator(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("·,;•|", r)
}

// WalkthroughCache stores the most recent narrative.
//
// Only one narrative is kept: it is a cache, not a history, and regenerating
// is cheap enough that eviction policy would be more machinery than value.
type WalkthroughCache interface {
	Load() (Walkthrough, bool, error)
	Save(w Walkthrough) error
	Clear() error
}

// JSONWalkthroughCache stores the narrative as JSON on disk.
type JSONWalkthroughCache struct {
	path string
	now  func() time.Time
}

// NewJSONWalkthroughCache returns a cache backed by the file at path.
func NewJSONWalkthroughCache(path string, opts ...WalkthroughOption) *JSONWalkthroughCache {
	c := &JSONWalkthroughCache{path: path, now: time.Now}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WalkthroughOption configures a JSONWalkthroughCache.
type WalkthroughOption func(*JSONWalkthroughCache)

// WithWalkthroughClock overrides the timestamp source, for tests.
func WithWalkthroughClock(fn func() time.Time) WalkthroughOption {
	return func(c *JSONWalkthroughCache) { c.now = fn }
}

// Path returns the file the cache reads and writes.
func (c *JSONWalkthroughCache) Path() string { return c.path }

// Load returns the cached narrative. ok is false when nothing is cached.
func (c *JSONWalkthroughCache) Load() (Walkthrough, bool, error) {
	b, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Walkthrough{}, false, nil
	}
	if err != nil {
		return Walkthrough{}, false, fmt.Errorf("read %s: %w", c.path, err)
	}

	var w Walkthrough
	if err := json.Unmarshal(b, &w); err != nil {
		// A corrupt cache is not worth failing over — regenerate instead.
		return Walkthrough{}, false, nil
	}
	return w, w.Body != "", nil
}

// Save replaces the cached narrative.
func (c *JSONWalkthroughCache) Save(w Walkthrough) error {
	if w.CreatedAt.IsZero() {
		w.CreatedAt = c.now().UTC()
	}
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return fmt.Errorf("encode walkthrough: %w", err)
	}
	b = append(b, '\n')

	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".walkthrough-*.json")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, c.path); err != nil {
		return fmt.Errorf("replace %s: %w", c.path, err)
	}
	return nil
}

// Clear discards the cached narrative.
func (c *JSONWalkthroughCache) Clear() error {
	err := os.Remove(c.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", c.path, err)
	}
	return nil
}

// Fingerprint hashes diff text so a cached narrative can be invalidated when
// the changeset moves on.
func Fingerprint(diff string) string {
	sum := sha256.Sum256([]byte(diff))
	return hex.EncodeToString(sum[:8])
}
