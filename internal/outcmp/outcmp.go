// Package outcmp normalizes and compares command output captured from a TTY.
//
// Normalization turns raw terminal output into the plain text a user would
// see, so outputs can be compared independently of colours, line endings and
// progress-style line rewriting.
package outcmp

import (
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Normalize returns s as plain text. It
//   - removes ANSI escape sequences (CSI, OSC, DCS, ...) and all other
//     control characters except '\n', '\r' and '\t',
//   - converts "\r\n" line endings to "\n",
//   - applies carriage-return overwrite within a line: text after a lone
//     '\r' overwrites the line from its first rune, as a terminal shows it,
//   - removes trailing whitespace from every line and trailing empty lines.
//
// The result has no trailing newline; whitespace-only input yields "".
func Normalize(s string) string {
	s = strings.Map(dropControl, ansi.Strip(s))
	s = strings.ReplaceAll(s, "\r\n", "\n")

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(overwrite(line), unicode.IsSpace)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// Equal reports whether a and b are equal after normalization. With
// opts.IgnoreOrder, lines are compared as multisets.
func Equal(a, b string, opts Options) bool {
	a, b = Normalize(a), Normalize(b)
	if !opts.IgnoreOrder {
		return a == b
	}
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	slices.Sort(la)
	slices.Sort(lb)
	return slices.Equal(la, lb)
}

// dropControl is a strings.Map function that removes control characters
// other than newline, carriage return and tab.
func dropControl(r rune) rune {
	if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
		return -1
	}
	return r
}

// overwrite applies carriage-return semantics to a single line: each '\r'
// moves the cursor back to column 0 and following runes replace existing
// ones; runes beyond the cursor remain visible.
func overwrite(line string) string {
	if !strings.ContainsRune(line, '\r') {
		return line
	}
	var buf []rune
	col := 0
	for _, r := range line {
		if r == '\r' {
			col = 0
			continue
		}
		if col < len(buf) {
			buf[col] = r
		} else {
			buf = append(buf, r)
		}
		col++
	}
	return string(buf)
}
