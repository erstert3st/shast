package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// Defaults for Options.
const (
	DefaultTimeout   = 10 * time.Second
	DefaultOutputCap = 1 << 20 // bytes
)

// ErrSessionGone reports that the sandbox container stopped or was removed
// (e.g. by its watchdog); the session cannot be used any more.
var ErrSessionGone = errors.New("sandbox container is gone – restart shast")

const (
	labelSession = "shast.session" // session ID; marks every shast container
	labelPID     = "shast.pid"     // PID of the owning shast process
	labelHost    = "shast.host"    // host name of the owning shast process

	// Commands run as the unprivileged user dev. The container's main
	// process (the watchdog) runs as nobody, so the `kill -9 -1` of Reset
	// cannot hit it.
	sandboxUser  = "1000:1000"
	watchdogUser = "65534:65534"
	// The watchdog removes the container (AutoRemove) after at most four
	// hours, even if shast itself is killed with SIGKILL.
	watchdogSeconds = 4 * 60 * 60

	workDir  = "/work"
	hostname = "web01"

	// clientGrace is added to the in-container timeout for the client-side
	// deadline, which only fires if the in-container timeout fails.
	clientGrace  = 2 * time.Second
	pollInterval = 50 * time.Millisecond
	// drainGrace is how long Run waits for remaining output after the
	// command exited before it kills processes that keep the TTY open.
	drainGrace = 100 * time.Millisecond
	// resetTimeout bounds a workspace reset.
	resetTimeout = 30 * time.Second
	// cleanupTimeout bounds kill and remove calls that must run even when
	// the caller's context is already canceled.
	cleanupTimeout = 10 * time.Second
)

// sandboxEnv is the fixed environment of every command.
var sandboxEnv = []string{
	"TZ=UTC",
	"LANG=C.UTF-8",
	"LC_ALL=C.UTF-8",
	"HOME=/tmp",
	"PAGER=cat",
	"GIT_PAGER=cat",
	"TERM=xterm-256color",
}

// resetScript restores the pristine workspace: it kills all leftover
// processes of the sandbox user (kill -1 spares the calling shell), removes
// leftover semaphores, makes everything removable again (e.g. after
// chmod 000), empties /work and /tmp (HOME) and copies the seed with all
// metadata back into /work. find -delete instead of a glob, because a glob
// over many files exceeds the argument list limit.
const resetScript = `kill -9 -1 2>/dev/null
ipcrm -a 2>/dev/null
set -eu
chmod -R u+rwX /work
chmod -R u+rwX /tmp 2>/dev/null || true
find /work /tmp -mindepth 1 -delete
cp -a /seed/. /work/`

// killScript kills every process of the sandbox user except itself.
const killScript = `kill -9 -1 2>/dev/null; true`

// Size is a terminal size in character cells.
type Size struct {
	Width, Height uint
}

// FixedSize returns the 80x24 terminal size used wherever output must be
// reproducible (verification, Reverse mode): column layouts depend on it.
func FixedSize() Size { return Size{Width: 80, Height: 24} }

// Options configures a Session. Zero fields use the defaults.
type Options struct {
	Timeout   time.Duration // per command, DefaultTimeout if 0
	OutputCap int64         // bytes of output per command, DefaultOutputCap if 0
}

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.OutputCap <= 0 {
		o.OutputCap = DefaultOutputCap
	}
	return o
}

// Result describes a finished command.
type Result struct {
	ExitCode  int
	Duration  time.Duration
	TimedOut  bool // killed after the per-command timeout
	Truncated bool // killed after exceeding the output cap
}

// Session is one sandbox container. Run and Reset must not be called
// concurrently; Resize may be called while Run is in progress.
type Session struct {
	d         *Docker
	id        string // container ID
	sessionID string
	opts      Options

	mu     sync.Mutex
	execID string // exec of the running command, "" if none
}

// StartSession creates and starts a sandbox container from the image that
// matches this binary. The caller must Close the session.
func (d *Docker) StartSession(ctx context.Context, opts Options) (*Session, error) {
	return d.startSession(ctx, opts, os.Getpid())
}

