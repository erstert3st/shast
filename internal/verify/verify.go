// Package verify checks that every catalog command works against the seed
// data and, for deterministic commands, produces identical output across
// fresh sandbox sessions. It also generates the expected outputs used for
// that comparison (and later by Reverse mode).
package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"shast/internal/catalog"
	"shast/internal/outcmp"
	"shast/internal/sandbox"
)

// Session is a sandbox session as provided by sandbox.Session.
type Session interface {
	Reset(ctx context.Context) error
	Run(ctx context.Context, cmd string, size sandbox.Size, out io.Writer) (sandbox.Result, error)
	Close() error
}

// NewSession starts a fresh sandbox session.
type NewSession func(ctx context.Context) (Session, error)

// execsPerSession is how often each command runs within one session; the
// second execution checks that Reset restores the workspace completely.
const execsPerSession = 2

// Outcome is the verification result of one challenge.
type Outcome struct {
	Challenge catalog.Challenge
	Problems  []string // empty if the challenge passed
	Output    string   // normalized output of the first execution
}

// Passed reports whether the challenge passed all checks.
func (o Outcome) Passed() bool { return len(o.Problems) == 0 }

// execution is the observed result of running a command once.
type execution struct {
	output string // normalized
	res    sandbox.Result
}

// Verify runs every challenge execsPerSession times in each of runs fresh
// sessions and checks exit code, non-empty output and, for deterministic
// challenges, that all outputs are equal to each other and to the stored
// expected output. Sandbox failures abort verification with an error;
// failing challenges are reported in the outcomes.
// Progress dots, one per challenge and run, are written to progress.
func Verify(ctx context.Context, cs []catalog.Challenge, runs int, newSession NewSession, progress io.Writer) ([]Outcome, error) {
	if runs < 1 {
		return nil, fmt.Errorf("verify: runs must be at least 1, got %d", runs)
	}
	execs := make([][]execution, len(cs))
	for run := range runs {
		fmt.Fprintf(progress, "run %d/%d ", run+1, runs)
		if err := inSession(ctx, newSession, func(s Session) error {
			for i, c := range cs {
				for range execsPerSession {
					e, err := execute(ctx, s, c.Command)
					if err != nil {
						return fmt.Errorf("verify %s: %w", c.ID, err)
					}
					execs[i] = append(execs[i], e)
				}
				fmt.Fprint(progress, ".")
			}
			return nil
		}); err != nil {
			fmt.Fprintln(progress)
			return nil, err
		}
		fmt.Fprintln(progress)
	}
	outcomes := make([]Outcome, len(cs))
	for i, c := range cs {
		outcomes[i] = judge(c, execs[i])
	}
	return outcomes, nil
}

