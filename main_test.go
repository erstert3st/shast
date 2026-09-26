package main

import (
	"bytes"
	"errors"
	"runtime/debug"
	"strings"
	"testing"

	"shast/internal/catalog"
	"shast/internal/engine"
)

func TestPlaySettings(t *testing.T) {
	tests := []struct {
		name       string
		difficulty string
		category   string
		rounds     int
		order      string
		wantErr    bool
	}{
		{name: "all", difficulty: "all", category: "all", rounds: 5, order: "random"},
		{name: "specific", difficulty: "hard", category: "git", rounds: 3, order: "difficulty"},
		{name: "bad difficulty", difficulty: "extreme", category: "all", rounds: 5, order: "random", wantErr: true},
		{name: "bad category", difficulty: "all", category: "network", rounds: 5, order: "random", wantErr: true},
		{name: "zero rounds", difficulty: "all", category: "all", rounds: 0, order: "random", wantErr: true},
		{name: "bad order", difficulty: "all", category: "all", rounds: 5, order: "alpha", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, err := playSettings(tt.difficulty, tt.category, tt.rounds, tt.order)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			wantD := catalog.Difficulty(tt.difficulty)
			if tt.difficulty == "all" {
				wantD = ""
			}
			wantC := tt.category
			if tt.category == "all" {
				wantC = ""
			}
			if st.Difficulty != wantD || st.Category != wantC || st.Rounds != tt.rounds || st.Order != engine.Order(tt.order) {
				t.Errorf("settings %+v", st)
			}
		})
	}
}

func TestRunDispatch(t *testing.T) {
	tests := []struct {
		args    []string
		wantErr error
		stdout  string
		stderr  string
	}{
		{args: []string{"help"}, stdout: "Usage:"},
		{args: []string{"-h"}, stdout: "Usage:"},
		{args: []string{"-version"}, stdout: "shast "},
		{args: []string{"--version"}, stdout: "shast "},
		{args: []string{"bogus"}, wantErr: errUsage, stderr: `unknown command "bogus"`},
		{args: []string{"image"}, wantErr: errUsage, stderr: "usage: shast image build"},
		{args: []string{"verify", "--runs", "0"}, wantErr: errUsage, stderr: "--runs must be at least 1"},
		{args: []string{"verify", "extra"}, wantErr: errUsage, stderr: "unexpected arguments"},
		{args: []string{"--difficulty", "extreme"}, wantErr: errUsage, stderr: "unknown difficulty"},
		{args: []string{"play", "--timeout", "0s"}, wantErr: errUsage, stderr: "timeout must be positive"},
		{args: []string{"verify", "-h"}, stderr: "-runs"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(t.Context(), tt.args, &stdout, &stderr)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if !strings.Contains(stdout.String(), tt.stdout) {
				t.Errorf("stdout %q lacks %q", stdout.String(), tt.stdout)
			}
			if !strings.Contains(stderr.String(), tt.stderr) {
				t.Errorf("stderr %q lacks %q", stderr.String(), tt.stderr)
			}
		})
	}
}

func TestResolveVersion(t *testing.T) {
	withVersion := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: v}}
	}
	tests := []struct {
		name    string
		release string
		info    *debug.BuildInfo
		want    string
	}{
		{"release wins", "v1.2.3", withVersion("v1.0.0"), "v1.2.3"},
		{"module version", "", withVersion("v0.0.0-20260926120000-51fed81abcde+dirty"), "v0.0.0-20260926120000-51fed81abcde+dirty"},
		{"devel", "", withVersion("(devel)"), "dev"},
		{"empty module version", "", withVersion(""), "dev"},
		{"no build info", "", nil, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.release, tt.info); got != tt.want {
				t.Errorf("resolveVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
