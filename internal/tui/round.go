package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"shast/internal/catalog"
	"shast/internal/cmdhelp"
	"shast/internal/engine"
	"shast/internal/outcmp"
	"shast/internal/sandbox"
	"shast/internal/score"
	"shast/internal/typing"
)

type phase int

const (
	phaseTyping  phase = iota
	phaseRunning       // command executes, output streams in
	phaseResult        // exit code and explanation shown
)

const (
	tickInterval = 100 * time.Millisecond
	// noExitCode marks a command stopped by the player.
	noExitCode   = -1
	hintDuration = 2 * time.Second
	// maxExplanationLines bounds the explanation below the output.
	maxExplanationLines = 3
)

// roundModel is the state of the current round.
type roundModel struct {
	session  *engine.Session
	settings Settings

	challenge catalog.Challenge // of the round on screen
	number    int               // 1-based round number on screen
	phase     phase
	typing    *typing.State
	now       time.Time // time of the last tick or keystroke, for live stats
	hint      string
	hintAt    time.Time
	stats     score.Round

	help        cmdhelp.Help // of the command, shown while typing
	toolLines   int          // lines reserved for the tool help (0 or 1)
	optionLines int          // lines reserved for the option help

	gen      int // identifies the current run; stale messages are dropped
	stream   *stream
	cancel   context.CancelFunc
	term     *term
	raw      bytes.Buffer // raw output, for judging
	output   viewport.Model
	result   sandbox.Result
	runErr   error
	runSize  sandbox.Size // TTY size of the running command
	canceled bool
	ticking  bool // a tick loop is running
}

// tickMsg refreshes the live statistics while typing.
type tickMsg struct {
	gen int
	at  time.Time
}

// start begins the current round of the session.
func (r *roundModel) start(m *Model) tea.Cmd {
	r.challenge = r.session.Current()
	r.number = r.session.Round()
	r.phase = phaseTyping
	r.typing = typing.New(r.challenge.Command)
	r.help = m.cfg.Help.Annotate(r.challenge.Command)
	r.hint = ""
	r.stats = score.Round{}
	r.term = nil
	r.raw.Reset()
	r.result = sandbox.Result{}
	r.runErr = nil
	r.canceled = false
	r.ticking = false
	r.gen++
	r.output = viewport.New()
	r.output.SoftWrap = true
	r.output.MouseWheelEnabled = false
	r.layout(m)
	return nil
}

func (m *Model) updateRound(msg tea.Msg) tea.Cmd {
	r := &m.round
	switch msg := msg.(type) {
	case tickMsg:
		if msg.gen != r.gen || r.phase != phaseTyping {
			return nil
		}
		r.now = msg.at
		return r.tick()
	case streamMsg:
		return r.handleStream(m, msg)
	case tea.KeyPressMsg:
		switch r.phase {
		case phaseTyping:
			return r.handleTypingKey(m, msg)
		case phaseRunning:
			if msg.String() == "esc" {
				r.canceled = true
				r.cancelRun()
				return nil
			}
			r.scroll(msg)
		case phaseResult:
			switch msg.String() {
			case "enter", "space":
				return m.nextRound()
			default:
				r.scroll(msg)
			}
		}
	}
	return nil
}

func (r *roundModel) tick() tea.Cmd {
	gen := r.gen
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg{gen: gen, at: t} })
}

func (r *roundModel) handleTypingKey(m *Model, key tea.KeyPressMsg) tea.Cmd {
	now := m.cfg.Now()
	r.now = now
	switch key.String() {
	case "esc":
		r.session.Skip()
		return m.nextRound()
	case "enter":
		if !r.session.Mode().CanSubmit(r.challenge, r.typing.Input()) {
			r.hint, r.hintAt = "no exact match yet", now
			return r.startTicking() // lets the hint expire
		}
		return r.submit(m, now)
	case "backspace":
		r.typing.Backspace()
		r.layout(m)
		return nil
	case "tab":
		return nil // not input
	}
	if key.Text == "" || key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) != 0 {
		return nil
	}
	r.hint = ""
	for _, ch := range key.Text {
		r.typing.Type(ch, now)
	}
	r.layout(m) // overflow input may need another target line
	return r.startTicking()
}

