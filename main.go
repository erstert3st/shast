// Command shast is a terminal typing game for real shell commands. Typed
// commands run inside an isolated Docker sandbox and their output is
// streamed live into the TUI.
//
// Usage:
//
//	shast [play] [flags]    start the game (default)
//	shast verify [flags]    verify every catalog command against the sandbox
//	shast expected [flags]  generate expected outputs for deterministic commands
//	shast image build       build the sandbox image
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
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
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "play", "verify", "expected", "image":
		return fmt.Errorf("%s %q: not implemented yet", cmd, args)
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
  shast image build       build the sandbox image
`)
}
