// Package tui implements the terminal UI of Mode 1 (Speed Typing): the
// setup screen, the rounds (typing, live command output, result) and the
// session summary with highscores. It is laid out for 80x24 and adapts to
// resizes.
package tui

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"time"

	tea "charm.land/bubbletea/v2"

	"shast/internal/catalog"
	"shast/internal/engine"
	"shast/internal/sandbox"
	"shast/internal/score"
)

// Runner executes commands in the sandbox; *sandbox.Session implements it.
type Runner interface {
	Reset(ctx context.Context) error
	Run(ctx context.Context, cmd string, size sandbox.Size, out io.Writer) (sandbox.Result, error)
	Resize(ctx context.Context, size sandbox.Size) error
}

// Scores stores highscores; *score.Store implements it.
type Scores interface {
	Add(e score.Entry) (rank int, top []score.Entry, err error)
}

// Settings are the session settings of the setup screen.
type Settings struct {
	Difficulty catalog.Difficulty // "" for all
	Category   string             // "" for all
	Rounds     int
	Order      engine.Order
	// Seed makes the order reproducible if FixedSeed is set; otherwise
	// every session draws a new seed.
	Seed      uint64
	FixedSeed bool
}

// Config configures the UI.
type Config struct {
	Catalog  []catalog.Challenge
	Defaults Settings // preselected in the setup screen (from CLI flags)
	Runner   Runner
	Scores   Scores

	// Now and NewSeed are replaceable for tests; nil means time.Now and
	// rand.Uint64.
	Now     func() time.Time
	NewSeed func() uint64
}

// Minimum terminal size; below it only a hint is shown.
const (
	minWidth  = 60
	minHeight = 16
)

type screen int

const (
	screenSetup screen = iota
	screenRound
	screenSummary
)

// Model is the Bubble Tea model of the game.
type Model struct {
	ctx    context.Context
	cfg    Config
	width  int
	height int
	screen screen

	setup   setupModel
	round   roundModel
	summary summaryModel
}

// New returns the initial model. ctx bounds all sandbox calls.
func New(ctx context.Context, cfg Config) *Model {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewSeed == nil {
		cfg.NewSeed = rand.Uint64
	}
	return &Model{ctx: ctx, cfg: cfg, setup: newSetup(cfg.Catalog, cfg.Defaults)}
}

// Run shows the UI until the player quits or ctx is canceled.
func Run(ctx context.Context, cfg Config) error {
	p := tea.NewProgram(New(ctx, cfg), tea.WithContext(ctx))
	_, err := p.Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil // terminated by a signal: a regular shutdown
	}
	return err
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, m.round.resize(m)
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.round.cancelRun()
			return m, tea.Quit
		}
	}
	switch m.screen {
	case screenSetup:
		return m, m.updateSetup(msg)
	case screenRound:
		return m, m.updateRound(msg)
	default:
		return m, m.updateSummary(msg)
	}
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	var content string
	switch {
	case m.width == 0:
		// No size yet.
	case m.width < minWidth || m.height < minHeight:
		content = tooSmall(m.width, m.height)
	case m.screen == screenSetup:
		content = m.viewSetup()
	case m.screen == screenRound:
		content = m.viewRound()
	default:
		content = m.viewSummary()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// startSession creates a session from the setup and starts the first round.
func (m *Model) startSession() tea.Cmd {
	st := m.setup.settings()
	seed := st.Seed
	if !st.FixedSeed {
		seed = m.cfg.NewSeed()
	}
	s, err := engine.NewSession(m.cfg.Catalog, engine.Config{
		Mode:   engine.Speed{},
		Filter: catalog.Filter{Difficulty: st.Difficulty, Category: st.Category},
		Rounds: st.Rounds,
		Order:  st.Order,
		Seed:   seed,
	})
	if err != nil {
		m.setup.err = err
		return nil
	}
	m.setup.err = nil
	// Keep the generation counter, so messages of the previous session
	// cannot match the new one.
	m.round = roundModel{session: s, settings: st, gen: m.round.gen}
	m.screen = screenRound
	return m.round.start(m)
}

// prepareSummary evaluates the finished session and records the
// highscore, once per session.
func (m *Model) prepareSummary() {
	if m.summary.session != m.round.session {
		m.summary = newSummary(m.round.session, m.round.settings, m.cfg.Scores, m.cfg.Now())
	}
}

// finishSession shows the summary.
func (m *Model) finishSession() {
	m.prepareSummary()
	m.screen = screenSummary
}