// startTicking starts the tick loop of the round unless it runs already.
func (r *roundModel) startTicking() tea.Cmd {
	if r.ticking {
		return nil
	}
	r.ticking = true
	return r.tick()
}

// submit records the typing statistics and starts the command.
func (r *roundModel) submit(m *Model, now time.Time) tea.Cmd {
	c := r.challenge
	r.stats = score.Round{
		ChallengeID: c.ID,
		Chars:       utf8.RuneCountInString(c.Command),
		Keystrokes:  r.typing.Keystrokes(),
		Errors:      r.typing.Errors(),
		Duration:    now.Sub(r.typing.StartedAt()),
	}
	r.phase = phaseRunning
	r.gen++
	r.layout(m)

	ctx, cancel := context.WithCancel(m.ctx)
	r.cancel = cancel
	r.runSize = r.outputSize()
	r.term = newTerm(int(r.runSize.Width))
	r.stream = newStream(r.runSize)
	go r.stream.run(ctx, m.cfg.Runner, c.Command)
	return r.stream.wait(r.gen)
}

func (r *roundModel) handleStream(m *Model, msg streamMsg) tea.Cmd {
	if msg.gen != r.gen || r.phase != phaseRunning {
		return nil
	}
	if len(msg.data) > 0 {
		atBottom := r.output.AtBottom()
		r.term.Write(msg.data)
		r.raw.Write(msg.data)
		// The viewport keeps the slice; term only changes lines it hands
		// out again with the next call.
		r.output.SetContentLines(r.term.Lines())
		if atBottom {
			r.output.GotoBottom()
		}
	}
	if !msg.done {
		return r.stream.wait(r.gen)
	}
	r.cancelRun()
	r.result, r.runErr = msg.res, msg.err
	if errors.Is(r.runErr, context.Canceled) {
		r.runErr = nil
		r.canceled = true
	}
	if r.canceled {
		r.result.ExitCode = noExitCode // the command did not end by itself
	}
	r.phase = phaseResult
	verdict := r.session.Mode().Judge(r.challenge, r.typing.Input(), outcmp.Normalize(r.raw.String()), r.result)
	r.session.Complete(r.stats, r.result, verdict)
	if r.session.Done() {
		m.prepareSummary() // record the highscore even if the player quits now
	}
	r.layout(m)
	return nil
}

// cancelRun stops a running command, if any.
func (r *roundModel) cancelRun() {
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
}

// nextRound advances to the next round or to the summary.
func (m *Model) nextRound() tea.Cmd {
	if m.round.session.Done() {
		m.finishSession()
		return nil
	}
	return m.round.start(m)
}

func (r *roundModel) scroll(key tea.KeyPressMsg) {
	switch key.String() {
	case "up", "k":
		r.output.ScrollUp(1)
	case "down", "j":
		r.output.ScrollDown(1)
	case "pgup", "b":
		r.output.PageUp()
	case "pgdown", "f":
		r.output.PageDown()
	case "home", "g":
		r.output.GotoTop()
	case "end", "G":
		r.output.GotoBottom()
	}
}

// resize relayouts the round and resizes the TTY of a running command.
func (r *roundModel) resize(m *Model) tea.Cmd {
	if r.session == nil || m.screen != screenRound {
		return nil
	}
	r.layout(m)
	if r.phase != phaseRunning || m.width < minWidth || m.height < minHeight {
		return nil
	}
	size := r.outputSize()
	if size == r.runSize {
		return nil
	}
	r.runSize = size
	r.term.setWidth(int(size.Width))
	s, runner, ctx := r.stream, m.cfg.Runner, m.ctx
	s.setSize(size)
	return func() tea.Msg {
		// A failed resize only affects the layout of the output; the
		// command itself keeps running, so the error is not surfaced.
		_ = s.applySize(ctx, runner)
		return nil
	}
}

// Layout: header, blank, [tool help], target, [option help], blank, stats,
// separator, output, separator, result (status + explanation), footer. The
// help lines exist only while typing.
func (r *roundModel) targetWidth(m *Model) int { return max(1, m.width-4) }

