package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"shast/internal/catalog"
	"shast/internal/engine"
	"shast/internal/sandbox"
	"shast/internal/score"
)

// fakeRunner records calls and writes scripted output.
type fakeRunner struct {
	resetGate chan struct{} // if set, Reset waits for it

	mu      sync.Mutex
	calls   []string
	output  string
	result  sandbox.Result
	block   bool // Run blocks until its context is canceled
	sizes   []sandbox.Size
	resizes []sandbox.Size
}

func (f *fakeRunner) Reset(context.Context) error {
	if f.resetGate != nil {
		<-f.resetGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "reset")
	return nil
}

func (f *fakeRunner) Run(ctx context.Context, cmd string, size sandbox.Size, out io.Writer) (sandbox.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "run "+cmd)
	f.sizes = append(f.sizes, size)
	block := f.block
	f.mu.Unlock()
	io.WriteString(out, f.output)
	if block {
		<-ctx.Done()
		return sandbox.Result{ExitCode: 137}, ctx.Err()
	}
	return f.result, nil
}

func (f *fakeRunner) Resize(_ context.Context, size sandbox.Size) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, size)
	return nil
}

type fakeScores struct {
	added []score.Entry
}

func (f *fakeScores) Add(e score.Entry) (int, []score.Entry, error) {
	f.added = append(f.added, e)
	return 1, []score.Entry{e}, nil
}

// clock is a controllable time source.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func testCatalog() []catalog.Challenge {
	var cs []catalog.Challenge
	for i, d := range []catalog.Difficulty{catalog.Easy, catalog.Medium, catalog.Hard} {
		cs = append(cs, catalog.Challenge{
			ID:            fmt.Sprintf("c%d", i),
			Command:       fmt.Sprintf("echo %d", i),
			Category:      "text",
			Difficulty:    d,
			Explanation:   "Prints a number. " + strings.Repeat("More words to wrap. ", 3),
			Deterministic: true,
		})
	}
	return cs
}

type harness struct {
	t      *testing.T
	m      *Model
	runner *fakeRunner
	scores *fakeScores
	clock  *clock
}

func newHarness(t *testing.T, st Settings) *harness {
	h := &harness{
		t:      t,
		runner: &fakeRunner{output: "hello\r\n\x1b[31mred\x1b[0m\r\n"},
		scores: &fakeScores{},
		clock:  &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)},
	}
	h.m = New(t.Context(), Config{
		Catalog:  testCatalog(),
		Defaults: st,
		Runner:   h.runner,
		Scores:   h.scores,
		Now:      h.clock.now,
		NewSeed:  func() uint64 { return 99 },
	})
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	return h
}

// send delivers msg and returns the resulting command without running it.
func (h *harness) send(msg tea.Msg) tea.Cmd {
	h.t.Helper()
	_, cmd := h.m.Update(msg)
	return cmd
}

// settle runs cmd and all commands it produces, except ticks (which
// sleep and would loop forever).
func (h *harness) settle(cmd tea.Cmd) {
	h.t.Helper()
	for cmd != nil {
		done := make(chan tea.Msg, 1)
		go func() { done <- cmd() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(3 * time.Second):
			h.t.Fatal("command did not finish")
		}
		switch msg := msg.(type) {
		case nil, tickMsg:
			return
		case tea.BatchMsg:
			for _, c := range msg {
				h.settle(c)
			}
			return
		default:
			cmd = h.send(msg)
		}
	}
}

func (h *harness) key(k string) tea.Cmd {
	h.t.Helper()
	var msg tea.KeyPressMsg
	switch k {
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		msg = tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "tab":
		msg = tea.KeyPressMsg{Code: tea.KeyTab}
	case "ctrl+c":
		msg = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "right":
		msg = tea.KeyPressMsg{Code: tea.KeyRight}
	case "down":
		msg = tea.KeyPressMsg{Code: tea.KeyDown}
	default:
		r := []rune(k)[0]
		msg = tea.KeyPressMsg{Code: r, Text: k}
	}
	return h.send(msg)
}

func (h *harness) typeText(s string) {
	h.t.Helper()
	for _, r := range s {
		h.clock.t = h.clock.t.Add(100 * time.Millisecond)
		h.key(string(r))
	}
}

