package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"shast/internal/outcmp"
)

func TestCapWriter(t *testing.T) {
	tests := []struct {
		name     string
		limit    int64
		writes   []string
		want     string
		exceeded bool
	}{
		{"below limit", 10, []string{"abc", "def"}, "abcdef", false},
		{"exactly limit", 6, []string{"abc", "def"}, "abcdef", false},
		{"split write", 4, []string{"abc", "def"}, "abcd", true},
		{"after limit", 3, []string{"abc", "def", "ghi"}, "abc", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := newCapWriter(&buf, tt.limit)
			for _, s := range tt.writes {
				if n, err := w.Write([]byte(s)); n != len(s) || err != nil {
					t.Fatalf("Write(%q) = %d, %v", s, n, err)
				}
			}
			if buf.String() != tt.want {
				t.Errorf("output %q, want %q", buf.String(), tt.want)
			}
			select {
			case <-w.exceeded:
				if !tt.exceeded {
					t.Error("exceeded closed, want open")
				}
			default:
				if tt.exceeded {
					t.Error("exceeded open, want closed")
				}
			}
		})
	}
}

func TestProcessAlive(t *testing.T) {
	// The test binary itself, running no tests: an exited process on every OS.
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	// Alive, but not ours (EPERM / access denied): init on Unix, System on
	// Windows.
	foreign := "1"
	if runtime.GOOS == "windows" {
		foreign = "4"
	}
	tests := []struct {
		pid  string
		want bool
	}{
		{strconv.Itoa(cmd.Process.Pid), false},
		{strconv.Itoa(os.Getpid()), true},
		{foreign, true},
		{"", false},
		{"abc", false},
		{"-5", false},
	}
	for _, tt := range tests {
		if got := processAlive(tt.pid); got != tt.want {
			t.Errorf("processAlive(%q) = %v, want %v", tt.pid, got, tt.want)
		}
	}
}

// sessionOrSkip starts a session or skips the test when Docker or the
// sandbox image is not available.
func sessionOrSkip(t *testing.T, opts Options) (*Docker, *Session) {
	t.Helper()
	d := dockerOrSkip(t)
	var missing *ImageMissingError
	if err := d.Preflight(t.Context()); errors.As(err, &missing) {
		t.Skipf("%v", err)
	} else if err != nil {
		t.Fatal(err)
	}
	s, err := d.StartSession(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return d, s
}

// run resets the workspace and runs cmd at 80x24.
func run(t *testing.T, s *Session, cmd string) (string, Result) {
	t.Helper()
	if err := s.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	return runNoReset(t, s, cmd)
}

func runNoReset(t *testing.T, s *Session, cmd string) (string, Result) {
	t.Helper()
	var buf bytes.Buffer
	res, err := s.Run(t.Context(), cmd, FixedSize(), &buf)
	if err != nil {
		t.Fatalf("Run(%q): %v", cmd, err)
	}
	return outcmp.Normalize(buf.String()), res
}

func TestSandboxIsolation(t *testing.T) {
	_, s := sessionOrSkip(t, Options{})
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{"user", "whoami; id -u; id -g", "dev\n1000\n1000"},
		{"hostname", "hostname", "web01"},
		{"cwd", "pwd", "/work"},
		{"timezone and locale", "echo $TZ $LC_ALL $HOME", "UTC C.UTF-8 /tmp"},
		{"tty", "test -t 0 && test -t 1 && stty size", "24 80"},
		{"only loopback", "ls /sys/class/net", "lo"},
		{"no network", "timeout 3 bash -c 'echo > /dev/tcp/1.1.1.1/53' 2>/dev/null && echo open || echo blocked", "blocked"},
		{"read-only rootfs", "touch /etc/x /usr/bin/x /seed/x 2>/dev/null || echo denied", "denied"},
		{"no capabilities", "grep -E '^Cap(Inh|Prm|Eff|Bnd|Amb)' /proc/self/status | awk '{print $2}' | sort -u", "0000000000000000"},
		{"no new privileges", "grep NoNewPrivs /proc/self/status | awk '{print $2}'", "1"},
		{"no setuid binaries", "find / -xdev -perm /6000 -type f 2>/dev/null | wc -l", "0"},
		{"no sudo", "command -v sudo || echo none", "none"},
		{"su cannot escalate", "su -c id root </dev/null >/dev/null 2>&1 || echo denied", "denied"},
		{"pids limit", "cat /sys/fs/cgroup/pids.max", "128"},
		{"memory limit", "cat /sys/fs/cgroup/memory.max /sys/fs/cgroup/memory.swap.max", "268435456\n0"},
		{"cpu limit", "cat /sys/fs/cgroup/cpu.max", "100000 100000"},
		{"no core dumps", "ulimit -c", "0"},
		{"no /dev/shm", "test -e /dev/shm && echo present || echo absent", "absent"},
		{"no sysv shared memory", "ipcmk -M 4096 >/dev/null 2>&1 && echo created || echo denied", "denied"},
		{"no message queues", "ipcmk -Q >/dev/null 2>&1 && echo created || echo denied", "denied"},
		// Reset (run before every command) does kill -9 -1 as dev; the
		// watchdog must survive it.
		{"watchdog survives reset", "ps -o user=,comm= -p 1; pgrep -u nobody -x sleep | wc -l", "nobody   docker-init\n1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, res := run(t, s, tt.cmd)
			if got != tt.want {
				t.Errorf("output %q, want %q (exit %d)", got, tt.want, res.ExitCode)
			}
		})
	}
}