func (r *roundModel) explanationLines(m *Model) []string {
	lines := wrapText(r.challenge.Explanation, m.width-2)
	if len(lines) > maxExplanationLines {
		lines = lines[:maxExplanationLines]
		lines[maxExplanationLines-1] = truncate(lines[maxExplanationLines-1], m.width-3) + "…"
	}
	return lines
}

func (r *roundModel) layout(m *Model) {
	if r.typing == nil {
		return
	}
	targetLines := len(wrapRunes(r.targetRunes(), r.targetWidth(m)))
	fixed := 1 + 1 + targetLines + 1 + 1 + 1 + 1 + 1 + len(r.explanationLines(m)) + 1
	r.toolLines, r.optionLines = 0, 0
	if r.phase == phaseTyping && m.cfg.Help != nil {
		// The output keeps at least one line; the tool line comes first.
		free := max(0, m.height-fixed-1)
		r.toolLines = min(1, free)
		r.optionLines = min(r.help.MaxOptions(), free-r.toolLines)
		fixed += r.toolLines + r.optionLines
	}
	follow := r.output.AtBottom()
	r.output.SetWidth(max(1, m.width))
	r.output.SetHeight(max(1, m.height-fixed))
	if follow {
		r.output.GotoBottom()
	}
}

func (r *roundModel) outputSize() sandbox.Size {
	return sandbox.Size{Width: uint(r.output.Width()), Height: uint(r.output.Height())}
}

// targetRunes returns the target followed by overflow input.
func (r *roundModel) targetRunes() []rune {
	target := []rune(r.typing.Target())
	input := []rune(r.typing.Input())
	if len(input) > len(target) {
		return append(target, input[len(target):]...)
	}
	return target
}