// checkFits asserts that the view fits the terminal.
func (h *harness) checkFits() {
	h.t.Helper()
	content := h.m.View().Content
	lines := strings.Split(content, "\n")
	if len(lines) > h.m.height {
		h.t.Errorf("view has %d lines, terminal %d:\n%s", len(lines), h.m.height, content)
	}
	for i, l := range lines {
		if w := visibleWidth(l); w > h.m.width {
			h.t.Errorf("line %d is %d wide, terminal %d: %q", i+1, w, h.m.width, l)
		}
	}
}

func defaults() Settings {
	return Settings{Rounds: 3, Order: engine.OrderDifficulty, Seed: 5, FixedSeed: true}
}

func TestSetupPreselection(t *testing.T) {
	h := newHarness(t, Settings{Difficulty: catalog.Hard, Category: "text", Rounds: 7, Order: engine.OrderDifficulty})
	st := h.m.setup.settings()
	if st.Difficulty != catalog.Hard || st.Category != "text" || st.Rounds != 7 || st.Order != engine.OrderDifficulty {
		t.Errorf("settings %+v do not reflect the flags", st)
	}
	if n := h.m.setup.matching(); n != 1 {
		t.Errorf("matching = %d, want 1", n)
	}
	view := h.m.View().Content
	for _, want := range []string{"Reverse (coming soon)", "Only 1 commands match"} {
		if !strings.Contains(view, want) {
			t.Errorf("setup view lacks %q:\n%s", want, view)
		}
	}
	h.checkFits()
}

func TestSetupNavigation(t *testing.T) {
	h := newHarness(t, defaults())
	// Mode row: Reverse is disabled and cannot be selected.
	h.m.setup.cursor = rowMode
	h.key("right")
	if got := h.m.setup.rows[rowMode].value(); got != "Speed" {
		t.Errorf("mode = %q, want Speed", got)
	}
	h.key("down") // difficulty
	h.key("right")
	if got := h.m.setup.settings().Difficulty; got != catalog.Easy {
		t.Errorf("difficulty = %q, want easy", got)
	}
}

func TestSetupNoMatch(t *testing.T) {
	h := newHarness(t, Settings{Category: "git", Rounds: 5, Order: engine.OrderRandom})
	h.key("enter")
	if h.m.screen != screenSetup || h.m.setup.err == nil {
		t.Fatalf("session started without challenges (screen %d)", h.m.screen)
	}
}

func TestRoundLifecycle(t *testing.T) {
	h := newHarness(t, defaults())
	h.key("enter")
	r := &h.m.round
	if h.m.screen != screenRound || r.phase != phaseTyping || r.challenge.ID != "c0" {
		t.Fatalf("round not started: screen %d phase %d challenge %q", h.m.screen, r.phase, r.challenge.ID)
	}
	h.checkFits()

	// A typo: Enter does not submit, a hint appears.
	h.typeText("echo 1")
	if r.typing.Matches() {
		t.Fatal("typo matches")
	}
	h.key("enter")
	if r.phase != phaseTyping || r.hint == "" {
		t.Fatalf("enter without match: phase %d hint %q", r.phase, r.hint)
	}
	// Tab is no input.
	h.key("tab")
	if r.typing.Input() != "echo 1" {
		t.Fatalf("tab changed input: %q", r.typing.Input())
	}
	h.key("backspace")
	h.typeText("0")
	h.clock.t = h.clock.t.Add(time.Second)
	h.settle(h.key("enter"))

	if r.phase != phaseResult {
		t.Fatalf("phase %d after run, want result", r.phase)
	}
	if got := strings.Join(h.runner.calls, ","); got != "reset,run echo 0" {
		t.Errorf("runner calls %q", got)
	}
	if want := r.outputSize(); h.runner.sizes[0] != want || want.Width != 80 {
		t.Errorf("run size %v, want viewport size %v", h.runner.sizes[0], want)
	}
	if !strings.Contains(r.term.String(), "\x1b[31mred") {
		t.Errorf("colour lost in output %q", r.term.String())
	}
	recs := h.m.round.session.Records()
	if len(recs) != 1 {
		t.Fatalf("%d records", len(recs))
	}
	st := recs[0].Stats
	// "echo 1", backspace, "0": 7 keystrokes, the "1" was wrong.
	if st.Chars != 6 || st.Keystrokes != 7 || st.Errors != 1 || st.Duration != 1600*time.Millisecond {
		t.Errorf("round stats %+v", st)
	}
	view := h.m.View().Content
	for _, want := range []string{"Round 1/3", "exit 0", "Prints a number.", "enter next round"} {
		if !strings.Contains(view, want) {
			t.Errorf("result view lacks %q:\n%s", want, view)
		}
	}
	h.checkFits()

	h.key("enter")
	if r.phase != phaseTyping || r.challenge.ID != "c1" || r.number != 2 {
		t.Errorf("next round: phase %d challenge %q number %d", r.phase, r.challenge.ID, r.number)
	}
}