// inSession starts a session, calls fn and closes the session again.
func inSession(ctx context.Context, newSession NewSession, fn func(Session) error) (err error) {
	s, err := newSession(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	return fn(s)
}

// execute resets the workspace and runs cmd at the fixed terminal size.
func execute(ctx context.Context, s Session, cmd string) (execution, error) {
	if err := s.Reset(ctx); err != nil {
		return execution{}, err
	}
	var buf bytes.Buffer
	res, err := s.Run(ctx, cmd, sandbox.FixedSize(), &buf)
	if err != nil {
		return execution{}, err
	}
	return execution{output: outcmp.Normalize(buf.String()), res: res}, nil
}

// judge checks the executions of one challenge.
func judge(c catalog.Challenge, execs []execution) Outcome {
	o := Outcome{Challenge: c}
	if len(execs) == 0 {
		o.Problems = append(o.Problems, "not executed")
		return o
	}
	o.Output = execs[0].output
	for i, e := range execs {
		o.Problems = append(o.Problems, checkExecution(c, e, i+1)...)
	}
	if !c.Deterministic {
		return o
	}
	for i, e := range execs[1:] {
		if !outcmp.Equal(o.Output, e.output, c.Compare) {
			o.Problems = append(o.Problems, fmt.Sprintf("output of execution %d differs from execution 1: %s",
				i+2, firstDiff(o.Output, e.output, c.Compare)))
		}
	}
	if c.Expected != "" && !outcmp.Equal(c.Expected, o.Output, c.Compare) {
		o.Problems = append(o.Problems, "output differs from expected: "+firstDiff(outcmp.Normalize(c.Expected), o.Output, c.Compare))
	}
	return o
}

func checkExecution(c catalog.Challenge, e execution, n int) []string {
	var ps []string
	switch {
	case e.res.TimedOut:
		ps = append(ps, fmt.Sprintf("execution %d timed out", n))
	case e.res.Truncated:
		ps = append(ps, fmt.Sprintf("execution %d exceeded the output cap", n))
	case e.res.ExitCode != c.ExitCode:
		ps = append(ps, fmt.Sprintf("execution %d: exit code %d, want %d", n, e.res.ExitCode, c.ExitCode))
	}
	if e.output == "" && !c.AllowEmpty {
		ps = append(ps, fmt.Sprintf("execution %d: empty output", n))
	}
	return ps
}

// firstDiff describes the first differing line of two normalized outputs.
// With IgnoreOrder the lines are compared sorted, as outcmp.Equal does.
func firstDiff(want, got string, opts outcmp.Options) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	if opts.IgnoreOrder {
		slices.Sort(wl)
		slices.Sort(gl)
	}
	line := func(ls []string, i int) string {
		if i < len(ls) {
			return strconv.Quote(ls[i])
		}
		return "<missing>"
	}
	for i := range max(len(wl), len(gl)) {
		if w, g := line(wl, i), line(gl, i); w != g {
			return fmt.Sprintf("line %d: want %s, got %s", i+1, w, g)
		}
	}
	return "outputs are equal"
}

// WriteReport prints one line per challenge and a summary. It returns the
// number of failed challenges.
func WriteReport(w io.Writer, outcomes []Outcome) (failed int, err error) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RESULT\tID\tCATEGORY\tDETAILS")
	for _, o := range outcomes {
		status, details := "PASS", ""
		if !o.Challenge.Deterministic {
			details = "non-deterministic: output not compared"
		}
		if !o.Passed() {
			status, details = "FAIL", strings.Join(o.Problems, "; ")
			failed++
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", status, o.Challenge.ID, o.Challenge.Category, details)
	}
	if err := tw.Flush(); err != nil {
		return failed, fmt.Errorf("write report: %w", err)
	}
	_, err = fmt.Fprintf(w, "\n%d passed, %d failed\n", len(outcomes)-failed, failed)
	return failed, err
}

// GenerateExpected verifies every deterministic challenge in one fresh
// session (execsPerSession executions each, ignoring stored expectations)
// and, only if all pass, writes each normalized output to dir/<id>.txt.
// Files of challenges in cs that are not deterministic are removed; other
// files in dir are left alone.
func GenerateExpected(ctx context.Context, cs []catalog.Challenge, dir string, newSession NewSession, progress io.Writer) (written int, err error) {
	var det []catalog.Challenge
	for _, c := range cs {
		if c.Deterministic {
			c.Expected = ""
			det = append(det, c)
		}
	}
	outcomes, err := Verify(ctx, det, 1, newSession, progress)
	if err != nil {
		return 0, err
	}
	var errs []error
	for _, o := range outcomes {
		if !o.Passed() {
			errs = append(errs, fmt.Errorf("%s: %s", o.Challenge.ID, strings.Join(o.Problems, "; ")))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return 0, fmt.Errorf("expected outputs not written: %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("expected outputs: %w", err)
	}
	for _, o := range outcomes {
		if err := os.WriteFile(filepath.Join(dir, o.Challenge.ID+".txt"), []byte(o.Output+"\n"), 0o644); err != nil {
			return written, fmt.Errorf("expected outputs: %w", err)
		}
		written++
	}
	for _, c := range cs {
		if c.Deterministic {
			continue
		}
		if err := os.Remove(filepath.Join(dir, c.ID+".txt")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return written, fmt.Errorf("remove expected output of non-deterministic %s: %w", c.ID, err)
		}
	}
	return written, nil
}