func (m *Model) viewRound() string {
	r := &m.round
	c := r.challenge
	var b strings.Builder
	header := fmt.Sprintf("Round %d/%d · %s · %s", r.number, r.session.Total(), c.Category, c.Difficulty)
	b.WriteString(styleTitle.Render(header) + "\n\n")
	if r.toolLines > 0 {
		b.WriteString(r.renderToolHelp(m) + "\n")
	}
	b.WriteString(r.renderTarget(m) + "\n")
	if r.optionLines > 0 {
		b.WriteString(r.renderOptionHelp(m) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(r.renderStats(m) + "\n")
	b.WriteString(separator("output", m.width) + "\n")
	b.WriteString(r.output.View() + "\n")
	b.WriteString(separator("", m.width) + "\n")
	b.WriteString(r.renderResult(m) + "\n")
	b.WriteString(styleFaint.Render(r.footer()))
	return b.String()
}

func (r *roundModel) renderTarget(m *Model) string {
	statuses := r.typing.Statuses()
	cursor := utf8.RuneCountInString(r.typing.Input())
	runes := r.targetRunes()
	var lines []string
	for _, line := range wrapRunes(runes, r.targetWidth(m)) {
		var b strings.Builder
		for _, i := range line {
			ch := string(runes[i])
			var st = stylePending
			switch {
			case i >= len(statuses): // overflow beyond the target
				st = styleWrong
				if runes[i] == ' ' {
					st = styleWrongSpace
				}
			case statuses[i] == typing.Correct:
				st = styleCorrect
			case statuses[i] == typing.Wrong:
				st = styleWrong
				if runes[i] == ' ' {
					st = styleWrongSpace
				}
			}
			if i == cursor && r.phase == phaseTyping {
				st = st.Reverse(true)
			}
			b.WriteString(st.Render(ch))
		}
		if len(line) > 0 && line[len(line)-1] == len(runes)-1 && cursor == len(runes) && r.phase == phaseTyping {
			b.WriteString(styleCursor.Render(" "))
		}
		lines = append(lines, "  "+b.String())
	}
	return strings.Join(lines, "\n")
}

// cursor is the position of the next character to type.
func (r *roundModel) cursor() int { return utf8.RuneCountInString(r.typing.Input()) }

// renderToolHelp describes the command the cursor is in.
func (r *roundModel) renderToolHelp(m *Model) string {
	tool, ok := r.help.Tool(r.cursor())
	if !ok {
		return ""
	}
	return helpLine(tool.Name, len(tool.Name), tool.Desc, m.width)
}

// renderOptionHelp describes the options at the cursor, one per reserved
// line; lines without an option stay empty.
func (r *roundModel) renderOptionHelp(m *Model) string {
	items := r.help.Options(r.cursor())
	items = items[:min(len(items), r.optionLines)]
	nameWidth := 0
	for _, it := range items {
		nameWidth = max(nameWidth, len(it.Name))
	}
	lines := make([]string, r.optionLines)
	for i, it := range items {
		lines[i] = helpLine(it.Name, nameWidth, it.Desc, m.width)
	}
	return strings.Join(lines, "\n")
}

// helpLine renders "name  -- description" like zsh completions, indented
// like the target and cut to width.
func helpLine(name string, nameWidth int, desc string, width int) string {
	name = fmt.Sprintf("%-*s", nameWidth, name)
	rest := "  -- " + desc
	if avail := width - 2 - len(name); utf8.RuneCountInString(rest) > avail {
		rest = truncate(rest, avail-1) + "…"
	}
	return "  " + styleHelpName.Render(name) + styleHelpDesc.Render(rest)
}

func (r *roundModel) renderStats(m *Model) string {
	var elapsed time.Duration
	chars, keys, errs := 0, 0, 0
	if r.phase == phaseTyping {
		if r.typing.Started() {
			elapsed = max(0, r.now.Sub(r.typing.StartedAt()))
		}
		chars = utf8.RuneCountInString(r.typing.Input())
		keys, errs = r.typing.Keystrokes(), r.typing.Errors()
	} else {
		elapsed, chars, keys, errs = r.stats.Duration, r.stats.Chars, r.stats.Keystrokes, r.stats.Errors
	}
	live := score.Round{Chars: chars, Keystrokes: keys, Errors: errs, Duration: elapsed}
	acc := "–"
	if keys > 0 {
		acc = fmt.Sprintf("%.0f%%", live.Accuracy()*100)
	}
	stats := fmt.Sprintf("WPM %3.0f · Accuracy %s · Errors %d · %.1fs", live.WPM(), acc, errs, elapsed.Seconds())
	if r.hint != "" && r.now.Sub(r.hintAt) < hintDuration {
		return styleLabel.Render(stats) + "  " + styleWarn.Render(truncate(r.hint, max(0, m.width-visibleWidth(stats)-2)))
	}
	return styleLabel.Render(stats)
}

func (r *roundModel) renderResult(m *Model) string {
	explanation := r.explanationLines(m)
	lines := make([]string, 1+len(explanation))
	switch r.phase {
	case phaseRunning:
		lines[0] = styleFaint.Render("running…")
	case phaseResult:
		lines[0] = r.status()
		for i, l := range explanation {
			lines[i+1] = styleExplain.Render(l)
		}
	}
	return strings.Join(lines, "\n")
}

// status summarizes how the command ended.
func (r *roundModel) status() string {
	if r.runErr != nil {
		return styleError.Render("sandbox error: " + r.runErr.Error())
	}
	var parts []string
	exit := fmt.Sprintf("exit %d", r.result.ExitCode)
	switch {
	case r.canceled:
		parts = append(parts, styleWarn.Render("[canceled]"))
	case r.result.ExitCode == 0:
		parts = append(parts, styleCorrect.Render(exit))
	default:
		parts = append(parts, styleWrong.Render(exit))
	}
	parts = append(parts, fmt.Sprintf("%.2fs", r.result.Duration.Seconds()))
	if r.result.TimedOut {
		parts = append(parts, styleWarn.Render("[timeout]"))
	}
	if r.result.Truncated {
		parts = append(parts, styleWarn.Render("[truncated]"))
	}
	return strings.Join(parts, " · ")
}

func (r *roundModel) footer() string {
	switch r.phase {
	case phaseTyping:
		return "type the command · enter run · esc skip round · ctrl+c quit"
	case phaseRunning:
		return "↑/↓ pgup/pgdn scroll · esc stop command · ctrl+c quit"
	default:
		if r.session.Done() {
			return "enter summary · ↑/↓ pgup/pgdn scroll · ctrl+c quit"
		}
		return "enter next round · ↑/↓ pgup/pgdn scroll · ctrl+c quit"
	}
}
