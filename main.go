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
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "image":
		return runImage(ctx, args, stdout, stderr)
	case "play", "verify", "expected":
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
  shast image build [--no-cache]
                          build the sandbox image
`)
}

func runImage(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "build" {
		fmt.Fprintln(stderr, "usage: shast image build [--no-cache]")
		return errUsage
	}
	fs := flag.NewFlagSet("image build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	noCache := fs.Bool("no-cache", false, "build without cache and pull the base image again")
	if err := fs.Parse(args[1:]); err != nil {
		return errUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "image build: unexpected arguments %q\n", fs.Args())
		return errUsage
	}

	docker, err := sandbox.Connect(ctx)
	if err != nil {
		return err
	}
	defer docker.Close()

	tag := sandbox.ImageTag()
	fmt.Fprintf(stdout, "building %s\n", tag)
	if err := docker.BuildImage(ctx, tag, *noCache, stdout); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "built %s\n", tag)
	return nil
}
