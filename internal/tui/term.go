package tui

import (
	"strings"
	"unicode/utf8"
)

// term renders raw TTY output for the output viewport. It is deliberately
// not a VT emulator: it applies carriage return, newline, backspace, tabs
// and erase-in-line, wraps at the terminal width like a real terminal,
// keeps SGR colours and drops every other control sequence. Input may
// arrive in arbitrary chunks; partial escape sequences and UTF-8 runes are
// completed by later writes.
type term struct {
	width int // wrap column; 0 disables wrapping
	lines []termLine
	row   int
	col   int
	style string // active SGR parameters, "" for default

	state  parseState
	params []byte // parameters of the CSI sequence being parsed
	utf8   []byte // incomplete UTF-8 rune

	rendered []string // cached rendering per line
	dirty    int      // lines from this index on need re-rendering
}

type termLine []termCell

type termCell struct {
	r     rune
	style string
}

type parseState int

const (
	stateGround    parseState = iota
	stateEsc                  // after ESC
	stateEscInter             // ESC followed by intermediate bytes
	stateCSI                  // ESC [
	stateString               // OSC, DCS, SOS, PM, APC: skipped until BEL or ST
	stateStringEsc            // ESC inside a string, maybe the start of ST
)

const (
	tabWidth = 8
	// maxStyleLen bounds the accumulated SGR parameters.
	maxStyleLen = 64
)

func newTerm(width int) *term {
	return &term{width: width, lines: []termLine{nil}}
}

// setWidth changes the wrap column for output that arrives from now on,
// like a resized terminal.
func (t *term) setWidth(width int) { t.width = width }

// Write implements io.Writer. It never fails.
func (t *term) Write(p []byte) (int, error) {
	t.markDirty(t.row)
	for _, b := range p {
		t.feed(b)
	}
	return len(p), nil
}

func (t *term) feed(b byte) {
	switch t.state {
	case stateGround:
		t.ground(b)
	case stateEsc:
		switch {
		case b == 0x1b: // ESC ESC: the second one starts over
		case b == '\r' || b == '\n' || b == '\b':
			t.ground(b) // C0 controls take effect inside sequences
		case b == '[':
			t.state, t.params = stateCSI, t.params[:0]
		case b == ']' || b == 'P' || b == 'X' || b == '^' || b == '_':
			t.state = stateString
		case b >= 0x20 && b <= 0x2f:
			t.state = stateEscInter
		default: // final byte (or garbage): sequence ends
			t.state = stateGround
		}
	case stateEscInter:
		switch {
		case b == '\r' || b == '\n' || b == '\b':
			t.ground(b)
		case b < 0x20 || b > 0x2f:
			t.state = stateGround
		}
	case stateCSI:
		switch {
		case b >= 0x40 && b <= 0x7e:
			switch b {
			case 'm':
				t.sgr(string(t.params))
			case 'K':
				t.eraseLine(string(t.params))
			}
			t.state = stateGround
		case b == '\r' || b == '\n' || b == '\b':
			t.ground(b)
		case b >= 0x20 && b <= 0x3f:
			t.params = append(t.params, b)
		default: // invalid in CSI: abort the sequence
			t.state = stateGround
		}
	case stateString:
		switch b {
		case 0x07:
			t.state = stateGround
		case 0x1b:
			t.state = stateStringEsc
		}
	case stateStringEsc:
		if b == '\\' {
			t.state = stateGround
		} else if b != 0x1b {
			t.state = stateString
		}
	}
}

func (t *term) ground(b byte) {
	if len(t.utf8) > 0 || b >= utf8.RuneSelf {
		t.utf8 = append(t.utf8, b)
		if !utf8.FullRune(t.utf8) {
			return
		}
		r, size := utf8.DecodeRune(t.utf8)
		rest := string(t.utf8[size:]) // bytes after an invalid sequence
		t.utf8 = t.utf8[:0]
		if r != utf8.RuneError && r >= 0xa0 { // skip C1 controls and invalid bytes
			t.put(r)
		}
		for i := range len(rest) {
			t.feed(rest[i])
		}
		return
	}
	switch b {
	case 0x1b:
		t.state = stateEsc
	case '\r':
		t.col = 0
	case '\n':
		t.newline()
	case '\b':
		t.col = max(0, t.col-1)
	case '\t':
		next := (t.col/tabWidth + 1) * tabWidth
		for t.col < next {
			t.put(' ')
		}
	default:
		if b >= 0x20 && b < 0x7f {
			t.put(rune(b))
		}
	}
}

// put writes r at the cursor and advances it, wrapping at the width.
func (t *term) put(r rune) {
	if t.width > 0 && t.col >= t.width {
		t.newline()
	}
	line := t.lines[t.row]
	for len(line) < t.col {
		line = append(line, termCell{r: ' '})
	}
	cell := termCell{r: r, style: t.style}
	if t.col < len(line) {
		line[t.col] = cell
	} else {
		line = append(line, cell)
	}
	t.lines[t.row] = line
	t.col++
}

func (t *term) newline() {
	t.row++
	t.col = 0
	if t.row == len(t.lines) {
		t.lines = append(t.lines, nil)
	}
}

// eraseLine implements EL (CSI K): 0 erases to the end of the line, 1 to
// the cursor, 2 the whole line.
func (t *term) eraseLine(params string) {
	line := t.lines[t.row]
	switch params {
	case "", "0":
		if t.col < len(line) {
			t.lines[t.row] = line[:t.col]
		}
	case "1":
		for i := 0; i <= t.col && i < len(line); i++ {
			line[i] = termCell{r: ' '}
		}
	case "2":
		t.lines[t.row] = nil
	}
}

// sgr applies the parameters of an SGR sequence to the active style.
func (t *term) sgr(params string) {
	if params != "" && strings.ContainsRune("<=>?", rune(params[0])) {
		return // private sequence ending in 'm', not SGR
	}
	switch {
	case params == "" || params == "0":
		t.style = ""
	case t.style == "" || len(t.style)+len(params) > maxStyleLen:
		t.style = params
	default:
		t.style += ";" + params
	}
}

func (t *term) markDirty(row int) {
	if row < t.dirty {
		t.dirty = row
	}
}

// Lines returns the rendered output: one entry per output line, styled
// runs wrapped in SGR sequences, each line ending with a reset. Only lines
// changed since the last call are rendered again; the result must not be
// modified.
func (t *term) Lines() []string {
	if t.dirty < len(t.rendered) {
		t.rendered = t.rendered[:t.dirty]
	}
	for _, line := range t.lines[len(t.rendered):] {
		t.rendered = append(t.rendered, renderLine(line))
	}
	t.dirty = t.row
	return t.rendered
}

// String returns Lines joined by newlines.
func (t *term) String() string { return strings.Join(t.Lines(), "\n") }

func renderLine(line termLine) string {
	var b strings.Builder
	style := ""
	for _, c := range line {
		if c.style != style {
			b.WriteString("\x1b[0m")
			if c.style != "" {
				b.WriteString("\x1b[" + c.style + "m")
			}
			style = c.style
		}
		b.WriteRune(c.r)
	}
	if style != "" {
		b.WriteString("\x1b[0m")
	}
	return strings.TrimRight(b.String(), " ")
}
