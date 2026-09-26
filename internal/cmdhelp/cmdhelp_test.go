package cmdhelp

import (
	"slices"
	"strings"
	"testing"
)

// fixture describes every option as "<tool> <option>", so the tests can see
// which command an option was attributed to.
func fixture() Dict {
	d := Dict{}
	add := func(tool string, opts ...string) {
		c := Command{About: "about " + tool, Options: map[string]string{}}
		for _, o := range opts {
			c.Options[o] = tool + " " + o
		}
		d[tool] = c
	}
	add("du", "-s", "-h", "--max-depth", "--exclude")
	add("sort", "-r", "-n", "-k", "-h")
	add("tail", "-n")
	add("paste", "-s", "-d")
	add("awk", "-F")
	add("grep", "-r", "-l", "-Z", "-c")
	add("xargs", "-0", "-P", "-I")
	add("sh", "-c")
	add("find", "-type", "-exec", "-size")
	add("ls", "-l", "-h")
	add("stat", "-c")
	add("git")
	add("git log", "--format", "--date", "-NUM")
	add("echo")
	add("wc", "-l")
	add("kill", "-TERM")
	add("tar", "-c", "-f")
	add("timeout")
	add("sleep")
	add("seq")
	return d
}

// spans renders all option items of h as "<covered text>=<description>".
func spans(cmd string, h Help) []string {
	var out []string
	for _, w := range h.words {
		for _, it := range w.items {
			out = append(out, cmd[it.Start:it.End]+"="+it.Desc)
		}
	}
	return out
}

func TestAnnotateOptions(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want []string
	}{
		{"short cluster", "du -sh cache", []string{"s=du -s", "h=du -h"}},
		{"long options with values", "du -h --max-depth=1 --exclude=.git .",
			[]string{"-h=du -h", "--max-depth=1=du --max-depth", "--exclude=.git=du --exclude"}},
		{"same letter, different tools", "tail -n +2 x | sort -rn",
			[]string{"-n=tail -n", "r=sort -r", "n=sort -n"}},
		{"attached value after a letter", "paste -sd+", []string{"s=paste -s", "d+=paste -d"}},
		{"attached value with digits", "sort -k1,1n", []string{"k1,1n=sort -k"}},
		{"attached separator", "awk -F: '{print $1}'", []string{"F:=awk -F"}},
		{"xargs runs a nested command", "grep -rlZ TODO app | xargs -0 grep -c TODO | sort",
			[]string{"r=grep -r", "l=grep -l", "Z=grep -Z", "-0=xargs -0", "-c=grep -c"}},
		{"xargs options with values", "seq 3 | xargs -P 3 -I{} sh -c 'echo {}'",
			[]string{"-P=xargs -P", "I{}=xargs -I", "-c=sh -c"}},
		{"find -exec with +", "find cache -type f -size +100k -exec ls -lh {} +",
			[]string{"-type=find -type", "-size=find -size", "-exec=find -exec", "l=ls -l", "h=ls -h"}},
		{"find -exec in a pipeline", "find c -exec stat -c '%a %n' {} + | sort -k2",
			[]string{"-exec=find -exec", "-c=stat -c", "k2=sort -k"}},
		{"find -exec with ;", `find c -exec ls -l {} \; -type f`,
			[]string{"-exec=find -exec", "-l=ls -l", "-type=find -type"}},
		{"subcommand and -NUM", "git log --format='%h' --date=short -5",
			[]string{"--format='%h'=git log --format", "--date=short=git log --date", "-5=git log -NUM"}},
		{"command substitution in quotes", `echo "$f: $(wc -l < "$f") lines"`, []string{"-l=wc -l"}},
		{"signal name", "kill -TERM $$", []string{"-TERM=kill -TERM"}},
		{"lone dash is no option", "tar -cf - app", []string{"c=tar -c", "f=tar -f"}},
		{"timeout runs a nested command", "timeout 1 sleep 5; echo x", nil},
		{"quoted words are no options", "echo '-n' \"-e\"", nil},
		{"unknown tool", "frob -x", nil},
		{"unknown first letter", "du -x", nil},
		{"unparsable", "echo 'open", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := spans(tt.cmd, fixture().Annotate(tt.cmd))
			if !slices.Equal(got, tt.want) {
				t.Errorf("Annotate(%q)\n got %q\nwant %q", tt.cmd, got, tt.want)
			}
		})
	}
}

func names(items []Item) string {
	var ns []string
	for _, it := range items {
		ns = append(ns, it.Name)
	}
	return strings.Join(ns, " ")
}

