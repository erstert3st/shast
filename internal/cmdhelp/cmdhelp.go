// Package cmdhelp explains a shell command line while it is typed: which
// command the cursor is in and what the option under the cursor does. The
// descriptions come from a dictionary (help.yaml); the command line is
// parsed with a real shell parser, so pipes, command substitutions and
// quoted programs (awk, jq) are attributed correctly.
package cmdhelp

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// numOption is the dictionary key for numeric options such as head -5.
const numOption = "-NUM"

// Command is the help of one command or subcommand.
type Command struct {
	About   string
	Options map[string]string // option ("-s", "--max-depth", "-NUM") → description
}

// Dict maps command names ("du", "git log") to their help.
type Dict map[string]Command

// Item is a described part of a command line: a command or an option. It
// covers the bytes [Start, End) of the command line.
type Item struct {
	Name  string
	Desc  string
	Start int
	End   int
}

// word is an option word with the options it contains (one for -type or
// --max-depth=1, several for -sh).
type word struct {
	start, end int
	items      []Item
}

// Help is the annotated command line. The zero value has no help.
type Help struct {
	calls []Item // one per simple command; Name is empty for unknown tools
	words []word
}

// wrappers run the command given in their arguments.
var wrappers = map[string]bool{"xargs": true, "timeout": true}

// findExec are the find actions that run the following arguments up to ";"
// or "+" as a command.
var findExec = map[string]bool{"-exec": true, "-execdir": true, "-ok": true, "-okdir": true}

// Annotate attributes every command and option of cmdline to its dictionary
// entry. A command line that does not parse has no help.
func (d Dict) Annotate(cmdline string) Help {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(cmdline), "")
	if err != nil {
		return Help{}
	}
	var h Help
	syntax.Walk(f, func(n syntax.Node) bool {
		st, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		call, ok := st.Cmd.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		// Redirections belong to the command, e.g. wc -l < "$f".
		end := offset(call.End())
		for _, r := range st.Redirs {
			end = max(end, offset(r.End()))
		}
		d.call(&h, call.Args, end)
		return true
	})
	return h
}

// call annotates the simple command args, which ends at byte end.
func (d Dict) call(h *Help, args []*syntax.Word, end int) {
	name, n := d.toolName(args)
	cmd, known := d[name]
	tool := Item{Start: offset(args[0].Pos()), End: end}
	if known {
		tool.Name, tool.Desc = name, cmd.About
	}
	h.calls = append(h.calls, tool)
	for i := n; i < len(args); i++ {
		w := args[i]
		lit := w.Lit()
		switch {
		case findExec[lit] && name == "find":
			h.addWord(w, cmd)
			j := i + 1
			for j < len(args) && !isExecEnd(args[j].Lit()) {
				j++
			}
			if j > i+1 {
				d.call(h, args[i+1:j], offset(args[j-1].End()))
			}
			i = j
		case isOption(w):
			h.addWord(w, cmd)
		case wrappers[name]:
			if _, ok := d[lit]; ok {
				d.call(h, args[i:], end)
				return
			}
		}
	}
}

// toolName returns the dictionary name of the command in args ("git log"
// before "git") and the number of words it takes.
func (d Dict) toolName(args []*syntax.Word) (string, int) {
	first := args[0].Lit()
	if len(args) > 1 {
		if sub := args[1].Lit(); sub != "" {
			if _, ok := d[first+" "+sub]; ok {
				return first + " " + sub, 2
			}
		}
	}
	return first, 1
}

func isExecEnd(lit string) bool { return lit == ";" || lit == `\;` || lit == "+" }

// isOption reports whether w starts with an unquoted dash; "-" and "--"
// are operands.
func isOption(w *syntax.Word) bool {
	lit, ok := w.Parts[0].(*syntax.Lit)
	return ok && strings.HasPrefix(lit.Value, "-") && w.Lit() != "-" && w.Lit() != "--"
}

// addWord adds the option word w of cmd; words without a known option are
// left out.
func (h *Help) addWord(w *syntax.Word, cmd Command) {
	start, end := offset(w.Pos()), offset(w.End())
	items := cmd.items(w.Parts[0].(*syntax.Lit).Value, start, end)
	if len(items) > 0 {
		h.words = append(h.words, word{start: start, end: end, items: items})
	}
}

// items splits an option word, whose leading literal is lit, into options:
// a whole-word option (-type, --max-depth=1), a number (-5) or a cluster of
// single letters (-sh). In a cluster the first unknown letter starts a value
// that belongs to the previous option (-sd+, -k1,1n).
func (c Command) items(lit string, start, end int) []Item {
	name, _, _ := strings.Cut(lit, "=")
	if desc, ok := c.Options[name]; ok {
		return []Item{{Name: name, Desc: desc, Start: start, End: end}}
	}
	if desc, ok := c.Options[numOption]; ok && isNumber(name[1:]) {
		return []Item{{Name: name, Desc: desc, Start: start, End: end}}
	}
	var items []Item
	for i := 1; i < len(lit); i++ {
		opt := "-" + lit[i:i+1]
		desc, ok := c.Options[opt]
		if !ok {
			break
		}
		items = append(items, Item{Name: opt, Desc: desc, Start: start + i, End: start + i + 1})
	}
	if len(items) > 0 {
		items[len(items)-1].End = end
	}
	return items
}

func isNumber(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

func offset(p syntax.Pos) int { return int(p.Offset()) }

// Tool returns the command the cursor pos is in. The space before a command
// already belongs to it; between commands (" | ") the previous one stays.
func (h Help) Tool(pos int) (Item, bool) {
	best := -1
	for i, c := range h.calls {
		if c.Start-1 <= pos && pos < c.End && (best < 0 || c.Start > h.calls[best].Start) {
			best = i
		}
	}
	if best < 0 {
		for i, c := range h.calls {
			if c.Start <= pos && (best < 0 || c.Start > h.calls[best].Start) {
				best = i
			}
		}
	}
	if best < 0 || h.calls[best].Name == "" {
		return Item{}, false
	}
	return h.calls[best], true
}

// Options returns the options explained at the cursor pos: all options of
// a word on the space before it and on its dash, otherwise the option
// under the cursor.
func (h Help) Options(pos int) []Item {
	for _, w := range h.words {
		if pos == w.start-1 || pos == w.start {
			return w.items
		}
		for _, it := range w.items {
			if it.Start <= pos && pos < it.End {
				return []Item{it}
			}
		}
	}
	return nil
}

// MaxOptions is the largest number of options Options can return.
func (h Help) MaxOptions() int {
	n := 0
	for _, w := range h.words {
		n = max(n, len(w.items))
	}
	return n
}
