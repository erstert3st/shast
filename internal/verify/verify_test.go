package verify

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"shast/internal/catalog"
	"shast/internal/outcmp"
	"shast/internal/sandbox"
)

// fakeSession returns scripted outputs per command. Each call to Run pops
// the next response for that command; the last one repeats.
type fakeSession struct {
	responses map[string][]response
	calls     map[string]int
	resets    int
	closed    bool
}

type response struct {
	out string
	res sandbox.Result
	err error
}

func (f *fakeSession) Reset(context.Context) error { f.resets++; return nil }

func (f *fakeSession) Run(_ context.Context, cmd string, size sandbox.Size, out io.Writer) (sandbox.Result, error) {
	if size != sandbox.FixedSize() {
		return sandbox.Result{}, errors.New("verify must run at the fixed size")
	}
	rs := f.responses[cmd]
	i := min(f.calls[cmd], len(rs)-1)
	f.calls[cmd]++
	r := rs[i]
	io.WriteString(out, r.out)
	return r.res, r.err
}

func (f *fakeSession) Close() error { f.closed = true; return nil }

// sessions hands out one fake per run; responses are split across runs by
// the per-command call counter shared through the factory.
type fakeFactory struct {
	responses map[string][]response
	calls     map[string]int
	started   []*fakeSession
}

func (ff *fakeFactory) new(context.Context) (Session, error) {
	if ff.calls == nil {
		ff.calls = make(map[string]int)
	}
	s := &fakeSession{responses: ff.responses, calls: ff.calls}
	ff.started = append(ff.started, s)
	return s, nil
}

func ch(id, cmd string, mod ...func(*catalog.Challenge)) catalog.Challenge {
	c := catalog.Challenge{ID: id, Command: cmd, Category: "text", Difficulty: catalog.Easy, Explanation: "x", Deterministic: true}
	for _, m := range mod {
		m(&c)
	}
	return c
}

func ok(out string) response { return response{out: out} }

// rep returns want once per execution (2 runs × execsPerSession).
func rep(want string) []string {
	return slices.Repeat([]string{want}, 2*execsPerSession)
}

func TestVerify(t *testing.T) {
	nonDet := func(c *catalog.Challenge) { c.Deterministic = false }
	tests := []struct {
		name      string
		challenge catalog.Challenge
		responses []response
		problems  []string // substrings, in order
	}{
		{"stable output", ch("a", "cmd"), []response{ok("x\r\n")}, nil},
		{"stable after normalization", ch("a", "cmd"), []response{ok("x\r\n"), ok("\x1b[1mx\x1b[0m  \n")}, nil},
		{"output changes", ch("a", "cmd"), []response{ok("1\n"), ok("2\n")}, []string{"execution 2 differs", "execution 3 differs", "execution 4 differs"}},
		{"order changes ignored", ch("a", "cmd", func(c *catalog.Challenge) { c.Compare = outcmp.Options{IgnoreOrder: true} }),
			[]response{ok("a\nb\n"), ok("b\na\n")}, nil},
		{"non-deterministic output may change", ch("a", "cmd", nonDet), []response{ok("1\n"), ok("2\n")}, nil},
		{"wrong exit code", ch("a", "cmd"), []response{{out: "x", res: sandbox.Result{ExitCode: 2}}}, rep("exit code 2, want 0")},
		{"expected exit code", ch("a", "cmd", func(c *catalog.Challenge) { c.ExitCode = 1 }), []response{{out: "x", res: sandbox.Result{ExitCode: 1}}}, nil},
		{"empty output", ch("a", "cmd"), []response{ok("")}, rep("empty output")},
		{"empty output allowed", ch("a", "cmd", func(c *catalog.Challenge) { c.AllowEmpty = true }), []response{ok("")}, nil},
		{"timeout", ch("a", "cmd"), []response{{out: "x", res: sandbox.Result{ExitCode: 137, TimedOut: true}}}, rep("timed out")},
		{"truncated", ch("a", "cmd"), []response{{out: "x", res: sandbox.Result{ExitCode: 137, Truncated: true}}}, rep("output cap")},
		{"matches expected", ch("a", "cmd", func(c *catalog.Challenge) { c.Expected = "x\n" }), []response{ok("x\r\n")}, nil},
		{"differs from expected", ch("a", "cmd", func(c *catalog.Challenge) { c.Expected = "y\n" }), []response{ok("x\r\n")}, []string{`differs from expected: line 1: want "y", got "x"`}},
		{"expected ignored when non-deterministic", ch("a", "cmd", nonDet, func(c *catalog.Challenge) { c.Expected = "y\n" }), []response{ok("x\r\n")}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ff := &fakeFactory{responses: map[string][]response{tt.challenge.Command: tt.responses}}
			outcomes, err := Verify(t.Context(), []catalog.Challenge{tt.challenge}, 2, ff.new, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if len(ff.started) != 2 {
				t.Fatalf("started %d sessions, want 2", len(ff.started))
			}
			for _, s := range ff.started {
				if !s.closed || s.resets != execsPerSession {
					t.Errorf("session closed=%v resets=%d, want closed and %d resets", s.closed, s.resets, execsPerSession)
				}
			}
			got := outcomes[0].Problems
			if len(got) != len(tt.problems) {
				t.Fatalf("problems %q, want %d matching %q", got, len(tt.problems), tt.problems)
			}
			for i, want := range tt.problems {
				if !strings.Contains(got[i], want) {
					t.Errorf("problem %d = %q, want it to contain %q", i, got[i], want)
				}
			}
		})
	}
}

