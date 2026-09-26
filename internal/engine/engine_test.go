package engine

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"shast/internal/catalog"
	"shast/internal/outcmp"
	"shast/internal/sandbox"
	"shast/internal/score"
)

// testCatalog has four challenges per difficulty in two categories.
func testCatalog() []catalog.Challenge {
	var cs []catalog.Challenge
	for _, d := range catalog.Difficulties() {
		for i := range 4 {
			cat := "text"
			if i%2 == 1 {
				cat = "json"
			}
			cs = append(cs, catalog.Challenge{
				ID:            fmt.Sprintf("%s-%d", d, i),
				Command:       fmt.Sprintf("echo %s %d", d, i),
				Category:      cat,
				Difficulty:    d,
				Explanation:   "x",
				Deterministic: i != 3,
			})
			if i < 2 {
				cs[len(cs)-1].Expected = "out\n"
			}
		}
	}
	return cs
}

func ids(cs []catalog.Challenge) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func TestSelect(t *testing.T) {
	cs := testCatalog()
	tests := []struct {
		name    string
		cfg     Config
		wantN   int
		check   func(t *testing.T, got []catalog.Challenge)
		wantErr error
	}{
		{"all random", Config{Mode: Speed{}, Rounds: 100, Order: OrderRandom, Seed: 1}, 12, nil, nil},
		{"limited rounds", Config{Mode: Speed{}, Rounds: 5, Order: OrderRandom, Seed: 1}, 5, nil, nil},
		{"filter difficulty", Config{Mode: Speed{}, Filter: catalog.Filter{Difficulty: catalog.Hard}, Rounds: 10, Order: OrderRandom}, 4,
			func(t *testing.T, got []catalog.Challenge) {
				for _, c := range got {
					if c.Difficulty != catalog.Hard {
						t.Errorf("%s is not hard", c.ID)
					}
				}
			}, nil},
		{"filter category and difficulty", Config{Mode: Speed{}, Filter: catalog.Filter{Difficulty: catalog.Easy, Category: "json"}, Rounds: 10, Order: OrderRandom}, 2, nil, nil},
		{"difficulty order", Config{Mode: Speed{}, Rounds: 12, Order: OrderDifficulty, Seed: 7}, 12,
			func(t *testing.T, got []catalog.Challenge) {
				if !slices.IsSortedFunc(got, func(a, b catalog.Challenge) int { return a.Difficulty.Rank() - b.Difficulty.Rank() }) {
					t.Errorf("not sorted by difficulty: %v", ids(got))
				}
			}, nil},
		{"difficulty order draws from all difficulties", Config{Mode: Speed{}, Rounds: 6, Order: OrderDifficulty, Seed: 7}, 6,
			func(t *testing.T, got []catalog.Challenge) {
				if !slices.IsSortedFunc(got, func(a, b catalog.Challenge) int { return a.Difficulty.Rank() - b.Difficulty.Rank() }) {
					t.Errorf("not sorted by difficulty: %v", ids(got))
				}
				if got[len(got)-1].Difficulty == catalog.Easy {
					t.Errorf("only easy challenges drawn: %v", ids(got))
				}
			}, nil},
		{"reverse mode only deterministic with expected", Config{Mode: Reverse{}, Rounds: 100, Order: OrderRandom}, 6,
			func(t *testing.T, got []catalog.Challenge) {
				for _, c := range got {
					if !c.Deterministic || c.Expected == "" {
						t.Errorf("%s not eligible for reverse mode", c.ID)
					}
				}
			}, nil},
		{"no match", Config{Mode: Speed{}, Filter: catalog.Filter{Category: "git"}, Rounds: 5, Order: OrderRandom}, 0, nil, ErrNoChallenges},
		{"zero rounds", Config{Mode: Speed{}, Rounds: 0, Order: OrderRandom}, 0, nil, errAny},
		{"unknown order", Config{Mode: Speed{}, Rounds: 1, Order: "alphabetic"}, 0, nil, errAny},
		{"no mode", Config{Rounds: 1, Order: OrderRandom}, 0, nil, errAny},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Select(cs, tt.cfg)
			if tt.wantErr != nil {
				if err == nil || (tt.wantErr != errAny && !errors.Is(err, tt.wantErr)) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.wantN {
				t.Fatalf("got %d challenges %v, want %d", len(got), ids(got), tt.wantN)
			}
			seen := map[string]bool{}
			for _, c := range got {
				if seen[c.ID] {
					t.Errorf("repeated challenge %s", c.ID)
				}
				seen[c.ID] = true
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

var errAny = errors.New("any error")

func TestSelectSeed(t *testing.T) {
	cs := testCatalog()
	cfg := Config{Mode: Speed{}, Rounds: 12, Order: OrderRandom, Seed: 42}
	a, _ := Select(cs, cfg)
	b, _ := Select(cs, cfg)
	if !slices.Equal(ids(a), ids(b)) {
		t.Errorf("same seed, different order: %v vs %v", ids(a), ids(b))
	}
	cfg.Seed = 43
	c, _ := Select(cs, cfg)
	if slices.Equal(ids(a), ids(c)) {
		t.Errorf("different seeds, same order: %v", ids(a))
	}
	if !slices.Equal(ids(cs), ids(testCatalog())) {
		t.Error("Select modified its input")
	}
}

func TestModes(t *testing.T) {
	c := catalog.Challenge{Command: "ls -l", Expected: "b\na\n", Deterministic: true, Compare: outcmp.Options{IgnoreOrder: true}}
	tests := []struct {
		name  string
		mode  Mode
		check func(t *testing.T, m Mode)
	}{
		{"speed", Speed{}, func(t *testing.T, m Mode) {
			if m.Prompt(c) != "ls -l" || !m.Eligible(c) || m.Eligible(catalog.Challenge{}) {
				t.Error("prompt/eligibility wrong")
			}
			for input, want := range map[string]bool{"ls -l": true, "ls -l ": false, "ls": false, "": false} {
				if got := m.CanSubmit(c, input); got != want {
					t.Errorf("CanSubmit(%q) = %v, want %v", input, got, want)
				}
			}
			if !m.Judge(c, "ls -l", "", sandbox.Result{ExitCode: 2}).Passed {
				t.Error("speed judge must pass")
			}
		}},
		{"reverse", Reverse{}, func(t *testing.T, m Mode) {
			if m.Prompt(c) != "b\na\n" || !m.Eligible(c) {
				t.Error("prompt/eligibility wrong")
			}
			nondet := c
			nondet.Deterministic = false
			if m.Eligible(nondet) || m.Eligible(catalog.Challenge{Command: "x", Deterministic: true}) {
				t.Error("non-deterministic or expected-less challenge eligible")
			}
			if m.CanSubmit(c, "  ") || !m.CanSubmit(c, "printf 'a\\nb'") {
				t.Error("CanSubmit wrong")
			}
			if !m.Judge(c, "x", "a\r\nb\r\n", sandbox.Result{}).Passed {
				t.Error("equal output (other order) rejected")
			}
			if v := m.Judge(c, "x", "a\n", sandbox.Result{}); v.Passed || v.Reason == "" {
				t.Errorf("different output accepted: %+v", v)
			}
			for _, res := range []sandbox.Result{{TimedOut: true}, {Truncated: true}} {
				if v := m.Judge(c, "x", "a\nb\n", res); v.Passed {
					t.Errorf("killed command accepted: %+v", res)
				}
			}
			empty := c
			empty.Expected = "\n"
			if m.Eligible(empty) {
				t.Error("empty expected output eligible")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mode.Name() != tt.name {
				t.Errorf("Name() = %q", tt.mode.Name())
			}
			tt.check(t, tt.mode)
		})
	}
}

func TestSessionLifecycle(t *testing.T) {
	s, err := NewSession(testCatalog(), Config{Mode: Speed{}, Rounds: 3, Order: OrderDifficulty, Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	if s.Total() != 3 || s.Round() != 1 || s.Done() {
		t.Fatalf("fresh session: total %d round %d done %v", s.Total(), s.Round(), s.Done())
	}
	first := s.Current()
	s.Complete(score.Round{ChallengeID: first.ID, Chars: 10, Keystrokes: 12, Errors: 2, Duration: 6 * time.Second}, sandbox.Result{}, Verdict{Passed: true})
	if s.Round() != 2 || s.Current().ID == first.ID {
		t.Fatalf("did not advance: round %d", s.Round())
	}
	s.Skip()
	third := s.Current()
	s.Complete(score.Round{ChallengeID: third.ID, Chars: 20, Keystrokes: 20, Duration: 6 * time.Second}, sandbox.Result{ExitCode: 1}, Verdict{Passed: true})
	if !s.Done() {
		t.Fatal("session not done after all rounds")
	}
	recs := s.Records()
	if len(recs) != 3 || recs[0].Skipped || !recs[1].Skipped || recs[2].Result.ExitCode != 1 {
		t.Errorf("records wrong: %+v", recs)
	}
	st := s.Stats()
	if len(st.Rounds) != 2 || st.Chars() != 30 || st.Errors() != 2 {
		t.Errorf("stats must exclude skipped rounds: %+v", st)
	}
	defer func() {
		if recover() == nil {
			t.Error("Current on finished session did not panic")
		}
	}()
	s.Current()
}