func TestSkipAndSummary(t *testing.T) {
	h := newHarness(t, defaults())
	h.key("enter")
	h.key("esc") // skip c0
	if h.m.round.challenge.ID != "c1" {
		t.Fatalf("esc did not skip: %q", h.m.round.challenge.ID)
	}
	h.typeText("echo 1")
	h.settle(h.key("enter"))
	h.key("enter")
	h.key("esc") // skip c2: session done
	if h.m.screen != screenSummary {
		t.Fatalf("screen %d, want summary", h.m.screen)
	}
	if len(h.scores.added) != 0 {
		t.Fatalf("%d highscores added; sessions with skipped rounds are not eligible", len(h.scores.added))
	}
	view := h.m.View().Content
	for _, want := range []string{"skipped", "2 round(s) skipped"} {
		if !strings.Contains(view, want) {
			t.Errorf("summary lacks %q:\n%s", want, view)
		}
	}
	h.checkFits()
	h.key("enter")
	if h.m.screen != screenSetup {
		t.Errorf("enter on summary: screen %d, want setup", h.m.screen)
	}
}

func TestCompleteSessionRecorded(t *testing.T) {
	h := newHarness(t, Settings{Rounds: 2, Order: engine.OrderDifficulty, Seed: 1, FixedSeed: true})
	h.key("enter")
	for range 2 {
		if h.m.round.phase == phaseResult {
			h.key("enter")
		}
		h.typeText(h.m.round.challenge.Command)
		h.settle(h.key("enter"))
	}
	// Recorded as soon as the last round completes, before the summary is
	// opened, so quitting on the last result screen keeps the score.
	if len(h.scores.added) != 1 {
		t.Fatalf("%d highscores added, want 1", len(h.scores.added))
	}
	e := h.scores.added[0]
	if e.Rounds != 2 || e.Mode != "speed" || e.Difficulty != "all" || e.Category != "all" || e.Order != "difficulty" || e.Score <= 0 {
		t.Errorf("entry %+v", e)
	}
	h.key("enter")
	if h.m.screen != screenSummary || len(h.scores.added) != 1 {
		t.Fatalf("screen %d, %d highscores added", h.m.screen, len(h.scores.added))
	}
	view := h.m.View().Content
	for _, want := range []string{"New highscore: place 1", "Highscores"} {
		if !strings.Contains(view, want) {
			t.Errorf("summary lacks %q:\n%s", want, view)
		}
	}
	h.checkFits()
}

func TestAllSkippedNotRecorded(t *testing.T) {
	h := newHarness(t, Settings{Rounds: 1, Order: engine.OrderRandom})
	h.key("enter")
	h.key("esc")
	if h.m.screen != screenSummary || len(h.scores.added) != 0 {
		t.Errorf("screen %d, %d highscores added", h.m.screen, len(h.scores.added))
	}
}

func TestCancelRunningCommand(t *testing.T) {
	h := newHarness(t, defaults())
	h.runner.block = true
	h.key("enter")
	h.typeText("echo 0")
	wait := h.key("enter")
	if h.m.round.phase != phaseRunning {
		t.Fatalf("phase %d, want running", h.m.round.phase)
	}
	h.key("esc")
	h.settle(wait)
	r := &h.m.round
	if r.phase != phaseResult || !r.canceled || r.runErr != nil {
		t.Errorf("phase %d canceled %v err %v", r.phase, r.canceled, r.runErr)
	}
	if !strings.Contains(h.m.View().Content, "[canceled]") {
		t.Error("result does not show [canceled]")
	}
}