func (d *Docker) startSession(ctx context.Context, opts Options, ownerPID int) (*Session, error) {
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	host, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("start sandbox: %w", err)
	}
	cfg := &container.Config{
		Image:           ImageTag(),
		Hostname:        hostname,
		User:            watchdogUser,
		Cmd:             []string{"sleep", strconv.Itoa(watchdogSeconds)},
		WorkingDir:      workDir,
		Env:             sandboxEnv,
		NetworkDisabled: true,
		Labels: map[string]string{
			labelSession: id,
			labelPID:     strconv.Itoa(ownerPID),
			labelHost:    host,
		},
	}
	pids := int64(128)
	init := true
	hostCfg := &container.HostConfig{
		NetworkMode:    "none",
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"},
		ReadonlyRootfs: true,
		AutoRemove:     true,
		Init:           &init,
		Tmpfs: map[string]string{
			"/work": "rw,exec,nosuid,nodev,size=64m,uid=1000,gid=1000,mode=0755",
			"/tmp":  "rw,noexec,nosuid,nodev,size=16m,mode=1777",
		},
		// Private IPC namespace without /dev/shm, and no SysV shared memory
		// or message queues: their memory would survive Reset and count
		// against the memory limit.
		IpcMode: "none",
		Sysctls: map[string]string{
			"kernel.shmmax":        "0",
			"kernel.shmall":        "0",
			"kernel.shmmni":        "0",
			"kernel.msgmni":        "0",
			"fs.mqueue.queues_max": "0",
		},
		Resources: container.Resources{
			PidsLimit:  &pids,
			Memory:     256 << 20,
			MemorySwap: 256 << 20,
			NanoCPUs:   1e9,
			// No core dumps: they would be written by the host's handler.
			Ulimits: []*container.Ulimit{{Name: "core", Soft: 0, Hard: 0}},
		},
	}
	c, err := d.api.ContainerCreate(ctx, client.ContainerCreateOptions{Config: cfg, HostConfig: hostCfg})
	if err != nil {
		return nil, fmt.Errorf("create sandbox container: %w", err)
	}
	s := &Session{d: d, id: c.ID, sessionID: id, opts: opts.withDefaults()}
	if _, err := d.api.ContainerStart(ctx, c.ID, client.ContainerStartOptions{}); err != nil {
		return nil, errors.Join(fmt.Errorf("start sandbox container: %w", err), s.Close())
	}
	return s, nil
}

func newSessionID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("session id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ID returns the session ID, the value of the shast.session label.
func (s *Session) ID() string { return s.sessionID }

// Close removes the container. It is safe to call more than once.
func (s *Session) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	return s.d.removeContainer(ctx, s.id)
}

func (d *Docker) removeContainer(ctx context.Context, id string) error {
	_, err := d.api.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
	// AutoRemove may already be removing the container.
	if err != nil && !cerrdefs.IsNotFound(err) && !cerrdefs.IsConflict(err) {
		return fmt.Errorf("remove sandbox container: %w", err)
	}
	return nil
}

// Reset restores /work to the pristine seed and kills leftover processes.
func (s *Session) Reset(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, resetTimeout)
	defer cancel()
	code, out, err := s.execWait(ctx, resetScript)
	if err != nil {
		return fmt.Errorf("reset workspace: %w", err)
	}
	if code != 0 {
		return s.gone(fmt.Errorf("reset workspace: exit code %d: %s", code, bytes.TrimSpace(out)))
	}
	return nil
}

