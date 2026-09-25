package typing

import (
	"strings"
	"testing"
	"time"
)

// backspace in a test key sequence stands for a Backspace call.
const backspace = '\b'

// play feeds keys into s, one per millisecond starting at base, and returns
// how many Type calls were rejected.
func play(s *State, keys string, base time.Time) (rejected int) {
	for i, r := range []rune(keys) {
		if r == backspace {
			s.Backspace()
			continue
		}
		if !s.Type(r, base.Add(time.Duration(i)*time.Millisecond)) {
			rejected++
		}
	}
	return rejected
}

// statusString renders statuses compactly: c=Correct, w=Wrong, .=Pending.
func statusString(st []Status) string {
	var b strings.Builder
	for _, s := range st {
		switch s {
		case Correct:
			b.WriteByte('c')
		case Wrong:
			b.WriteByte('w')
		default:
			b.WriteByte('.')
		}
	}
	return b.String()
}

func TestState(t *testing.T) {
	bs := string(backspace)
	tests := []struct {
		name       string
		target     string
		keys       string
		input      string
		statuses   string
		overflow   int
		matches    bool
		started    bool
		keystrokes int
		correct    int
		rejected   int
	}{
		{
			name:     "untouched",
			target:   "ls -la",
			statuses: "......",
		},
		{
			name:       "correct sequence",
			target:     "ls -la",
			keys:       "ls -la",
			input:      "ls -la",
			statuses:   "cccccc",
			matches:    true,
			started:    true,
			keystrokes: 6,
			correct:    6,
		},
		{
			name:       "partial correct",
			target:     "ls -la",
			keys:       "ls",
			input:      "ls",
			statuses:   "cc....",
			started:    true,
			keystrokes: 2,
			correct:    2,
		},
		{
			name:       "wrong char then continue",
			target:     "ls -la",
			keys:       "lx -la",
			input:      "lx -la",
			statuses:   "cwcccc",
			started:    true,
			keystrokes: 6,
			correct:    5,
		},
		{
			name:       "backspace correction keeps error counted",
			target:     "ls -la",
			keys:       "lx" + bs + "s -la",
			input:      "ls -la",
			statuses:   "cccccc",
			matches:    true,
			started:    true,
			keystrokes: 7,
			correct:    6,
		},
		{
			name:       "backspace over correct char retyped counts again",
			target:     "ab",
			keys:       "a" + bs + "ab",
			input:      "ab",
			statuses:   "cc",
			matches:    true,
			started:    true,
			keystrokes: 3,
			correct:    3,
		},
		{
			name:     "backspace on empty input",
			target:   "ls",
			keys:     bs + bs,
			statuses: "..",
		},
		{
			name:       "backspace to empty after typing",
			target:     "ls",
			keys:       "l" + bs + bs,
			statuses:   "..",
			started:    true,
			keystrokes: 1,
			correct:    1,
		},
		{
			name:       "overflow counted and wrong",
			target:     "ls",
			keys:       "lsxx",
			input:      "lsxx",
			statuses:   "cc",
			overflow:   2,
			started:    true,
			keystrokes: 4,
			correct:    2,
		},
		{
			name:       "overflow limit enforced",
			target:     "ls",
			keys:       "ls" + strings.Repeat("x", 15),
			input:      "ls" + strings.Repeat("x", 10),
			statuses:   "cc",
			overflow:   10,
			started:    true,
			keystrokes: 12,
			correct:    2,
			rejected:   5,
		},
		{
			name:       "backspace frees room after overflow limit",
			target:     "ls",
			keys:       "ls" + strings.Repeat("x", 11) + bs + "y",
			input:      "ls" + strings.Repeat("x", 9) + "y",
			statuses:   "cc",
			overflow:   10,
			started:    true,
			keystrokes: 13,
			correct:    2,
			rejected:   1,
		},
		{
			name:       "correct char at matching position after overflow is still wrong",
			target:     "a",
			keys:       "aa",
			input:      "aa",
			statuses:   "c",
			overflow:   1,
			started:    true,
			keystrokes: 2,
			correct:    1,
		},
		{
			name:       "unicode target",
			target:     "echo ✓ ä",
			keys:       "echo ✓ a" + bs + "ä",
			input:      "echo ✓ ä",
			statuses:   "cccccccc",
			matches:    true,
			started:    true,
			keystrokes: 9,
			correct:    8,
		},
		{
			name:       "unicode wrong rune",
			target:     "✓✓",
			keys:       "✓x",
			input:      "✓x",
			statuses:   "cw",
			started:    true,
			keystrokes: 2,
			correct:    1,
		},
		{
			name:    "empty target matches empty input",
			target:  "",
			matches: true,
		},
		{
			name:       "empty target accepts overflow up to limit",
			target:     "",
			keys:       strings.Repeat("x", 11),
			input:      strings.Repeat("x", 10),
			overflow:   10,
			started:    true,
			keystrokes: 10,
			rejected:   1,
		},
	}
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(tt.target)
			if got := play(s, tt.keys, base); got != tt.rejected {
				t.Errorf("rejected = %d, want %d", got, tt.rejected)
			}
			if got := s.Target(); got != tt.target {
				t.Errorf("Target() = %q, want %q", got, tt.target)
			}
			if got := s.Input(); got != tt.input {
				t.Errorf("Input() = %q, want %q", got, tt.input)
			}
			if got := statusString(s.Statuses()); got != tt.statuses {
				t.Errorf("Statuses() = %q, want %q", got, tt.statuses)
			}
			if got := s.Overflow(); got != tt.overflow {
				t.Errorf("Overflow() = %d, want %d", got, tt.overflow)
			}
			if got := s.Matches(); got != tt.matches {
				t.Errorf("Matches() = %v, want %v", got, tt.matches)
			}
			if got := s.Started(); got != tt.started {
				t.Errorf("Started() = %v, want %v", got, tt.started)
			}
			if got := s.Keystrokes(); got != tt.keystrokes {
				t.Errorf("Keystrokes() = %d, want %d", got, tt.keystrokes)
			}
			if got := s.CorrectKeystrokes(); got != tt.correct {
				t.Errorf("CorrectKeystrokes() = %d, want %d", got, tt.correct)
			}
			if got, want := s.Errors(), tt.keystrokes-tt.correct; got != want {
				t.Errorf("Errors() = %d, want %d", got, want)
			}
		})
	}
}

func TestStartTime(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	t1 := t0.Add(time.Second)
	t2 := t0.Add(2 * time.Second)

	tests := []struct {
		name    string
		run     func(s *State)
		started bool
		want    time.Time
	}{
		{
			name: "not started",
			run:  func(*State) {},
		},
		{
			name: "backspace does not start",
			run:  func(s *State) { s.Backspace() },
		},
		{
			name:    "first keystroke starts",
			run:     func(s *State) { s.Type('l', t1) },
			started: true,
			want:    t1,
		},
		{
			name: "later keystrokes keep start",
			run: func(s *State) {
				s.Type('l', t1)
				s.Type('s', t2)
			},
			started: true,
			want:    t1,
		},
		{
			name: "start survives backspace to empty",
			run: func(s *State) {
				s.Type('x', t1)
				s.Backspace()
				s.Type('l', t2)
			},
			started: true,
			want:    t1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New("")
			tt.run(s)
			if got := s.Started(); got != tt.started {
				t.Errorf("Started() = %v, want %v", got, tt.started)
			}
			if got := s.StartedAt(); !got.Equal(tt.want) {
				t.Errorf("StartedAt() = %v, want %v", got, tt.want)
			}
		})
	}
}
