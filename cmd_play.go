package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"shast/internal/catalog"
	"shast/internal/engine"
	"shast/internal/sandbox"
	"shast/internal/score"
	"shast/internal/tui"
)

func runPlay(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("play", flag.ContinueOnError)
	fs.SetOutput(stderr)
	difficulty := fs.String("difficulty", "all", "all, "+joinDifficulties())
	category := fs.String("category", "all", "all, "+strings.Join(catalog.Categories(), ", "))
	rounds := fs.Int("rounds", 10, "rounds per session")
	order := fs.String("order", string(engine.OrderRandom), "random or difficulty")
	seed := fs.Uint64("seed", 0, "seed for a reproducible order (default: random)")
	catalogDir := fs.String("catalog", "", "directory with an additional commands.yaml (and expected/)")
	timeout := fs.Duration("timeout", sandbox.DefaultTimeout, "per-command timeout")
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	settings, err := playSettings(*difficulty, *category, *rounds, *order)
	if err == nil && *timeout <= 0 {
		err = fmt.Errorf("timeout must be positive, got %v", *timeout)
	}
	if err != nil {
		fmt.Fprintln(stderr, "play:", err)
		return errUsage
	}
	settings.Seed = *seed
	fs.Visit(func(f *flag.Flag) { settings.FixedSeed = settings.FixedSeed || f.Name == "seed" })

	cs, err := catalog.LoadWithOverlay(*catalogDir)
	if err != nil {
		return err
	}
	scorePath, err := score.DefaultPath()
	if err != nil {
		return err
	}

	// Fail with a clear message before the TUI takes over the terminal.
	docker, err := openSandbox(ctx)
	if err != nil {
		return err
	}
	defer docker.Close()
	session, err := docker.StartSession(ctx, sandbox.Options{Timeout: *timeout})
	if err != nil {
		return err
	}
	defer session.Close()

	return tui.Run(ctx, tui.Config{
		Catalog:  cs,
		Defaults: settings,
		Runner:   session,
		Scores:   score.NewStore(scorePath),
	})
}

// playSettings validates the filter flags.
func playSettings(difficulty, category string, rounds int, order string) (tui.Settings, error) {
	var st tui.Settings
	switch {
	case difficulty == "all":
	case catalog.Difficulty(difficulty).Rank() >= 0:
		st.Difficulty = catalog.Difficulty(difficulty)
	default:
		return st, fmt.Errorf("unknown difficulty %q (want all, %s)", difficulty, joinDifficulties())
	}
	switch {
	case category == "all":
	case slices.Contains(catalog.Categories(), category):
		st.Category = category
	default:
		return st, fmt.Errorf("unknown category %q (want all, %s)", category, strings.Join(catalog.Categories(), ", "))
	}
	if rounds < 1 {
		return st, fmt.Errorf("rounds must be at least 1, got %d", rounds)
	}
	st.Rounds = rounds
	if !slices.Contains(engine.Orders(), engine.Order(order)) {
		return st, fmt.Errorf("unknown order %q (want random or difficulty)", order)
	}
	st.Order = engine.Order(order)
	return st, nil
}

func joinDifficulties() string {
	var ds []string
	for _, d := range catalog.Difficulties() {
		ds = append(ds, string(d))
	}
	return strings.Join(ds, ", ")
}
