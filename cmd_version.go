package main

import (
	"fmt"
	"io"
	"runtime/debug"
)

// version is the release version. Release builds set it from the git tag
// with -ldflags "-X main.version=<tag>".
var version string

func runVersion(stdout io.Writer) error {
	info, _ := debug.ReadBuildInfo()
	fmt.Fprintln(stdout, "shast", resolveVersion(version, info))
	return nil
}

// resolveVersion returns the release version if set, else the main module
// version recorded by the Go toolchain (the VCS tag or a pseudo-version),
// else "dev".
func resolveVersion(release string, info *debug.BuildInfo) string {
	switch {
	case release != "":
		return release
	case info != nil && info.Main.Version != "" && info.Main.Version != "(devel)":
		return info.Main.Version
	default:
		return "dev"
	}
}
