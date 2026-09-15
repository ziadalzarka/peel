package tui

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
)

const markdownAddedMark = "│"

var markdownExtensions = map[string]bool{
	".md":       true,
	".markdown": true,
	".mkd":      true,
	".mdown":    true,
}

func isMarkdown(path string) bool {
	return markdownExtensions[strings.ToLower(extensionOf(path))]
}

var markdownLexer = chroma.Coalesce(chroma.MustNewLexer(
	&chroma.Config{Name: "peel markdown"},
	func() chroma.Rules {
		return chroma.Rules{
			"root": {
				{Pattern: `^ {0,3}#{1,6}(?:[ \t].*)?$`, Type: chroma.GenericHeading},
				{Pattern: `^[ \t]*\|?[ \t]*:?-{3,}:?[ \t]*(?:\|[ \t]*:?-{3,}:?[ \t]*)*\|?[ \t]*$`, Type: chroma.Punctuation},
				{Pattern: `\\.`, Type: chroma.Text},
				{Pattern: "(`+)[^`].*?(?<!`)\\1(?!`)", Type: chroma.LiteralStringBacktick},
				{Pattern: `(!?\[)([^\]]+)(\]\()([^)]*)(\))`, Type: chroma.ByGroups(
					chroma.Punctuation, chroma.GenericUnderline, chroma.Punctuation, chroma.NameAttribute, chroma.Punctuation,
				)},
				{Pattern: `<https?://[^>\s]+>|(?<![\w/])https?://[^\s<>()\[\]]+`, Type: chroma.GenericUnderline},
				{Pattern: `\*\*(?!\s)(?:[^*]|\*(?!\*))+?(?<!\s)\*\*`, Type: chroma.GenericStrong},
				{Pattern: `(?<!\w)__(?!\s)(?:[^_]|_(?!_))+?(?<!\s)__(?!\w)`, Type: chroma.GenericStrong},
				{Pattern: `\*(?![\s*])[^*]*?(?<![\s*])\*`, Type: chroma.GenericEmph},
				{Pattern: `(?<!\w)_(?![\s_])[^_]*?(?<![\s_])_(?!\w)`, Type: chroma.GenericEmph},
				{Pattern: `\|`, Type: chroma.Punctuation},
				{Pattern: "[^\\\\`*_\\[!<|h]+", Type: chroma.Text},
				{Pattern: `.`, Type: chroma.Text},
			},
		}
	},
))

func markdownStyle(base *chroma.Style) (*chroma.Style, error) {
	return base.Builder().
		Add(chroma.GenericHeading, "bold #79c0ff").
		Add(chroma.GenericStrong, "bold").
		Add(chroma.GenericEmph, "italic").
		Add(chroma.GenericUnderline, "underline #79c0ff").
		Add(chroma.NameAttribute, "#8b949e").
		Add(chroma.Punctuation, "#8b949e").
		Build()
}
