// Package engine holds the game rules independent of the UI: game modes,
// the selection of challenges for a session and the round lifecycle.
package engine

import (
	"cmp"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"

	"shast/internal/catalog"
	"shast/internal/sandbox"
	"shast/internal/score"
)

// Order is the order in which a session presents its challenges.
type Order string

// Supported orders.
const (
	// OrderRandom shuffles all selected challenges.
	OrderRandom Order = "random"
	// OrderDifficulty presents easy, then medium, then hard challenges,
	// shuffled within each difficulty.
	OrderDifficulty Order = "difficulty"
)

// Orders returns all supported orders.
func Orders() []Order { return []Order{OrderRandom, OrderDifficulty} }

// Config selects the challenges of a session.
type Config struct {
	Mode   Mode
	Filter catalog.Filter
	Rounds int
	Order  Order
	Seed   uint64 // makes the order reproducible
}

// ErrNoChallenges is returned when no challenge matches the configuration.
var ErrNoChallenges = errors.New("no challenges match the selected filters")

// Select returns the challenges for a session: those eligible for the mode
// and matching the filter, ordered as configured, without repetitions and
// at most cfg.Rounds of them. The same seed yields the same selection.
func Select(cs []catalog.Challenge, cfg Config) ([]catalog.Challenge, error) {
	if cfg.Mode == nil {
		return nil, errors.New("select challenges: no mode")
	}
	if cfg.Rounds < 1 {
		return nil, fmt.Errorf("select challenges: rounds must be at least 1, got %d", cfg.Rounds)
	}
	var pool []catalog.Challenge
	for _, c := range cs {
		if cfg.Mode.Eligible(c) && cfg.Filter.Match(c) {
			pool = append(pool, c)
		}
	}
	if len(pool) == 0 {
		return nil, ErrNoChallenges
	}
	if !slices.Contains(Orders(), cfg.Order) {
		return nil, fmt.Errorf("select challenges: unknown order %q", cfg.Order)
	}
	// Draw the rounds at random from all matching challenges first, so
	// every difficulty can appear, then order them.
	rng := rand.New(rand.NewPCG(cfg.Seed, cfg.Seed^0x9e3779b97f4a7c15))
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	rounds := pool[:min(cfg.Rounds, len(pool))]
	if cfg.Order == OrderDifficulty {
		// Stable, so the random order within a difficulty is kept.
		slices.SortStableFunc(rounds, func(a, b catalog.Challenge) int {
			return cmp.Compare(a.Difficulty.Rank(), b.Difficulty.Rank())
		})
	}
	return rounds, nil
}

// RoundRecord is the outcome of one round.
type RoundRecord struct {
	Challenge catalog.Challenge
	Skipped   bool        // aborted by the player; excluded from the stats
	Stats     score.Round // typing statistics
	Result    sandbox.Result
	Verdict   Verdict
}

// Session is the round lifecycle of one game: it hands out the selected
// challenges one after another and records the outcome of each.
type Session struct {
	mode    Mode
	rounds  []catalog.Challenge
	records []RoundRecord
}

// NewSession selects the challenges for cfg and starts a session.
func NewSession(cs []catalog.Challenge, cfg Config) (*Session, error) {
	rounds, err := Select(cs, cfg)
	if err != nil {
		return nil, err
	}
	return &Session{mode: cfg.Mode, rounds: rounds}, nil
}

// Mode returns the game mode of the session.
func (s *Session) Mode() Mode { return s.mode }

// Total returns the number of rounds.
func (s *Session) Total() int { return len(s.rounds) }

// Round returns the 1-based number of the current round.
func (s *Session) Round() int { return len(s.records) + 1 }

// Done reports whether all rounds have been played.
func (s *Session) Done() bool { return len(s.records) >= len(s.rounds) }

// Current returns the challenge of the current round. It panics if the
// session is done.
func (s *Session) Current() catalog.Challenge {
	if s.Done() {
		panic("engine: Current called on a finished session")
	}
	return s.rounds[len(s.records)]
}

// Complete records the current round as played and advances.
func (s *Session) Complete(stats score.Round, res sandbox.Result, v Verdict) {
	s.records = append(s.records, RoundRecord{Challenge: s.Current(), Stats: stats, Result: res, Verdict: v})
}

// Skip records the current round as aborted and advances.
func (s *Session) Skip() {
	s.records = append(s.records, RoundRecord{Challenge: s.Current(), Skipped: true})
}

// Records returns the outcomes of the rounds played so far.
func (s *Session) Records() []RoundRecord { return slices.Clone(s.records) }

// Stats returns the typing statistics of all completed (not skipped) rounds.
func (s *Session) Stats() score.Session {
	var st score.Session
	for _, r := range s.records {
		if !r.Skipped {
			st.Rounds = append(st.Rounds, r.Stats)
		}
	}
	return st
}
