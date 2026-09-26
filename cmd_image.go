package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"shast/internal/sandbox"
)

func runImage(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "build" {
		fmt.Fprintln(stderr, "usage: shast image build [--no-cache]")
		return errUsage
	}
	fs := flag.NewFlagSet("image build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	noCache := fs.Bool("no-cache", false, "build without cache and pull the base image again")
	if help, err := parseFlags(fs, args[1:]); help || err != nil {
		return err
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
