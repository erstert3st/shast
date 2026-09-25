package outcmp

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", " \t\r\n \n", ""},
		{"plain text", "hello world", "hello world"},
		{"plain multi-line", "a\nb\nc", "a\nb\nc"},

		{"CRLF", "a\r\nb\r\n", "a\nb"},
		{"mixed line endings", "a\r\nb\nc\r\n", "a\nb\nc"},
		{"CR CRLF", "abc\r\r\n", "abc"},

		{"CR overwrite shorter", "hello\rHE", "HEllo"},
		{"CR overwrite longer", "ab\rXYZW", "XYZW"},
		{"CR overwrite equal", "abc\rxyz", "xyz"},
		{"multiple CRs", "10%\r50%\r100%", "100%"},
		{"multiple CRs shrinking", "aaaa\rbbb\rcc", "ccba"},
		{"consecutive CRs", "abc\r\rX", "Xbc"},
		{"CR at end of line", "abc\r", "abc"},
		{"CR at end then CRLF", "done\r\r\nnext", "done\nnext"},
		{"CR at start of line", "\rabc", "abc"},
		{"CR overwrite per line", "ab\rX\ncd\rY", "Xb\nYd"},
		{"CR overwrite with spaces", "progress\r   ", "   gress"},
		{"CR overwrite spaces only", "abc\r   \n", ""},

		{"SGR colour", "\x1b[31mred\x1b[0m plain", "red plain"},
		{"SGR 256 and truecolor", "\x1b[38;5;208mA\x1b[38;2;1;2;3mB\x1b[m", "AB"},
		{"SGR bold reset", "\x1b[1mbold\x1b[22m", "bold"},
		{"CSI cursor movement", "a\x1b[2Ab\x1b[10;5Hc\x1b[3C", "abc"},
		{"CSI erase line", "old\x1b[2K\rnew", "new"},
		// Escape sequences are stripped before CR overwrite; erase-line is
		// not emulated (no VT emulator, see plan).
		{"CSI erase line not emulated", "50%\r\x1b[KOK", "OK%"},
		{"CSI private mode", "\x1b[?25lhidden\x1b[?25h", "hidden"},
		{"OSC title BEL", "\x1b]0;my title\x07text", "text"},
		{"OSC title ST", "\x1b]2;my title\x1b\\text", "text"},
		{"OSC hyperlink", "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"other ESC sequences", "\x1b(Ba\x1b=b\x1b7c\x1b8", "abc"},
		{"DCS", "\x1bPq#0\x1b\\text", "text"},
		{"C1 CSI byte", "\x9b31mtext", "text"},

		{"C0 controls", "a\x00b\x01c\x07d\be\x7ff", "abcdef"},
		{"form feed and vertical tab", "a\fb\vc", "abc"},

		{"trailing spaces", "a  \nb\t\n", "a\nb"},
		{"trailing whitespace after ANSI", "a \x1b[0m \n", "a"},
		{"leading whitespace kept", "  a\n\tb", "  a\n\tb"},
		{"trailing newlines", "a\n\n\n", "a"},
		{"trailing blank lines with spaces", "a\n  \n\t\n", "a"},
		{"blank lines in the middle", "a\n\n\nb", "a\n\n\nb"},
		{"leading blank lines", "\n\na", "\n\na"},
		{"tabs kept", "a\tb\t\tc", "a\tb\t\tc"},

		{"umlauts", "Grüße\r\n", "Grüße"},
		{"CJK", "日本語\r\n", "日本語"},
		{"emoji", "ok 🎉\r\n", "ok 🎉"},
		{"CR overwrite over umlauts", "äöü\rX", "Xöü"},
		{"CR overwrite over CJK", "日本語\rab", "ab語"},
		{"CR overwrite with emoji", "abc\r🎉", "🎉bc"},
		{"coloured Unicode", "\x1b[32m✓\x1b[0m Größe", "✓ Größe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestEqual(t *testing.T) {
	ordered := Options{}
	unordered := Options{IgnoreOrder: true}
	tests := []struct {
		name string
		a, b string
		opts Options
		want bool
	}{
		{"identical", "a\nb", "a\nb", ordered, true},
		{"both empty", "", "\r\n\n", ordered, true},
		{"normalization applied", "\x1b[1ma\x1b[0m  \r\nb\r\n\n", "a\nb", ordered, true},
		{"different text", "a\nb", "a\nc", ordered, false},
		{"different order", "a\nb\nc", "c\na\nb", ordered, false},
		{"different order ignored", "a\nb\nc", "c\na\nb", unordered, true},
		{"different order ignored with ANSI", "\x1b[31ma\x1b[0m\r\nb\r\n", "b\na", unordered, true},
		{"different multiplicity", "a\na\nb", "a\nb\nb", unordered, false},
		{"missing duplicate", "a\na\nb", "a\nb", unordered, false},
		{"different lines unordered", "a\nb", "a\nc", unordered, false},
		{"blank line counts", "a\n\nb", "a\nb", unordered, false},
		{"empty vs non-empty unordered", "", "a", unordered, false},
		{"both empty unordered", "", "  \n", unordered, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Equal(tt.a, tt.b, tt.opts); got != tt.want {
				t.Errorf("Equal(%q, %q, %+v) = %v, want %v", tt.a, tt.b, tt.opts, got, tt.want)
			}
		})
	}
}