func TestRunResult(t *testing.T) {
	_, s := sessionOrSkip(t, Options{Timeout: 2 * time.Second, OutputCap: 64 << 10})
	tests := []struct {
		name      string
		cmd       string
		exitCode  int
		timedOut  bool
		truncated bool
		maxDur    time.Duration
	}{
		{"success", "true", 0, false, false, time.Second},
		{"exit code", "exit 3", 3, false, false, time.Second},
		{"command not found", "no-such-command", 127, false, false, time.Second},
		{"timeout", "sleep 30", 137, true, false, 5 * time.Second},
		{"output cap", "yes", 137, false, true, 5 * time.Second},
		{"background process does not block", "sleep 30 & echo started", 0, false, false, 2 * time.Second},
		{"detached process does not block", "setsid sleep 30 & echo started", 0, false, false, 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Reset(t.Context()); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			res, err := s.Run(t.Context(), tt.cmd, FixedSize(), &buf)
			if err != nil {
				t.Fatal(err)
			}
			if res.ExitCode != tt.exitCode || res.TimedOut != tt.timedOut || res.Truncated != tt.truncated {
				t.Errorf("result %+v, want exit %d timedOut %v truncated %v", res, tt.exitCode, tt.timedOut, tt.truncated)
			}
			if res.Duration > tt.maxDur {
				t.Errorf("took %v, want <= %v", res.Duration, tt.maxDur)
			}
			if int64(buf.Len()) > 64<<10 {
				t.Errorf("output %d bytes exceeds cap", buf.Len())
			}
		})
	}
}

// timedWriter records when the first byte arrived.
type timedWriter struct {
	mu    sync.Mutex
	first time.Time
	buf   bytes.Buffer
}

func (w *timedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.first.IsZero() {
		w.first = time.Now()
	}
	return w.buf.Write(p)
}

func TestRunStreamsLive(t *testing.T) {
	_, s := sessionOrSkip(t, Options{})
	if err := s.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	var w timedWriter
	start := time.Now()
	res, err := s.Run(t.Context(), "echo start; sleep 2; echo end", FixedSize(), &w)
	if err != nil {
		t.Fatal(err)
	}
	if lag := w.first.Sub(start); lag > time.Second {
		t.Errorf("first output after %v, want it streamed before the command ends (%v)", lag, res.Duration)
	}
	if got := outcmp.Normalize(w.buf.String()); got != "start\nend" {
		t.Errorf("output %q", got)
	}
}

// resizeWriter resizes the session once the first size was printed.
type resizeWriter struct {
	s    *Session
	buf  bytes.Buffer
	once sync.Once
	err  error
}

func (w *resizeWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if strings.Contains(w.buf.String(), "20 60") {
		w.once.Do(func() { w.err = w.s.Resize(context.Background(), Size{Width: 100, Height: 30}) })
	}
	return len(p), nil
}

func TestResize(t *testing.T) {
	_, s := sessionOrSkip(t, Options{})
	if err := s.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := &resizeWriter{s: s}
	if _, err := s.Run(t.Context(), "stty size; sleep 1; stty size", Size{Width: 60, Height: 20}, w); err != nil {
		t.Fatal(err)
	}
	if w.err != nil {
		t.Fatal(w.err)
	}
	if got := outcmp.Normalize(w.buf.String()); got != "20 60\n30 100" {
		t.Errorf("sizes %q, want \"20 60\\n30 100\"", got)
	}
}

