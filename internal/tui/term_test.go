package tui

import (
	"strings"
	"testing"
)

func TestTerm(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello", "hello"},
		{"crlf lines", "a\r\nb\r\n", "a\nb\n"},
		{"carriage return overwrites", "12345\rab", "ab345"},
		{"progress bar", "10%\r50%\r100%\r\n", "100%\n"},
		{"backspace", "abc\b\bX", "aXc"},
		{"backspace at start", "\b\bx", "x"},
		{"tab stops", "a\tb\tc", "a       b       c"},
		{"sgr kept", "\x1b[31mred\x1b[0m plain", "\x1b[0m\x1b[31mred\x1b[0m plain"},
		{"sgr combined", "\x1b[1m\x1b[32mX\x1b[m", "\x1b[0m\x1b[1;32mX\x1b[0m"},
		{"sgr spans lines", "\x1b[33ma\r\nb\x1b[0m", "\x1b[0m\x1b[33ma\x1b[0m\n\x1b[0m\x1b[33mb\x1b[0m"},
		{"cursor movement dropped", "a\x1b[2Jb\x1b[1;1Hc\x1b[3A", "abc"},
		{"private mode dropped", "\x1b[?25lx\x1b[?25h", "x"},
		{"osc title with bel", "\x1b]0;title\x07x", "x"},
		{"osc with st", "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"charset escape dropped", "\x1b(Bx", "x"},
		{"other c0 dropped", "a\x00\x07\x0eb", "ab"},
		{"unicode", "grüße 世界 ✓", "grüße 世界 ✓"},
		{"c1 control dropped", "a\u0085b", "ab"},
		{"invalid utf8 dropped", "a\xffb", "ab"},
		{"trailing spaces trimmed", "ab   \r\n", "ab\n"},
		{"cursor past end pads", "abc\rabcdef", "abcdef"},
		{"erase to end of line", "50%\r\x1b[KOK", "OK"},
		{"erase whole line", "abc\x1b[2Kx", "   x"},
		{"erase to cursor", "abcdef\rab\x1b[1Kx", "  xdef"},
		{"esc esc", "\x1b\x1b[31mred", "\x1b[0m\x1b[31mred\x1b[0m"},
		// The newline takes effect; "b" then ends the ESC sequence, as in a
		// real terminal.
		{"newline inside escape", "a\x1b\nb", "a\n"},
		{"private sgr-like sequence", "\x1b[<0mx\x1b[=1mx", "xx"},
		{"invalid utf8 keeps next byte", "x\xe9\x1b[31mred", "x\x1b[0m\x1b[31mred\x1b[0m"},
		{"truncated utf8 keeps newline", "a\xc3\nb", "a\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Feed byte by byte and as a whole: chunking must not matter.
			whole := newTerm(0)
			whole.Write([]byte(tt.in))
			bytewise := newTerm(0)
			for i := range len(tt.in) {
				bytewise.Write([]byte{tt.in[i]})
			}
			if got := whole.String(); got != tt.want {
				t.Errorf("whole: got %q, want %q", got, tt.want)
			}
			if got := bytewise.String(); got != tt.want {
				t.Errorf("bytewise: got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTermIncrementalRender(t *testing.T) {
	tm := newTerm(0)
	tm.Write([]byte("line1\r\nline2"))
	if got := tm.String(); got != "line1\nline2" {
		t.Fatalf("got %q", got)
	}
	tm.Write([]byte("\rLINE2\r\nline3\r\n"))
	if got := tm.String(); got != "line1\nLINE2\nline3\n" {
		t.Fatalf("after overwrite: got %q", got)
	}
	tm.Write([]byte(strings.Repeat("x", 3)))
	if got := tm.String(); got != "line1\nLINE2\nline3\nxxx" {
		t.Fatalf("after append: got %q", got)
	}
}

func TestTermWraps(t *testing.T) {
	tm := newTerm(4)
	tm.Write([]byte("abcdefghij\r\nxy\r\nabcd\rZ"))
	if got := tm.String(); got != "abcd\nefgh\nij\nxy\nZbcd" {
		t.Errorf("got %q", got)
	}
	// A megabyte on one line renders quickly and never exceeds the width.
	big := newTerm(80)
	big.Write([]byte(strings.Repeat("x", 1<<20)))
	for i, l := range big.Lines() {
		if len(l) > 80 {
			t.Fatalf("line %d is %d wide", i, len(l))
		}
	}
}
