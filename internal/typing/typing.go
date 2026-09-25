// Package typing holds the pure typing state of a single round: the target
// text, the player's input and the keystroke statistics derived from it.
//
// The package performs no I/O and never reads the clock; callers pass the
// time of each keystroke explicitly.
package typing

import "time"

// maxOverflow is the number of runes the input may exceed the target by.
// Further keystrokes are rejected.
const maxOverflow = 10

// Status is the feedback state of a single target rune.
type Status int

// Status values for a target rune.
const (
	Pending Status = iota // not typed yet
	Correct               // typed and matching the target
	Wrong                 // typed but different from the target
)

// State is the typing state of one round. The zero value is not usable;
// create a State with New.
type State struct {
	target     []rune
	input      []rune
	startedAt  time.Time
	started    bool
	keystrokes int
	correct    int
}

// New returns a fresh State for target.
func New(target string) *State {
	return &State{target: []rune(target)}
}

// Type appends r to the input. It reports false and has no effect if the
// input would exceed the target length by more than maxOverflow runes.
// The first accepted keystroke records now as the start time.
func (s *State) Type(r rune, now time.Time) bool {
	pos := len(s.input)
	if pos >= len(s.target)+maxOverflow {
		return false
	}
	if !s.started {
		s.started = true
		s.startedAt = now
	}
	s.input = append(s.input, r)
	s.keystrokes++
	if pos < len(s.target) && s.target[pos] == r {
		s.correct++
	}
	return true
}

// Backspace removes the last input rune. It is a no-op on empty input and
// is not counted as a keystroke.
func (s *State) Backspace() {
	if len(s.input) > 0 {
		s.input = s.input[:len(s.input)-1]
	}
}

// Target returns the target text.
func (s *State) Target() string { return string(s.target) }

// Input returns the current input.
func (s *State) Input() string { return string(s.input) }

// Statuses returns one Status per target rune: Correct or Wrong for typed
// positions, Pending for positions not typed yet.
func (s *State) Statuses() []Status {
	st := make([]Status, len(s.target))
	for i := range st {
		switch {
		case i >= len(s.input):
			st[i] = Pending
		case s.input[i] == s.target[i]:
			st[i] = Correct
		default:
			st[i] = Wrong
		}
	}
	return st
}

// Overflow returns the number of input runes beyond the target length.
func (s *State) Overflow() int {
	return max(0, len(s.input)-len(s.target))
}

// Matches reports whether the input equals the target exactly.
func (s *State) Matches() bool {
	return string(s.input) == string(s.target)
}

// Started reports whether at least one keystroke has been accepted.
func (s *State) Started() bool { return s.started }

// StartedAt returns the time of the first accepted keystroke, or the zero
// time if nothing has been typed yet.
func (s *State) StartedAt() time.Time { return s.startedAt }

// Keystrokes returns the number of accepted Type calls.
func (s *State) Keystrokes() int { return s.keystrokes }

// CorrectKeystrokes returns the number of keystrokes whose rune matched the
// target rune at the position it was typed at.
func (s *State) CorrectKeystrokes() int { return s.correct }

// Errors returns the number of wrong keystrokes. Errors that were later
// corrected with Backspace still count.
func (s *State) Errors() int { return s.keystrokes - s.correct }
