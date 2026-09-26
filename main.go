// Command shast is a terminal typing game for real shell commands. Typed
// commands run inside an isolated Docker sandbox and their output is
// streamed live into the TUI.
//
// Usage:
//
//	shast [play] [flags]    start the game (default)
//	shast verify [flags]    verify every catalog command against the sandbox
//	shast expected [flags]  generate expected outputs for deterministic commands
//	shast image build       build the sandbox image (--no-cache to rebuild)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"shast/internal/sandbox"
)

var errUsage = errors.New("usage error")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	if err != nil {
		if !errors.Is(err, errUsage) {
			fmt.Fprintln(os.Stderr, "shast:", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cmd := "play"
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		cmd, args = "help", nil
	} else if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "play":
		return fmt.Errorf("play %q: not implemented yet", args)
	case "verify":
		return runVerify(ctx, args, stdout, stderr)
	case "expected":
		return runExpected(ctx, args, stdout, stderr)
	case "image":
		return runImage(ctx, args, stdout, stderr)
	case "help":
		usage(stdout)
		return nil
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", cmd)
		usage(stderr)
		return errUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  shast [play] [flags]    start the game (default)
  shast verify [flags]    verify every catalog command against the sandbox
  shast expected [flags]  generate expected outputs for deterministic commands
  shast image build [--no-cache]
                          build the sandbox image

Run "shast play -h" (or any other command with -h) for its flags.
`)
}

// parseFlags parses args into fs and rejects positional arguments. -h
// prints the flags and ends the command successfully.
func parseFlags(fs *flag.FlagSet, args []string) (help bool, err error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, nil
		}
		return false, errUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(fs.Output(), "%s: unexpected arguments %q\n", fs.Name(), fs.Args())
		return false, errUsage
	}
	return false, nil
}

// openSandbox connects to Docker, checks that the sandbox image exists and
// removes containers left behind by earlier, crashed runs.
func openSandbox(ctx context.Context) (*sandbox.Docker, error) {
	docker, err := sandbox.Connect(ctx)
	if err != nil {
		return nil, err
	}
	if err := docker.Preflight(ctx); err != nil {
		docker.Close()
		return nil, err
	}
	if err := docker.Sweep(ctx); err != nil {
		docker.Close()
		return nil, err
	}
	return docker, nil
}
