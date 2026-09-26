package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"shast/internal/catalog"
	"shast/internal/sandbox"
	"shast/internal/verify"
)

// errVerifyFailed signals failed challenges; the report was already printed.
var errVerifyFailed = errors.New("verification failed")

func runVerify(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runs := fs.Int("runs", 2, "number of fresh sandbox sessions")
	id := fs.String("id", "", "verify only the challenge with this ID")
	catalogDir := fs.String("catalog", "", "directory with an additional commands.yaml (and expected/)")
	timeout := fs.Duration("timeout", sandbox.DefaultTimeout, "per-command timeout")
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if *runs < 1 {
		fmt.Fprintln(stderr, "verify: --runs must be at least 1")
		return errUsage
	}

	cs, err := catalog.LoadWithOverlay(*catalogDir)
	if err != nil {
		return err
	}
	if *id != "" {
		if cs, err = selectID(cs, *id); err != nil {
			return err
		}
	}

	docker, err := openSandbox(ctx)
	if err != nil {
		return err
	}
	defer docker.Close()

	outcomes, err := verify.Verify(ctx, cs, *runs, sessionFactory(docker, *timeout), stderr)
	if err != nil {
		return err
	}
	failed, err := verify.WriteReport(stdout, outcomes)
	if err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%w: %d of %d challenges", errVerifyFailed, failed, len(outcomes))
	}
	return nil
}

func runExpected(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("expected", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "directory for the <id>.txt files (default internal/catalog/expected, or DIR/expected with --catalog)")
	catalogDir := fs.String("catalog", "", "generate for the commands.yaml in this directory instead of the built-in catalog")
	timeout := fs.Duration("timeout", sandbox.DefaultTimeout, "per-command timeout")
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}

	// The built-in catalog's expectations are embedded from the source
	// tree; an overlay keeps its own next to its commands.yaml.
	var cs []catalog.Challenge
	var err error
	if *catalogDir == "" {
		cs, err = catalog.Default()
		if *out == "" {
			*out = filepath.Join("internal", "catalog", "expected")
		}
	} else {
		cs, err = catalog.Load(os.DirFS(*catalogDir))
		if *out == "" {
			*out = filepath.Join(*catalogDir, "expected")
		}
	}
	if err != nil {
		return err
	}
	docker, err := openSandbox(ctx)
	if err != nil {
		return err
	}
	defer docker.Close()

	n, err := verify.GenerateExpected(ctx, cs, *out, sessionFactory(docker, *timeout), stderr)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %d expected outputs to %s\n", n, *out)
	return nil
}

// sessionFactory starts sandbox sessions for verification.
func sessionFactory(docker *sandbox.Docker, timeout time.Duration) verify.NewSession {
	return func(ctx context.Context) (verify.Session, error) {
		return docker.StartSession(ctx, sandbox.Options{Timeout: timeout})
	}
}

// selectID returns the challenge with the given ID.
func selectID(cs []catalog.Challenge, id string) ([]catalog.Challenge, error) {
	for _, c := range cs {
		if c.ID == id {
			return []catalog.Challenge{c}, nil
		}
	}
	return nil, fmt.Errorf("no challenge with id %q", id)
}
