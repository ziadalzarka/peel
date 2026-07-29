package tui

import "github.com/charmbracelet/lipgloss"

// Theme holds every style the UI draws with, so colours are chosen in one
// place rather than scattered through the renderers.
type Theme struct {
	Added    lipgloss.Style
	Removed  lipgloss.Style
	Context  lipgloss.Style
	Gutter   lipgloss.Style
	HunkHead lipgloss.Style
	FileHead lipgloss.Style
	Note     lipgloss.Style
	Comment  lipgloss.Style
	Resolved lipgloss.Style
	Author   lipgloss.Style
	Cursor   lipgloss.Style
	Selected lipgloss.Style
	Header   lipgloss.Style
	Footer   lipgloss.Style
	Key      lipgloss.Style
	Status   lipgloss.Style
	Error    lipgloss.Style
	Staged   lipgloss.Style
	Partial  lipgloss.Style
	Dim      lipgloss.Style
	Title    lipgloss.Style
}

// DefaultTheme returns the built-in colour scheme. Colours are adaptive so the
// UI stays readable on a light terminal.
func DefaultTheme() Theme {
	var (
		green  = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
		red    = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}
		blue   = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"}
		yellow = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"}
		purple = lipgloss.AdaptiveColor{Light: "#8250df", Dark: "#bc8cff"}
		grey   = lipgloss.AdaptiveColor{Light: "#6e7781", Dark: "#8b949e"}
		faint  = lipgloss.AdaptiveColor{Light: "#8c959f", Dark: "#6e7681"}
		text   = lipgloss.AdaptiveColor{Light: "#1f2328", Dark: "#e6edf3"}
	)

	return Theme{
		Added:    lipgloss.NewStyle().Foreground(green),
		Removed:  lipgloss.NewStyle().Foreground(red),
		Context:  lipgloss.NewStyle().Foreground(text),
		Gutter:   lipgloss.NewStyle().Foreground(faint),
		HunkHead: lipgloss.NewStyle().Foreground(purple),
		FileHead: lipgloss.NewStyle().Foreground(blue).Bold(true),
		Note:     lipgloss.NewStyle().Foreground(grey).Italic(true),
		Comment:  lipgloss.NewStyle().Foreground(yellow),
		Resolved: lipgloss.NewStyle().Foreground(faint).Strikethrough(true),
		Author:   lipgloss.NewStyle().Foreground(grey),
		Cursor:   lipgloss.NewStyle().Foreground(blue).Bold(true),
		Selected: lipgloss.NewStyle().Foreground(yellow).Bold(true),
		Header:   lipgloss.NewStyle().Foreground(text).Bold(true),
		Footer:   lipgloss.NewStyle().Foreground(grey),
		Key:      lipgloss.NewStyle().Foreground(blue),
		Status:   lipgloss.NewStyle().Foreground(green),
		Error:    lipgloss.NewStyle().Foreground(red).Bold(true),
		Staged:   lipgloss.NewStyle().Foreground(green),
		Partial:  lipgloss.NewStyle().Foreground(yellow),
		Dim:      lipgloss.NewStyle().Foreground(faint),
		Title:    lipgloss.NewStyle().Foreground(purple).Bold(true),
	}
}
