// Package score computes typing statistics for rounds and sessions and
// persists the local highscore list.
package score

import "time"

// charsPerWord is the conventional word length used for WPM.
const charsPerWord = 5

// Round holds the statistics of one typed challenge.
type Round struct {
	ChallengeID string
	Chars       int           // runes of the target command
	Keystrokes  int           // all accepted keystrokes
	Errors      int           // wrong keystrokes, corrected ones included
	Duration    time.Duration // first keystroke → Enter
}

// WPM returns the words per minute of the round, 0 if Duration <= 0.
func (r Round) WPM() float64 { return wpm(r.Chars, r.Duration) }

// Accuracy returns the share of correct keystrokes in [0,1], 0 if there
// were no keystrokes.
func (r Round) Accuracy() float64 { return accuracy(r.Keystrokes, r.Errors) }

// Session aggregates the rounds of one game session.
type Session struct {
	Rounds []Round
}

// Chars returns the total number of target runes.
func (s Session) Chars() int {
	n := 0
	for _, r := range s.Rounds {
		n += r.Chars
	}
	return n
}

// Keystrokes returns the total number of keystrokes.
func (s Session) Keystrokes() int {
	n := 0
	for _, r := range s.Rounds {
		n += r.Keystrokes
	}
	return n
}

// Errors returns the total number of wrong keystrokes.
func (s Session) Errors() int {
	n := 0
	for _, r := range s.Rounds {
		n += r.Errors
	}
	return n
}

// Duration returns the total typing time.
func (s Session) Duration() time.Duration {
	var d time.Duration
	for _, r := range s.Rounds {
		d += r.Duration
	}
	return d
}

// WPM returns total chars / 5 per total minute.
func (s Session) WPM() float64 { return wpm(s.Chars(), s.Duration()) }

// Accuracy returns total correct keystrokes / total keystrokes.
func (s Session) Accuracy() float64 { return accuracy(s.Keystrokes(), s.Errors()) }

// Score returns WPM × Accuracy, the value highscores are ranked by.
func (s Session) Score() float64 { return s.WPM() * s.Accuracy() }

func wpm(chars int, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(chars) / charsPerWord / d.Minutes()
}

func accuracy(keystrokes, errors int) float64 {
	if keystrokes <= 0 {
		return 0
	}
	a := float64(keystrokes-errors) / float64(keystrokes)
	return min(max(a, 0), 1)
}