// Run executes cmd with bash in /work on a TTY of the given size (80x24 if
// zero) and streams the raw terminal output to out while it runs. out must
// not block for long: Run waits for pending writes before it returns.
//
// The command is killed after the session timeout or when its output
// exceeds the output cap; both are reported in the Result, not as errors.
// Processes it leaves behind are killed before Run returns. If ctx is
// canceled, the command is killed and ctx's error is returned. If the
// container is gone, the error wraps ErrSessionGone.
func (s *Session) Run(ctx context.Context, cmd string, size Size, out io.Writer) (Result, error) {
	if size.Width == 0 || size.Height == 0 {
		size = FixedSize()
	}
	// The command is passed as $0 of the outer shell, so it needs no quoting.
	secs := int(math.Ceil(s.opts.Timeout.Seconds()))
	script := fmt.Sprintf(`timeout -s KILL %ds bash -c "$0"`, secs)
	argv := []string{"bash", "--noprofile", "--norc", "-c", script, cmd}

	runCtx, cancel := context.WithTimeout(ctx, s.opts.Timeout+clientGrace)
	defer cancel()

	start := time.Now()
	execID, conn, err := s.startExec(runCtx, argv, size)
	if err != nil {
		return Result{}, s.gone(fmt.Errorf("run command: %w", err))
	}
	s.setExec(execID)
	defer s.setExec("")

	capped := newCapWriter(out, s.opts.OutputCap)
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		// Read errors are expected once the connection is closed; write
		// errors are kept by capped.
		_, _ = io.Copy(capped, conn.Reader)
	}()

	var res Result
	var exited time.Time // when the command was seen to end
	overflow := capped.exceeded
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
wait:
	for {
		select {
		case <-copied:
			exited = time.Now()
			break wait
		case <-overflow:
			overflow = nil
			res.Truncated = true
			s.killAll(ctx)
		case <-ticker.C:
			if running, _, err := s.inspect(runCtx, execID); err == nil && !running {
				exited = time.Now()
				// A background process may still hold the TTY open: give the
				// remaining output a moment, then kill it so the stream ends
				// and everything written so far is drained.
				select {
				case <-copied:
				case <-time.After(drainGrace):
					s.killAll(ctx)
				}
				break wait
			}
		case <-runCtx.Done():
			break wait
		}
	}
	if runCtx.Err() == nil {
		select {
		case <-copied:
		case <-runCtx.Done():
		}
	}
	conn.Close()
	<-copied
	s.killAll(ctx) // nothing may outlive the round
	if exited.IsZero() {
		exited = time.Now()
	}
	res.Duration = exited.Sub(start)

	if err := capped.writeErr(); err != nil {
		return res, fmt.Errorf("run command: write output: %w", err)
	}
	if runCtx.Err() != nil {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.TimedOut = true
		res.ExitCode = 128 + int(syscall.SIGKILL)
		return res, nil
	}
	code, err := s.exitCode(runCtx, execID)
	if err != nil {
		return res, s.gone(fmt.Errorf("run command: %w", err))
	}
	// A vanished container ends the command like a kill would.
	if !s.alive(ctx) {
		return res, fmt.Errorf("run command: %w", ErrSessionGone)
	}
	res.ExitCode = code
	if code == 128+int(syscall.SIGKILL) && res.Duration >= s.opts.Timeout && !res.Truncated {
		res.TimedOut = true
	}
	return res, nil
}

// alive reports whether the container is still running.
func (s *Session) alive(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	res, err := s.d.api.ContainerInspect(ctx, s.id, client.ContainerInspectOptions{})
	return err == nil && res.Container.State != nil && res.Container.State.Running
}

// gone wraps err with ErrSessionGone if the container no longer runs.
func (s *Session) gone(err error) error {
	if s.alive(context.Background()) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrSessionGone, err)
}

// Resize changes the TTY size of the running command, if any.
func (s *Session) Resize(ctx context.Context, size Size) error {
	s.mu.Lock()
	id := s.execID
	s.mu.Unlock()
	if id == "" || size.Width == 0 || size.Height == 0 {
		return nil
	}
	_, err := s.d.api.ExecResize(ctx, id, client.ExecResizeOptions{Height: size.Height, Width: size.Width})
	if err != nil {
		return fmt.Errorf("resize terminal: %w", err)
	}
	return nil
}

func (s *Session) setExec(id string) {
	s.mu.Lock()
	s.execID = id
	s.mu.Unlock()
}

// startExec creates and starts argv as the sandbox user on a TTY.
func (s *Session) startExec(ctx context.Context, argv []string, size Size) (string, client.HijackedResponse, error) {
	console := client.ConsoleSize{Height: size.Height, Width: size.Width}
	ex, err := s.d.api.ExecCreate(ctx, s.id, client.ExecCreateOptions{
		User:         sandboxUser,
		TTY:          true,
		ConsoleSize:  console,
		AttachStdout: true,
		AttachStderr: true,
		WorkingDir:   workDir,
		Cmd:          argv,
	})
	if err != nil {
		return "", client.HijackedResponse{}, fmt.Errorf("create exec: %w", err)
	}
	att, err := s.d.api.ExecAttach(ctx, ex.ID, client.ExecAttachOptions{TTY: true, ConsoleSize: console})
	if err != nil {
		return "", client.HijackedResponse{}, fmt.Errorf("start exec: %w", err)
	}
	return ex.ID, att.HijackedResponse, nil
}

