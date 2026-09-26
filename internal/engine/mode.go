package engine

import (
	"strings"

	"shast/internal/catalog"
	"shast/internal/outcmp"
	"shast/internal/sandbox"
)

// Mode is a way to play a challenge: what the player is shown, when the
// input may be submitted and how the executed result is judged. A
// challenge carries both a target command and (for deterministic commands)
// an expected output, so every mode works on the same catalog.
type Mode interface {
	// Name identifies the mode, e.g. in highscores.
	Name() string
	// Eligible reports whether c can be played in this mode.
	Eligible(c catalog.Challenge) bool
	// Prompt returns what the player is shown for c.
	Prompt(c catalog.Challenge) string
	// CanSubmit reports whether input may be submitted for execution.
	CanSubmit(c catalog.Challenge, input string) bool
	// Judge evaluates the executed input given its normalized output.
	Judge(c catalog.Challenge, input, output string, res sandbox.Result) Verdict
}

// Verdict is the judgement of an executed round.
type Verdict struct {
	Passed bool
	Reason string // why the round failed, "" if it passed
}

// Speed is Mode 1: the player types the shown command exactly; it can
// only be submitted on an exact match and then counts as passed.
type Speed struct{}

// Name implements Mode.
func (Speed) Name() string { return "speed" }

// Eligible implements Mode: every challenge with a command.
func (Speed) Eligible(c catalog.Challenge) bool { return c.Command != "" }

// Prompt implements Mode: the command to type.
func (Speed) Prompt(c catalog.Challenge) string { return c.Command }

// CanSubmit implements Mode: only an exact match.
func (Speed) CanSubmit(c catalog.Challenge, input string) bool { return input == c.Command }

// Judge implements Mode. A submitted command is always correct.
func (Speed) Judge(catalog.Challenge, string, string, sandbox.Result) Verdict {
	return Verdict{Passed: true}
}

// Reverse is Mode 2 (Reverse Challenge): the player sees an expected
// output and writes any command that produces it.
//
// It is a stub: the rules below are complete, but the game does not offer
// the mode yet because the TUI lacks a free-text input screen that shows
// the expected output instead of a target command. See README.
type Reverse struct{}

// Name implements Mode.
func (Reverse) Name() string { return "reverse" }

// Eligible implements Mode: only deterministic challenges with a stored
// expected output can be compared reliably.
func (Reverse) Eligible(c catalog.Challenge) bool {
	return c.Deterministic && outcmp.Normalize(c.Expected) != ""
}

// Prompt implements Mode: the expected output.
func (Reverse) Prompt(c catalog.Challenge) string { return c.Expected }

// CanSubmit implements Mode: any non-blank command.
func (Reverse) CanSubmit(_ catalog.Challenge, input string) bool {
	return strings.TrimSpace(input) != ""
}

// Judge implements Mode: the command must finish on its own and its output
// must equal the expected output under the challenge's comparison options
// (run at sandbox.FixedSize).
func (Reverse) Judge(c catalog.Challenge, _, output string, res sandbox.Result) Verdict {
	switch {
	case res.TimedOut:
		return Verdict{Reason: "the command timed out"}
	case res.Truncated:
		return Verdict{Reason: "the command printed too much output"}
	}
	if outcmp.Equal(c.Expected, output, c.Compare) {
		return Verdict{Passed: true}
	}
	return Verdict{Reason: "output differs from the expected output"}
}