func TestRunCancel(t *testing.T) {
	_, s := sessionOrSkip(t, Options{})
	if err := s.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := s.Run(ctx, "sleep 30", FixedSize(), &bytes.Buffer{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("cancel took %v", d)
	}
	if got, _ := runNoReset(t, s, "pgrep -u dev -c sleep || true"); got != "0" {
		t.Errorf("sleep still running after cancel: %q", got)
	}
}

func TestReset(t *testing.T) {
	_, s := sessionOrSkip(t, Options{})
	// A listing of /work that covers content, mode and mtime of every path.
	// Directory sizes differ between tmpfs and overlayfs, so sizes are left
	// out; diff -r covers the file contents.
	const snapshot = "cd /work && find . -printf '%p %M %T@\\n' | sort | md5sum; cd /seed && find . -printf '%p %M %T@\\n' | sort | md5sum; diff -r /seed /work && echo same"
	pristine, _ := run(t, s, snapshot)
	lines := strings.Split(pristine, "\n")
	if len(lines) != 3 || lines[0] != lines[1] || lines[2] != "same" {
		t.Fatalf("fresh workspace differs from seed:\n%s", pristine)
	}

	destructive := []string{
		"rm -rf * .[!.]*",
		"find . -name '*.log' -delete",
		"sed -i 's/a/b/g' README.md",
		"chmod -R 000 app configs",
		"chmod 000 /work",
		"echo junk > new.txt; mkdir -p deep/er; touch -d 2000-01-01 README.md",
		"git config --global user.name Mallory; echo x > /tmp/leftover",
		"ln -s /etc/passwd link; ln -s /seed seedlink",
	}
	for _, cmd := range destructive {
		t.Run(cmd, func(t *testing.T) {
			run(t, s, cmd)
			if err := s.Reset(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got, _ := runNoReset(t, s, snapshot); got != pristine {
				t.Errorf("workspace not restored after %q:\n%s", cmd, got)
			}
			if got, _ := runNoReset(t, s, "ls -A /tmp | wc -l"); got != "0" {
				t.Errorf("/tmp not emptied after %q: %s entries", cmd, got)
			}
		})
	}
}

// TestSeedMatchesManifest recomputes the manifest over /seed inside the
// container, so the committed image layer is checked, not just the file
// seed.sh wrote about itself.
func TestSeedMatchesManifest(t *testing.T) {
	_, s := sessionOrSkip(t, Options{})
	const recompute = `cd /seed && find . -printf '%p\t%M\t%s\t%TY-%Tm-%Td %TH:%TM:%TS\n' | while IFS=$'\t' read -r p m sz mt; do
sum=-; [[ -f $p && ! -L $p ]] && sum=$(sha256sum <"$p" | cut -d' ' -f1); [[ -d $p ]] && sz=-
printf '%s %s %s %s %s\n' "$p" "$m" "$sz" "$mt" "$sum"; done | LC_ALL=C sort | cmp - /seed.sha256 && echo match`
	if got, res := run(t, s, recompute); got != "match" {
		t.Errorf("seed differs from /seed.sha256 (exit %d): %s", res.ExitCode, got)
	}
}

// Background processes must not outlive their round, not even until the
// next Reset: Run kills them before it returns.
func TestBackgroundProcessesDie(t *testing.T) {
	_, s := sessionOrSkip(t, Options{})
	cmds := []string{
		"sleep 300 & echo started",
		"setsid sleep 300 & nohup sleep 301 >/dev/null 2>&1 & (trap '' HUP; exec sleep 302) & sleep 0.5; echo started",
	}
	for _, cmd := range cmds {
		t.Run(cmd, func(t *testing.T) {
			run(t, s, cmd)
			if got, _ := runNoReset(t, s, "pgrep -u dev -c sleep || true"); got != "0" {
				t.Errorf("background processes survived the round: %q", got)
			}
		})
	}
}

// TestResetSurvivesAbuse checks that no command can break Reset.
func TestResetSurvivesAbuse(t *testing.T) {
	_, s := sessionOrSkip(t, Options{Timeout: 3 * time.Second})
	abuse := []string{
		// More names than fit into one argument list.
		"seq -f '%0200g' 12000 | xargs touch; ls | wc -l",
		"cd /tmp && seq -f '%0200g' 12000 | xargs touch; ls | wc -l",
		// Fork bomb against the pids limit.
		"b(){ b|b& }; b",
		// Fill the tmpfs.
		"head -c 100M /dev/zero > big; ls -l big",
	}
	for _, cmd := range abuse {
		t.Run(cmd, func(t *testing.T) {
			run(t, s, cmd)
			if got, _ := run(t, s, "ls | wc -l; ls -A /tmp | wc -l; echo ok"); got != "10\n0\nok" {
				t.Errorf("session broken after %q: %q", cmd, got)
			}
		})
	}
}

// slowWriter delays every write like a slow terminal UI.
type slowWriter struct {
	delay time.Duration
	buf   bytes.Buffer
}

func (w *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(w.delay)
	return w.buf.Write(p)
}

func TestRunSlowWriterGetsAllOutput(t *testing.T) {
	_, s := sessionOrSkip(t, Options{})
	if err := s.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := &slowWriter{delay: 2 * time.Millisecond}
	if _, err := s.Run(t.Context(), "head -c 300000 /dev/zero | tr '\\0' a; echo END", FixedSize(), w); err != nil {
		t.Fatal(err)
	}
	got := outcmp.Normalize(w.buf.String())
	if !strings.HasSuffix(got, "END") || strings.Count(got, "a") != 300000 {
		t.Errorf("lost output: %d bytes, suffix %q", len(got), got[max(0, len(got)-10):])
	}
}

type failingWriter struct{}

var errWriteFailed = errors.New("write failed")

func (failingWriter) Write([]byte) (int, error) { return 0, errWriteFailed }

func TestRunWriterError(t *testing.T) {
	_, s := sessionOrSkip(t, Options{})
	if err := s.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := s.Run(t.Context(), "while true; do echo x; sleep 0.1; done", FixedSize(), failingWriter{})
	if !errors.Is(err, errWriteFailed) {
		t.Errorf("err = %v, want the writer's error", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v; a failing writer must end the command early", d)
	}
}

// TestSessionGone simulates the watchdog removing the container. (When it
// happens in the middle of a command, detection is best effort; the next
// Reset reports it reliably.)
func TestSessionGone(t *testing.T) {
	d, s := sessionOrSkip(t, Options{})
	if err := d.removeContainer(t.Context(), s.id); err != nil {
		t.Fatal(err)
	}
	waitSessionContainers(t, d, s.ID(), 0)
	if err := s.Reset(t.Context()); !errors.Is(err, ErrSessionGone) {
		t.Errorf("Reset: err = %v, want ErrSessionGone", err)
	}
	if _, err := s.Run(t.Context(), "true", FixedSize(), &bytes.Buffer{}); !errors.Is(err, ErrSessionGone) {
		t.Errorf("Run: err = %v, want ErrSessionGone", err)
	}
}

func TestCloseRemovesContainer(t *testing.T) {
	d, s := sessionOrSkip(t, Options{})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := waitSessionContainers(t, d, s.ID(), 0); n != 0 {
		t.Errorf("%d containers left after Close", n)
	}
}

func TestSweep(t *testing.T) {
	d, live := sessionOrSkip(t, Options{})
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	orphan, err := d.startSession(t.Context(), Options{}, dead.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { orphan.Close() })
	// A container of a shast on another host sharing the daemon.
	const foreign = "foreign-session"
	c, err := d.api.ContainerCreate(t.Context(), client.ContainerCreateOptions{
		Image:  ImageTag(),
		Config: &container.Config{Labels: map[string]string{labelSession: foreign, labelHost: "other-host", labelPID: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.removeContainer(context.Background(), c.ID) })

	if err := d.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := waitSessionContainers(t, d, orphan.ID(), 0); n != 0 {
		t.Errorf("orphaned container not swept")
	}
	if n := waitSessionContainers(t, d, live.ID(), 1); n != 1 {
		t.Errorf("live container swept")
	}
	if n := waitSessionContainers(t, d, foreign, 1); n != 1 {
		t.Errorf("container of another host swept")
	}
}

// waitSessionContainers returns the number of containers of a session,
// polling for a few seconds until it equals want (removal is asynchronous
// with AutoRemove).
func waitSessionContainers(t *testing.T, d *Docker, id string, want int) int {
	t.Helper()
	var n int
	for range 50 {
		list, err := d.api.ContainerList(t.Context(), client.ContainerListOptions{
			All:     true,
			Filters: make(client.Filters).Add("label", labelSession+"="+id),
		})
		if err != nil {
			t.Fatal(err)
		}
		if n = len(list.Items); n == want {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return n
}