// execWait runs a bash script as the sandbox user, waits for it and returns
// its exit code and (capped) output.
func (s *Session) execWait(ctx context.Context, script string) (int, []byte, error) {
	id, conn, err := s.startExec(ctx, []string{"bash", "--noprofile", "--norc", "-c", script}, FixedSize())
	if err != nil {
		return 0, nil, s.gone(err)
	}
	stop := context.AfterFunc(ctx, conn.Close)
	defer stop()
	var buf bytes.Buffer
	_, _ = io.Copy(newCapWriter(&buf, 64<<10), conn.Reader)
	conn.Close()
	if err := ctx.Err(); err != nil {
		return 0, buf.Bytes(), err
	}
	code, err := s.exitCode(ctx, id)
	return code, buf.Bytes(), err
}

// killAll kills every process of the sandbox user. It runs even if ctx is
// already canceled; failures are ignored because the next Reset kills
// again and Close removes the container anyway.
func (s *Session) killAll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	_, _, _ = s.execWait(ctx, killScript)
}

func (s *Session) inspect(ctx context.Context, execID string) (running bool, exitCode int, err error) {
	res, err := s.d.api.ExecInspect(ctx, execID, client.ExecInspectOptions{})
	if err != nil {
		return false, 0, fmt.Errorf("inspect exec: %w", err)
	}
	return res.Running, res.ExitCode, nil
}

// exitCode waits until the exec has terminated and returns its exit code.
func (s *Session) exitCode(ctx context.Context, execID string) (int, error) {
	for {
		running, code, err := s.inspect(ctx, execID)
		if err != nil {
			return 0, err
		}
		if !running {
			return code, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// capWriter forwards at most limit bytes to w and closes exceeded once more
// output arrives. Dropped output is not an error, so the stream keeps
// draining; a failing w stops the copy and is reported by writeErr.
type capWriter struct {
	w        io.Writer
	left     int64
	exceeded chan struct{}
	once     sync.Once

	mu  sync.Mutex
	err error
}

func newCapWriter(w io.Writer, limit int64) *capWriter {
	return &capWriter{w: w, left: limit, exceeded: make(chan struct{})}
}

func (c *capWriter) Write(p []byte) (int, error) {
	n := len(p)
	if int64(len(p)) > c.left {
		p = p[:c.left]
		c.once.Do(func() { close(c.exceeded) })
	}
	if len(p) > 0 {
		if _, err := c.w.Write(p); err != nil {
			c.mu.Lock()
			c.err = err
			c.mu.Unlock()
			return 0, err
		}
		c.left -= int64(len(p))
	}
	return n, nil
}

func (c *capWriter) writeErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Sweep removes shast containers left behind by shast processes that no
// longer run (e.g. killed with SIGKILL). Containers of live shast processes
// on this host, including the caller's own, are kept, and so are
// containers of other hosts sharing the daemon: their owners (or the
// watchdog) clean them up.
func (d *Docker) Sweep(ctx context.Context) error {
	list, err := d.api.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", labelSession),
	})
	if err != nil {
		return fmt.Errorf("list sandbox containers: %w", err)
	}
	host, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("sweep: %w", err)
	}
	var errs []error
	for _, c := range list.Items {
		owner, ok := c.Labels[labelHost]
		if ok && owner != host || owner == host && processAlive(c.Labels[labelPID]) {
			continue
		}
		errs = append(errs, d.removeContainer(ctx, c.ID))
	}
	return errors.Join(errs...)
}

// processAlive reports whether the process with the given PID exists. A
// reused PID keeps an orphaned container until its watchdog expires.
func processAlive(pid string) bool {
	n, err := strconv.Atoi(pid)
	if err != nil || n <= 0 {
		return false
	}
	p, err := os.FindProcess(n)
	if runtime.GOOS == "windows" {
		// FindProcess opens the process there, which fails if it does not
		// exist; Signal only supports Kill.
		if err == nil {
			p.Release()
		}
		return err == nil || errors.Is(err, os.ErrPermission)
	}
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