func TestVerifySandboxError(t *testing.T) {
	boom := errors.New("boom")
	ff := &fakeFactory{responses: map[string][]response{"cmd": {{err: boom}}}}
	_, err := Verify(t.Context(), []catalog.Challenge{ch("a", "cmd")}, 1, ff.new, io.Discard)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
	if !ff.started[0].closed {
		t.Error("session not closed after error")
	}
}

func TestVerifyRuns(t *testing.T) {
	ff := &fakeFactory{}
	if _, err := Verify(t.Context(), nil, 0, ff.new, io.Discard); err == nil {
		t.Error("runs=0 accepted")
	}
}

func TestWriteReport(t *testing.T) {
	outcomes := []Outcome{
		{Challenge: ch("good", "a")},
		{Challenge: ch("bad", "b"), Problems: []string{"p1", "p2"}},
		{Challenge: ch("rand", "c", func(c *catalog.Challenge) { c.Deterministic = false })},
	}
	var buf bytes.Buffer
	failed, err := WriteReport(&buf, outcomes)
	if err != nil {
		t.Fatal(err)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
	rows := map[string]string{} // id -> "RESULT details"
	for _, line := range strings.Split(buf.String(), "\n") {
		if f := strings.Fields(line); len(f) >= 3 {
			rows[f[1]] = f[0] + " " + strings.Join(f[3:], " ")
		}
	}
	for id, want := range map[string]string{
		"good": "PASS ",
		"bad":  "FAIL p1; p2",
		"rand": "PASS non-deterministic: output not compared",
	} {
		if rows[id] != want {
			t.Errorf("row %s = %q, want %q\n%s", id, rows[id], want, buf.String())
		}
	}
	if !strings.Contains(buf.String(), "2 passed, 1 failed") {
		t.Errorf("summary missing:\n%s", buf.String())
	}
}

func TestFirstDiff(t *testing.T) {
	tests := []struct {
		want, got string
		opts      outcmp.Options
		diff      string
	}{
		{"a\nb", "a\nc", outcmp.Options{}, `line 2: want "b", got "c"`},
		{"a", "a\n\nb", outcmp.Options{}, `line 2: want <missing>, got ""`},
		{"a\nb", "a", outcmp.Options{}, `line 2: want "b", got <missing>`},
		{"c\nb\nX", "a\nb\nc", outcmp.Options{IgnoreOrder: true}, `line 1: want "X", got "a"`},
		{"b\na", "a\nb", outcmp.Options{IgnoreOrder: true}, "outputs are equal"},
	}
	for _, tt := range tests {
		if got := firstDiff(tt.want, tt.got, tt.opts); got != tt.diff {
			t.Errorf("firstDiff(%q, %q) = %s, want %s", tt.want, tt.got, got, tt.diff)
		}
	}
}

func TestGenerateExpected(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"rand.txt", "notes.txt", "README.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cs := []catalog.Challenge{
		ch("a", "cmd-a", func(c *catalog.Challenge) { c.Expected = "stale\n" }),
		ch("b", "cmd-b"),
		ch("rand", "cmd-rand", func(c *catalog.Challenge) { c.Deterministic = false }),
	}
	ff := &fakeFactory{responses: map[string][]response{
		"cmd-a":    {ok("\x1b[32mhello\x1b[0m\r\n")},
		"cmd-b":    {ok("x\r\ny\r\n\r\n")},
		"cmd-rand": {ok("r")},
	}}
	n, err := GenerateExpected(t.Context(), cs, dir, ff.new, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("wrote %d files, want 2", n)
	}
	if len(ff.started) != 1 || ff.calls["cmd-a"] != execsPerSession || ff.calls["cmd-rand"] != 0 {
		t.Errorf("sessions %d, calls %v", len(ff.started), ff.calls)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// rand.txt belongs to a non-deterministic challenge; notes.txt is not ours.
	if want := []string{"README.md", "a.txt", "b.txt", "notes.txt"}; !slices.Equal(names, want) {
		t.Errorf("files %v, want %v", names, want)
	}
	for id, want := range map[string]string{"a": "hello\n", "b": "x\ny\n"} {
		got, _ := os.ReadFile(filepath.Join(dir, id+".txt"))
		if string(got) != want {
			t.Errorf("%s.txt = %q, want %q", id, got, want)
		}
	}
}

func TestGenerateExpectedRejectsFailures(t *testing.T) {
	tests := []struct {
		name      string
		responses []response
	}{
		{"wrong exit code", []response{{out: "x", res: sandbox.Result{ExitCode: 1}}}},
		{"unstable output", []response{ok("1"), ok("2")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			ff := &fakeFactory{responses: map[string][]response{"cmd": tt.responses}}
			if _, err := GenerateExpected(t.Context(), []catalog.Challenge{ch("a", "cmd")}, dir, ff.new, io.Discard); err == nil {
				t.Fatal("failing command accepted")
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("files written despite failure: %v", entries)
			}
		})
	}
}
