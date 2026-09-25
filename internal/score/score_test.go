package score

import (
	"math"
	"testing"
	"time"
)

const eps = 1e-9

func almostEqual(a, b float64) bool { return math.Abs(a-b) < eps }

func TestRound(t *testing.T) {
	tests := []struct {
		name     string
		round    Round
		wpm      float64
		accuracy float64
	}{
		{
			name:     "one minute perfect",
			round:    Round{Chars: 50, Keystrokes: 50, Duration: time.Minute},
			wpm:      10,
			accuracy: 1,
		},
		{
			name:     "half minute with errors",
			round:    Round{Chars: 25, Keystrokes: 30, Errors: 6, Duration: 30 * time.Second},
			wpm:      10,
			accuracy: 0.8,
		},
		{
			name:     "zero duration",
			round:    Round{Chars: 10, Keystrokes: 10},
			wpm:      0,
			accuracy: 1,
		},
		{
			name:     "negative duration",
			round:    Round{Chars: 10, Keystrokes: 10, Duration: -time.Second},
			wpm:      0,
			accuracy: 1,
		},
		{
			name:     "zero keystrokes",
			round:    Round{Chars: 10, Duration: time.Minute},
			wpm:      2,
			accuracy: 0,
		},
		{
			name:     "all wrong",
			round:    Round{Chars: 5, Keystrokes: 4, Errors: 4, Duration: time.Minute},
			wpm:      1,
			accuracy: 0,
		},
		{
			name:     "errors exceeding keystrokes clamp to zero",
			round:    Round{Keystrokes: 2, Errors: 3},
			accuracy: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.round.WPM(); !almostEqual(got, tt.wpm) {
				t.Errorf("WPM() = %v, want %v", got, tt.wpm)
			}
			if got := tt.round.Accuracy(); !almostEqual(got, tt.accuracy) {
				t.Errorf("Accuracy() = %v, want %v", got, tt.accuracy)
			}
		})
	}
}

func TestSession(t *testing.T) {
	tests := []struct {
		name       string
		rounds     []Round
		chars      int
		keystrokes int
		errors     int
		duration   time.Duration
		wpm        float64
		accuracy   float64
		score      float64
	}{
		{
			name: "empty",
		},
		{
			name: "single round",
			rounds: []Round{
				{Chars: 50, Keystrokes: 50, Duration: time.Minute},
			},
			chars:      50,
			keystrokes: 50,
			duration:   time.Minute,
			wpm:        10,
			accuracy:   1,
			score:      10,
		},
		{
			name: "totals not averages",
			rounds: []Round{
				{ChallengeID: "a", Chars: 20, Keystrokes: 20, Duration: 30 * time.Second},
				{ChallengeID: "b", Chars: 80, Keystrokes: 80, Errors: 20, Duration: 90 * time.Second},
			},
			chars:      100,
			keystrokes: 100,
			errors:     20,
			duration:   2 * time.Minute,
			wpm:        10,
			accuracy:   0.8,
			score:      8,
		},
		{
			name: "zero duration",
			rounds: []Round{
				{Chars: 10, Keystrokes: 10, Errors: 5},
			},
			chars:      10,
			keystrokes: 10,
			errors:     5,
			accuracy:   0.5,
		},
		{
			name: "zero keystrokes",
			rounds: []Round{
				{Chars: 10, Duration: time.Minute},
			},
			chars:    10,
			duration: time.Minute,
			wpm:      2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Session{Rounds: tt.rounds}
			if got := s.Chars(); got != tt.chars {
				t.Errorf("Chars() = %d, want %d", got, tt.chars)
			}
			if got := s.Keystrokes(); got != tt.keystrokes {
				t.Errorf("Keystrokes() = %d, want %d", got, tt.keystrokes)
			}
			if got := s.Errors(); got != tt.errors {
				t.Errorf("Errors() = %d, want %d", got, tt.errors)
			}
			if got := s.Duration(); got != tt.duration {
				t.Errorf("Duration() = %v, want %v", got, tt.duration)
			}
			if got := s.WPM(); !almostEqual(got, tt.wpm) {
				t.Errorf("WPM() = %v, want %v", got, tt.wpm)
			}
			if got := s.Accuracy(); !almostEqual(got, tt.accuracy) {
				t.Errorf("Accuracy() = %v, want %v", got, tt.accuracy)
			}
			if got := s.Score(); !almostEqual(got, tt.score) {
				t.Errorf("Score() = %v, want %v", got, tt.score)
			}
		})
	}
}
