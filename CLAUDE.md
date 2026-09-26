# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

`shast` is a terminal typing game for real shell commands (Go, single binary). A typed command runs in an
isolated Docker sandbox and its output streams live into a Bubble Tea TUI. The original task is in
`.docs/spec.md`; `.docs/plan.md` (German) holds the approved design and, under "Abweichungen", every
deviation from it with its rationale. Check there before changing sandbox or determinism behaviour.
Don't add features beyond the spec without asking.

## Commands

```sh
make check                      # gofmt -l, go vet, staticcheck, go test ./... (must pass before a commit)
make test-short                 # unit tests only, no Docker
go test -run TestName ./internal/engine          # single test
go build -o shast . && ./shast image build        # binary + sandbox image (needed by integration tests)
./shast verify --id <id>        # run one catalog command against the sandbox
./shast verify                  # whole catalog, 2 fresh sessions, 2 runs each; exit 1 on any FAIL
./shast expected                # regenerate internal/catalog/expected/ (never edit those files by hand)
go test -run TestSeedManifestGolden -update ./internal/sandbox   # after an intended seed change
make seed-determinism           # two --no-cache image builds, compares seed manifests (slow, network)
```

`staticcheck` is pinned as a Go tool in `go.mod` and invoked via `go tool staticcheck`; it is not
expected in `PATH`.

Integration tests (sandbox, `verify`) skip automatically with `-short`, without a Docker daemon, or when
the image for the current tag is missing. They only run for real after `./shast image build`.

CI (`.github/workflows/ci.yml`) runs `go vet` and `go test -race -short ./...` on Linux, macOS and
Windows, so unit tests must stay portable (no Unix-only binaries, PIDs, permission bits or `/` paths;
use `runtime.GOOS` where behaviour differs). A pushed `v*` tag builds release binaries with
`-ldflags "-X main.version=<tag>"`; see `cmd_version.go` for the version fallback.

## Architecture

Dependency direction: `main` (`main.go`, `cmd_*.go`) → `tui` / `verify` → `engine` → `catalog`,
`outcmp`, `score`, `typing`; `sandbox` is used by `main`, `engine` (for `sandbox.Result`), `tui` and
`verify`.

- **Subcommands** use stdlib `flag` with one `FlagSet` each (`parseFlags` rejects positional args).
  `main.openSandbox` (Connect → Preflight → Sweep) must succeed *before* the TUI takes the terminal, so
  Docker/image problems surface as plain errors. Returning `errUsage` suppresses the `shast:` prefix.
- **Small interfaces at the point of use**: `tui.Runner`, `tui.Scores` and `verify.Session` are
  satisfied by `*sandbox.Session` / `*score.Store`. Unit tests of `tui`, `engine` and `verify` use
  fakes, so only `internal/sandbox` and `internal/verify/integration_test.go` need Docker.
- **Modes** (`engine/mode.go`): `engine.Mode` decides eligibility, prompt, when input may be submitted
  and how a result is judged. `Speed` (Mode 1) is playable; `Reverse` (Mode 2) has complete rules but no
  UI yet (the setup screen shows it as "coming soon"). A `catalog.Challenge` carries both `Command` and
  `Expected`, so both modes share one catalog. `Command` stays mandatory: it is the reference solution
  that `verify`/`expected` use to prove an expected output is reachable. `Judge` takes the output as a
  separate argument because `sandbox.Result` deliberately contains no output (it is streamed and capped).
- **Catalog** (`internal/catalog`): `commands.yaml` and `expected/*.txt` are embedded via `go:embed`.
  Decoding is strict (`KnownFields`), validation reports all problems at once, and an `expected/<id>.txt`
  without a matching ID is a load error. `--catalog DIR` overlays another catalog by ID (`Merge`).
  Adding a command needs no code change; see README "Adding commands" for the field reference.
- **Sandbox** (`internal/sandbox`): one long-lived container per game session, not per command.
  - The image is built from the embedded `image/` directory (Dockerfile, `seed.sh`, `gitconfig`); the tag
    is `shast-sandbox:<sha256 of that dir>[:12]`. Any edit there changes the tag, so the binary reports
    the image as missing until `./shast image build`. A changed seed usually also means regenerating the
    expected outputs and the golden manifest `testdata/seed.sha256`.
  - `Reset` (before every command) kills all `dev` processes, removes IPC objects, empties `/work` and
    `/tmp` (= `HOME`) and copies `/seed` back with `cp -a`. `Run` executes via
    `bash -c 'timeout -s KILL Ns bash -c "$0"' <cmd>` (no quoting of the command) on a TTY and kills
    leftover processes after every command.
  - The container's main process `sleep 14400` is the 4-hour watchdog and runs as `nobody`, so the
    `kill -9 -1` issued as `dev` cannot kill it. `Sweep` removes only containers whose owner process
    (labels `shast.pid`, `shast.host`) is gone, so parallel shast instances and test packages coexist.
- **Determinism**: output that is compared (`verify`, `expected`, Reverse mode) is always produced at
  `sandbox.FixedSize()` (80x24), because `ls` column layouts depend on it. `outcmp.Normalize` strips
  ANSI, applies CR overwrites and trims whitespace; `IgnoreOrder` compares lines as a multiset. Tool
  versions are frozen by the pinned base-image digest plus apt from snapshot.debian.org. Some expected
  outputs (tmpfs block counts, PID 1 = `docker-init`) hold only for x86-64/4K pages with Docker's
  default init; on other hosts `verify` reports drift and `shast expected` must be rerun.
- **TUI** (`internal/tui`, Bubble Tea v2 under `charm.land/...` import paths): screens setup → round →
  summary; a round has phases typing → running → result. The command runs in a goroutine
  (`stream.run`: Reset, then Run) and non-blocking writes are coalesced into `streamMsg`s. A generation
  counter (`gen`) on round and stream messages drops messages from a previous run or session. `term.go`
  is intentionally not a VT emulator: it handles `\r`, `\n`, `\b`, tabs, SGR colours and erase-in-line,
  wraps at the TTY width and drops every other control sequence.
- **Scoring**: skipped rounds (Esc while typing) are excluded from WPM/accuracy, so only sessions without
  skipped rounds enter the highscores (`$XDG_DATA_HOME/shast/scores.json`).

## Rules

- Never weaken sandbox isolation (network, user, capabilities, read-only rootfs, limits, IPC, timeout,
  output cap) to make a command work. Change the command or `seed.sh` instead.
- `seed.sh` must stay free of randomness: fixed TZ/locale/hostname, fixed mtimes via `touch -d`,
  fixed `GIT_*_DATE` for every git operation, `gzip -n`.
- Mark commands whose output changes between runs (`ps`, `date`, `df`, ctimes/atimes …) as
  `deterministic: false`; use `compare: { ignore_order: true }` when only the line order is unstable.
- Tests are table-driven; errors are wrapped with `fmt.Errorf("…: %w", err)`; `context` carries
  cancellation and timeouts; no global singletons.
