package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"shast/internal/engine"
	"shast/internal/score"
)

// summaryModel shows the session results and the highscores.
type summaryModel struct {
	session *engine.Session
	records []engine.RoundRecord
	stats   score.Session
	rank    int           // 1-based place in the highscores, 0 if none
	top     []score.Entry // highscores after this session
	skipped int           // rounds skipped by the player
	saved   bool          // the session was eligible for the highscores
	err     error         // saving the highscore failed
	view    viewport.Model
}

// newSummary evaluates the finished session and stores its highscore
// entry. Only sessions in which every round was played are eligible:
// skipped rounds do not count towards the stats, so skipping hard commands
// would otherwise inflate the score.
func newSummary(s *engine.Session, st Settings, scores Scores, now time.Time) (sm summaryModel) {
	sm = summaryModel{session: s, records: s.Records(), stats: s.Stats(), view: viewport.New()}
	sm.view.MouseWheelEnabled = false
	sm.view.SoftWrap = true
	for _, r := range sm.records {
		if r.Skipped {
			sm.skipped++
		}
	}
	defer func() { sm.view.SetContent(sm.content()) }()
	if sm.skipped > 0 || scores == nil {
		return sm
	}
	sm.saved = true
	sm.rank, sm.top, sm.err = scores.Add(score.Entry{
		Score:      sm.stats.Score(),
		WPM:        sm.stats.WPM(),
		Accuracy:   sm.stats.Accuracy(),
		Errors:     sm.stats.Errors(),
		Rounds:     len(sm.stats.Rounds),
		Duration:   sm.stats.Duration(),
		Mode:       s.Mode().Name(),
		Difficulty: orAll(string(st.Difficulty)),
		Category:   orAll(st.Category),
		Order:      string(st.Order),
		At:         now,
	})
	return sm
}

func orAll(s string) string {
	if s == "" {
		return allOption
	}
	return s
}

func (m *Model) updateSummary(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "enter":
		m.screen = screenSetup
	case "q", "esc":
		return tea.Quit
	case "up", "k":
		m.summary.view.ScrollUp(1)
	case "down", "j":
		m.summary.view.ScrollDown(1)
	case "pgup", "b":
		m.summary.view.PageUp()
	case "pgdown", "f", "space":
		m.summary.view.PageDown()
	}
	return nil
}

func (m *Model) viewSummary() string {
	s := &m.summary
	s.view.SetWidth(m.width)
	s.view.SetHeight(max(1, m.height-2))
	return styleTitle.Render("Session summary") + "\n" +
		s.view.View() + "\n" +
		styleFaint.Render("enter new session · ↑/↓ pgup/pgdn scroll · q quit")
}

func (s *summaryModel) content() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%-3s %-26s %5s %6s %6s %7s %5s\n", "#", "command", "WPM", "acc", "errors", "time", "exit")
	for i, r := range s.records {
		id := truncate(r.Challenge.ID, 26)
		if r.Skipped {
			fmt.Fprintf(&b, "%-3d %-26s %s\n", i+1, id, styleFaint.Render("skipped"))
			continue
		}
		exit := strconv.Itoa(r.Result.ExitCode)
		if r.Result.ExitCode == noExitCode {
			exit = "–"
		}
		fmt.Fprintf(&b, "%-3d %-26s %5.0f %5.0f%% %6d %6.1fs %5s\n", i+1, id,
			r.Stats.WPM(), r.Stats.Accuracy()*100, r.Stats.Errors, r.Stats.Duration.Seconds(), exit)
	}
	st := s.stats
	fmt.Fprintf(&b, "\n%s WPM %.0f · accuracy %.0f%% · errors %d · typing time %.1fs · score %.1f\n",
		styleLabel.Render("Session:"), st.WPM(), st.Accuracy()*100, st.Errors(), st.Duration().Seconds(), st.Score())

	switch {
	case s.skipped > 0:
		b.WriteString(styleFaint.Render(fmt.Sprintf("%d round(s) skipped – only complete sessions enter the highscores.", s.skipped)) + "\n")
	case !s.saved:
		b.WriteString(styleFaint.Render("Highscores are not recorded.") + "\n")
	case s.err != nil:
		b.WriteString(styleError.Render("Could not save the highscore: "+s.err.Error()) + "\n")
	case s.rank > 0:
		b.WriteString(styleCorrect.Render(fmt.Sprintf("New highscore: place %d!", s.rank)) + "\n")
	default:
		b.WriteString(styleFaint.Render(fmt.Sprintf("Not in the top %d this time.", score.MaxEntries)) + "\n")
	}

	if len(s.top) > 0 {
		fmt.Fprintf(&b, "\n%s\n", styleLabel.Render("Highscores"))
		for i, e := range s.top {
			line := fmt.Sprintf("%2d. %6.1f  %3.0f WPM %4.0f%%  %-6s %-14s %-10s %s",
				i+1, e.Score, e.WPM, e.Accuracy*100, e.Difficulty, e.Category, e.Order, e.At.Local().Format("2006-01-02"))
			if i+1 == s.rank {
				line = styleAccent.Render(line)
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}