func TestOptionsAtCursor(t *testing.T) {
	const cmd = "du -sh cache"
	// One entry per cursor position 0..len(cmd).
	want := []string{"", "", "-s -h", "-s -h", "-s", "-h", "", "", "", "", "", "", ""}
	h := fixture().Annotate(cmd)
	for pos := range len(cmd) + 1 {
		if got := names(h.Options(pos)); got != want[pos] {
			t.Errorf("Options(%d) at %q = %q, want %q", pos, cmd[:pos]+"|"+cmd[pos:], got, want[pos])
		}
	}
	if got := h.MaxOptions(); got != 2 {
		t.Errorf("MaxOptions = %d, want 2", got)
	}
}

func TestOptionsNames(t *testing.T) {
	tests := []struct {
		cmd  string
		pos  int
		want string
	}{
		{"du --max-depth=1", 3, "--max-depth"},
		{"du --max-depth=1", 15, "--max-depth"}, // on the value
		{"git log -5", 9, "-5"},
		{"paste -sd+", 9, "-d"}, // on the attached value
	}
	for _, tt := range tests {
		h := fixture().Annotate(tt.cmd)
		if got := names(h.Options(tt.pos)); got != tt.want {
			t.Errorf("Options(%d) of %q = %q, want %q", tt.pos, tt.cmd, got, tt.want)
		}
	}
}

func TestTool(t *testing.T) {
	const pipe = "awk '{print $2}' f | sort | uniq -c"
	const nested = "grep -rlZ TODO app | xargs -0 grep -c TODO"
	const loop = `for f in *.sh; do echo "$f: $(wc -l < "$f")"; done`
	tests := []struct {
		name string
		cmd  string
		at   string // cursor position: the text before it
		want string // tool name, "" for none
	}{
		{"first letter", pipe, "", "awk"},
		{"inside the call", pipe, "awk '{pr", "awk"},
		{"on the pipe keeps the previous tool", pipe, "awk '{print $2}' f ", "awk"},
		{"space before the next tool", pipe, "awk '{print $2}' f | ", "sort"},
		{"unknown tool shows nothing", pipe, "awk '{print $2}' f | sort | uniq", ""},
		{"subcommand", "git log -5", "git lo", "git log"},
		{"xargs", nested, "grep -rlZ TODO app | xargs -", "xargs"},
		{"space before the nested call", nested, "grep -rlZ TODO app | xargs -0 ", "grep"},
		{"nested call", nested, "grep -rlZ TODO app | xargs -0 grep -c", "grep"},
		{"before the first call", loop, "for f in", ""},
		{"command substitution", loop, `for f in *.sh; do echo "$f: $(wc -l`, "wc"},
		{"after the command substitution", loop, `for f in *.sh; do echo "$f: $(wc -l < "$f")`, "echo"},
		{"end of the line", "du -sh cache", "du -sh cache", "du"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.HasPrefix(tt.cmd, tt.at) {
				t.Fatalf("%q is no prefix of %q", tt.at, tt.cmd)
			}
			h := fixture().Annotate(tt.cmd)
			it, ok := h.Tool(len(tt.at))
			got := ""
			if ok {
				got = it.Name
				if it.Desc != "about "+it.Name {
					t.Errorf("description %q", it.Desc)
				}
			}
			if got != tt.want {
				t.Errorf("Tool at %q = %q, want %q", tt.at+"|", got, tt.want)
			}
		})
	}
}

func TestNilDict(t *testing.T) {
	var d Dict
	h := d.Annotate("du -sh cache")
	if _, ok := h.Tool(0); ok || h.MaxOptions() != 0 || len(h.Options(3)) != 0 {
		t.Errorf("nil dictionary gives help: %+v", h)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr []string // substrings of the error; nil for success
	}{
		{"valid", "du:\n  about: estimate file space usage\n  options:\n    -s: display only a total\n", nil},
		{"no options", "git:\n  about: the stupid content tracker\n", nil},
		{"unknown field", "du:\n  about: x\n  flags: {}\n", []string{"flags"}},
		{"empty", "", []string{"no commands"}},
		{"all problems at once", "du:\n  options:\n    s: x\n    -h: ''\n' ls':\n  about: \"a\\nb\"\n",
			[]string{`"du": about is empty`, `"du": option "s" must start with -`, `"du": option "-h": description is empty`,
				`" ls": name`, `" ls": about must be a single line`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Parse([]byte(tt.yaml))
			if tt.wantErr == nil {
				if err != nil || len(d) == 0 {
					t.Fatalf("Parse: %v (%d entries)", err, len(d))
				}
				return
			}
			if err == nil {
				t.Fatal("Parse succeeded")
			}
			for _, w := range tt.wantErr {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error lacks %q:\n%v", w, err)
				}
			}
		})
	}
}
