// Package catalog defines the challenges of the game and loads, validates,
// merges and filters them. The default catalog is embedded in the binary;
// additional catalogs can be layered on top of it without a rebuild.
package catalog

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"shast/internal/outcmp"
)

// Difficulty is the difficulty level of a challenge.
type Difficulty string

// The known difficulty levels.
const (
	Easy   Difficulty = "easy"
	Medium Difficulty = "medium"
	Hard   Difficulty = "hard"
)

// Difficulties returns all difficulty levels in ascending order.
func Difficulties() []Difficulty {
	return []Difficulty{Easy, Medium, Hard}
}

// Rank returns the position of d in ascending difficulty order (0 for
// Easy), or -1 if d is not a known difficulty.
func (d Difficulty) Rank() int {
	return slices.Index(Difficulties(), d)
}

// Categories returns all valid challenge categories.
func Categories() []string {
	return []string{
		"files", "search", "text", "json", "archive", "git",
		"permissions", "disk", "process", "network-config", "system",
	}
}

// Challenge is a single catalog entry.
type Challenge struct {
	ID string
	// Command is the command to type in Speed mode and the reference
	// solution in Reverse mode.
	Command     string
	Category    string
	Difficulty  Difficulty
	Explanation string
	// Deterministic reports whether the command's output is reproducible
	// against the seed. Only deterministic challenges are usable in
	// Reverse mode.
	Deterministic bool
	// Compare controls how the output is compared with Expected.
	Compare outcmp.Options
	// ExitCode is the exit code the command is expected to return.
	ExitCode int
	// AllowEmpty permits the command to produce no output.
	AllowEmpty bool
	// Expected is the stored expected output, or "" if there is none.
	Expected string
}

const maxCommandLen = 120

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Validate checks every challenge and reports all problems at once, each
// prefixed with the position and ID of the offending challenge.
func Validate(cs []Challenge) error {
	var errs []error
	firstIndex := make(map[string]int, len(cs))
	for i, c := range cs {
		prefix := fmt.Sprintf("challenge #%d", i+1)
		if c.ID != "" {
			prefix += fmt.Sprintf(" %q", c.ID)
		}
		ps := problems(c)
		if c.ID != "" {
			if j, dup := firstIndex[c.ID]; dup {
				ps = append(ps, fmt.Sprintf("duplicate id, first used by challenge #%d", j+1))
			} else {
				firstIndex[c.ID] = i
			}
		}
		for _, p := range ps {
			errs = append(errs, fmt.Errorf("%s: %s", prefix, p))
		}
	}
	return errors.Join(errs...)
}

// problems returns the validation problems of a single challenge.
func problems(c Challenge) []string {
	var ps []string
	switch {
	case c.ID == "":
		ps = append(ps, "missing id")
	case !idPattern.MatchString(c.ID):
		ps = append(ps, fmt.Sprintf("id must match %s", idPattern))
	}
	if c.Command == "" {
		ps = append(ps, "missing command")
	} else if p := commandProblem(c.Command); p != "" {
		ps = append(ps, p)
	}
	switch {
	case c.Category == "":
		ps = append(ps, "missing category")
	case !slices.Contains(Categories(), c.Category):
		ps = append(ps, fmt.Sprintf("unknown category %q (want one of %s)",
			c.Category, strings.Join(Categories(), ", ")))
	}
	switch {
	case c.Difficulty == "":
		ps = append(ps, "missing difficulty")
	case c.Difficulty.Rank() < 0:
		ps = append(ps, fmt.Sprintf("unknown difficulty %q (want one of %s, %s, %s)",
			c.Difficulty, Easy, Medium, Hard))
	}
	if strings.TrimSpace(c.Explanation) == "" {
		ps = append(ps, "missing explanation")
	}
	if c.ExitCode < 0 || c.ExitCode > 255 {
		ps = append(ps, fmt.Sprintf("exit_code %d out of range 0..255", c.ExitCode))
	}
	return ps
}

// commandProblem reports why cmd cannot be typed by a player, or "" if it can.
func commandProblem(cmd string) string {
	for i, r := range cmd {
		if r < 0x20 || r > 0x7e {
			return fmt.Sprintf("command contains %q at byte %d; only printable ASCII is allowed", r, i)
		}
	}
	if strings.HasPrefix(cmd, " ") || strings.HasSuffix(cmd, " ") {
		return "command has leading or trailing spaces"
	}
	if len(cmd) > maxCommandLen {
		return fmt.Sprintf("command is %d characters long, max %d", len(cmd), maxCommandLen)
	}
	return ""
}

// Merge returns base with overlay applied: an overlay challenge replaces the
// base challenge with the same ID in place, other overlay challenges are
// appended in overlay order. Neither input is modified.
func Merge(base, overlay []Challenge) []Challenge {
	out := slices.Clone(base)
	index := make(map[string]int, len(out)+len(overlay))
	for i, c := range out {
		index[c.ID] = i
	}
	for _, c := range overlay {
		if i, ok := index[c.ID]; ok {
			out[i] = c
			continue
		}
		index[c.ID] = len(out)
		out = append(out, c)
	}
	return out
}

// Filter selects challenges. A zero-valued field matches everything.
type Filter struct {
	Difficulty Difficulty
	Category   string
}

// Match reports whether c passes the filter.
func (f Filter) Match(c Challenge) bool {
	return (f.Difficulty == "" || c.Difficulty == f.Difficulty) &&
		(f.Category == "" || c.Category == f.Category)
}

// Select returns the challenges matching f, preserving their order.
func Select(cs []Challenge, f Filter) []Challenge {
	var out []Challenge
	for _, c := range cs {
		if f.Match(c) {
			out = append(out, c)
		}
	}
	return out
}
