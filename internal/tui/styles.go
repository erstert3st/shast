package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

// Styles use the 16 ANSI colours so they follow the terminal's theme.
var (
	styleTitle      = lipgloss.NewStyle().Bold(true)
	styleLabel      = lipgloss.NewStyle().Bold(true)
	styleFaint      = lipgloss.NewStyle().Faint(true)
	styleAccent     = lipgloss.NewStyle().Foreground(lipgloss.Cyan).Bold(true)
	styleSelected   = lipgloss.NewStyle().Foreground(lipgloss.Black).Background(lipgloss.Cyan)
	styleCorrect    = lipgloss.NewStyle().Foreground(lipgloss.Green)
	styleWrong      = lipgloss.NewStyle().Foreground(lipgloss.Red)
	styleWrongSpace = lipgloss.NewStyle().Foreground(lipgloss.Red).Underline(true)
	stylePending    = lipgloss.NewStyle().Faint(true)
	styleCursor     = lipgloss.NewStyle().Reverse(true)
	styleWarn       = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	styleError      = lipgloss.NewStyle().Foreground(lipgloss.Red).Bold(true)
	styleExplain    = lipgloss.NewStyle().Italic(true)
)

// visibleWidth is the display width of s without escape sequences.
func visibleWidth(s string) int { return lipgloss.Width(s) }

// separator draws a horizontal rule with an optional label.
func separator(label string, width int) string {
	if label == "" {
		return styleFaint.Render(strings.Repeat("─", max(0, width)))
	}
	head := "── " + label + " "
	return styleFaint.Render(head + strings.Repeat("─", max(0, width-utf8.RuneCountInString(head))))
}

// wrapRunes splits the indices of runes into lines of at most width runes.
// Lines break after a space when possible, so words stay together.
func wrapRunes(runes []rune, width int) [][]int {
	var lines [][]int
	for start := 0; start < len(runes); {
		end := min(start+width, len(runes))
		if end < len(runes) {
			for i := end; i > start+width/2; i-- {
				if runes[i-1] == ' ' {
					end = i
					break
				}
			}
		}
		line := make([]int, 0, end-start)
		for i := start; i < end; i++ {
			line = append(line, i)
		}
		lines = append(lines, line)
		start = end
	}
	if len(lines) == 0 {
		lines = append(lines, nil)
	}
	return lines
}

// wrapText wraps plain text at word boundaries to lines of at most width
// runes; longer words are split.
func wrapText(text string, width int) []string {
	width = max(1, width)
	var lines []string
	var line []rune
	for _, word := range strings.Fields(text) {
		w := []rune(word)
		for len(w) > width {
			if len(line) > 0 {
				lines, line = append(lines, string(line)), nil
			}
			lines, w = append(lines, string(w[:width])), w[width:]
		}
		switch {
		case len(line) == 0:
			line = w
		case len(line)+1+len(w) <= width:
			line = append(append(line, ' '), w...)
		default:
			lines, line = append(lines, string(line)), w
		}
	}
	if len(line) > 0 {
		lines = append(lines, string(line))
	}
	return lines
}

// truncate shortens plain text to at most width runes.
func truncate(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:max(0, width)])
}

func tooSmall(w, h int) string {
	return fmt.Sprintf("terminal too small: %dx%d\nneeds at least %dx%d\n(ctrl+c quits)", w, h, minWidth, minHeight)
}