func TestResizeWhileRunning(t *testing.T) {
	h := newHarness(t, defaults())
	h.runner.block = true
	h.key("enter")
	h.typeText("echo 0")
	wait := h.key("enter")
	h.waitForRun()
	h.settle(h.send(tea.WindowSizeMsg{Width: 100, Height: 30}))
	h.runner.mu.Lock()
	resizes := h.runner.resizes
	h.runner.mu.Unlock()
	if len(resizes) != 1 || resizes[0].Width != 100 || resizes[0].Height != uint(h.m.round.output.Height()) {
		t.Errorf("resizes %v, want one to 100x%d", resizes, h.m.round.output.Height())
	}
	h.checkFits()
	h.key("esc")
	h.settle(wait)
}

// waitForRun waits until the fake runner's Run was called.
func (h *harness) waitForRun() {
	h.t.Helper()
	for range 200 {
		h.runner.mu.Lock()
		n := len(h.runner.sizes)
		h.runner.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatal("Run was not called")
}

func TestResizeDuringReset(t *testing.T) {
	h := newHarness(t, defaults())
	h.runner.resetGate = make(chan struct{})
	h.key("enter")
	h.typeText("echo 0")
	wait := h.key("enter")
	h.settle(h.send(tea.WindowSizeMsg{Width: 100, Height: 30}))
	close(h.runner.resetGate)
	h.settle(wait)
	if got, want := h.runner.sizes[0], h.m.round.outputSize(); got != want || want.Width != 100 {
		t.Errorf("command started with %v, want the size after the resize %v", got, want)
	}
}

func TestOverflowKeepsLayout(t *testing.T) {
	h := newHarness(t, defaults())
	h.m.cfg.Catalog = []catalog.Challenge{{ID: "long", Command: "echo " + strings.Repeat("a", 71),
		Category: "text", Difficulty: catalog.Easy, Explanation: "x", Deterministic: true}}
	h.m.setup.catalog = h.m.cfg.Catalog
	h.key("enter")
	h.typeText("echo " + strings.Repeat("a", 71) + "zzzzzzzzzz")
	h.checkFits()
}

func TestTooSmall(t *testing.T) {
	h := newHarness(t, defaults())
	h.send(tea.WindowSizeMsg{Width: 59, Height: 24})
	if !strings.Contains(h.m.View().Content, "terminal too small") {
		t.Error("no hint at 59x24")
	}
	h.send(tea.WindowSizeMsg{Width: 60, Height: 16})
	if strings.Contains(h.m.View().Content, "terminal too small") {
		t.Error("hint at the minimum size")
	}
	h.checkFits()
	h.key("enter")
	h.checkFits()
}

func TestCtrlCQuits(t *testing.T) {
	h := newHarness(t, defaults())
	h.key("enter")
	cmd := h.key("ctrl+c")
	if cmd == nil {
		t.Fatal("no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("ctrl+c does not quit")
	}
}

func TestWrapHelpers(t *testing.T) {
	tests := []struct {
		text  string
		width int
		want  []string
	}{
		{"a bb ccc", 4, []string{"a bb", "ccc"}},
		{"abcdefgh", 3, []string{"abc", "def", "gh"}},
		{"", 5, nil},
	}
	for _, tt := range tests {
		got := wrapText(tt.text, tt.width)
		if strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("wrapText(%q, %d) = %q, want %q", tt.text, tt.width, got, tt.want)
		}
	}
	lines := wrapRunes([]rune("ls -l /var/log | grep x"), 10)
	var parts []string
	for _, l := range lines {
		var b strings.Builder
		for _, i := range l {
			b.WriteRune([]rune("ls -l /var/log | grep x")[i])
		}
		parts = append(parts, b.String())
	}
	if got := strings.Join(parts, "¦"); got != "ls -l ¦/var/log ¦| grep x" {
		t.Errorf("wrapRunes = %q", got)
	}
}
