package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"shast/internal/catalog"
	"shast/internal/engine"
)

// allOption is the setup value that disables a filter.
const allOption = "all"

// roundChoices are the offered session lengths.
var roundChoices = []int{5, 10, 20}

// setupRow is one adjustable setting.
type setupRow struct {
	label    string
	options  []string
	selected int
	disabled map[int]bool // options shown but not selectable
}

func (r *setupRow) value() string { return r.options[r.selected] }

// move changes the selection by delta, skipping disabled options.
func (r *setupRow) move(delta int) {
	for i := r.selected + delta; i >= 0 && i < len(r.options); i += delta {
		if !r.disabled[i] {
			r.selected = i
			return
		}
	}
}

const (
	rowMode = iota
	rowDifficulty
	rowCategory
	rowRounds
	rowOrder
)

// setupModel is the start menu, preselected from the CLI flags.
type setupModel struct {
	rows    []setupRow
	cursor  int
	catalog []catalog.Challenge
	seed    uint64
	fixed   bool
	err     error // why the last start failed
}

func newSetup(cs []catalog.Challenge, d Settings) setupModel {
	difficulties := []string{allOption}
	for _, x := range catalog.Difficulties() {
		difficulties = append(difficulties, string(x))
	}
	categories := append([]string{allOption}, catalog.Categories()...)
	rounds := slices.Clone(roundChoices)
	if d.Rounds > 0 && !slices.Contains(rounds, d.Rounds) {
		rounds = append(rounds, d.Rounds)
		slices.Sort(rounds)
	}
	roundOpts := make([]string, len(rounds))
	for i, r := range rounds {
		roundOpts[i] = strconv.Itoa(r)
	}
	var orders []string
	for _, o := range engine.Orders() {
		orders = append(orders, string(o))
	}

	rows := []setupRow{
		rowMode:       {label: "Mode", options: []string{"Speed", "Reverse (coming soon)"}, disabled: map[int]bool{1: true}},
		rowDifficulty: {label: "Difficulty", options: difficulties},
		rowCategory:   {label: "Category", options: categories},
		rowRounds:     {label: "Rounds", options: roundOpts},
		rowOrder:      {label: "Order", options: orders},
	}
	preselect(&rows[rowDifficulty], string(d.Difficulty))
	preselect(&rows[rowCategory], d.Category)
	preselect(&rows[rowRounds], strconv.Itoa(d.Rounds))
	preselect(&rows[rowOrder], string(d.Order))
	if d.Rounds <= 0 {
		preselect(&rows[rowRounds], "10")
	}
	return setupModel{rows: rows, cursor: rowDifficulty, catalog: cs, seed: d.Seed, fixed: d.FixedSeed}
}

func preselect(r *setupRow, v string) {
	if i := slices.Index(r.options, v); i >= 0 {
		r.selected = i
	}
}

// settings returns the selected settings.
func (s setupModel) settings() Settings {
	st := Settings{Order: engine.Order(s.rows[rowOrder].value()), Seed: s.seed, FixedSeed: s.fixed}
	if v := s.rows[rowDifficulty].value(); v != allOption {
		st.Difficulty = catalog.Difficulty(v)
	}
	if v := s.rows[rowCategory].value(); v != allOption {
		st.Category = v
	}
	st.Rounds, _ = strconv.Atoi(s.rows[rowRounds].value())
	return st
}

// matching returns how many challenges the selected filters leave.
func (s setupModel) matching() int {
	st := s.settings()
	f := catalog.Filter{Difficulty: st.Difficulty, Category: st.Category}
	n := 0
	for _, c := range s.catalog {
		if (engine.Speed{}).Eligible(c) && f.Match(c) {
			n++
		}
	}
	return n
}

func (m *Model) updateSetup(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	s := &m.setup
	switch key.String() {
	case "up", "k":
		s.cursor = max(0, s.cursor-1)
	case "down", "j", "tab":
		s.cursor = min(len(s.rows)-1, s.cursor+1)
	case "left", "h":
		s.rows[s.cursor].move(-1)
	case "right", "l":
		s.rows[s.cursor].move(1)
	case "enter":
		return m.startSession()
	case "q", "esc":
		return tea.Quit
	}
	return nil
}

func (m *Model) viewSetup() string {
	s := m.setup
	var b strings.Builder
	b.WriteString(styleTitle.Render("shast") + styleFaint.Render(" · type real shell commands, watch them run") + "\n\n")
	for i, r := range s.rows {
		pointer := "  "
		if i == s.cursor {
			pointer = styleAccent.Render("> ")
		}
		b.WriteString(pointer + styleLabel.Render(fmt.Sprintf("%-11s", r.label)))
		var opts []string
		for j, o := range r.options {
			switch {
			case r.disabled[j]:
				opts = append(opts, styleFaint.Render(o))
			case j == r.selected:
				opts = append(opts, styleSelected.Render(o))
			default:
				opts = append(opts, o)
			}
		}
		b.WriteString(wrapJoin(opts, "  ", m.width-15, 15) + "\n")
	}

	st := s.settings()
	n := s.matching()
	b.WriteString("\n")
	switch {
	case n == 0:
		b.WriteString(styleError.Render("No command matches these filters.") + "\n")
	case n < st.Rounds:
		b.WriteString(styleWarn.Render(fmt.Sprintf("Only %d commands match – the session will have %d rounds.", n, n)) + "\n")
	default:
		b.WriteString(styleFaint.Render(fmt.Sprintf("%d commands match.", n)) + "\n")
	}
	if s.fixed {
		b.WriteString(styleFaint.Render(fmt.Sprintf("Seed %d: the order is reproducible.", s.seed)) + "\n")
	}
	if s.err != nil && n > 0 { // with no match the hint above says it all
		b.WriteString(styleError.Render(s.err.Error()) + "\n")
	}
	b.WriteString("\n" + styleFaint.Render("↑/↓ select · ←/→ change · enter start · q quit"))
	return b.String()
}

// wrapJoin joins items with sep, breaking lines so they fit width and
// indenting continuation lines by indent spaces.
func wrapJoin(items []string, sep string, width, indent int) string {
	var b strings.Builder
	col := 0
	for i, it := range items {
		w := visibleWidth(it)
		if i > 0 {
			if col+len(sep)+w > width {
				b.WriteString("\n" + strings.Repeat(" ", indent))
				col = 0
			} else {
				b.WriteString(sep)
				col += len(sep)
			}
		}
		b.WriteString(it)
		col += w
	}
	return b.String()
}
