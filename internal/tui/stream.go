package tui

import (
	"context"
	"sync"

	tea "charm.land/bubbletea/v2"

	"shast/internal/sandbox"
)

// streamMsg delivers output of the running command to the model. Output
// that arrived since the last message is coalesced into one message.
type streamMsg struct {
	gen  int
	data []byte
	done bool // the command finished; res and err are set
	res  sandbox.Result
	err  error
}

// stream connects the goroutine running a command with the Bubble Tea
// loop. Writes never block, so a slow UI cannot stall the command; the
// sandbox output cap bounds the buffer.
type stream struct {
	mu      sync.Mutex
	buf     []byte
	done    bool
	res     sandbox.Result
	err     error
	size    sandbox.Size  // latest wanted TTY size
	started bool          // Run was called, resizes go to the runner
	notify  chan struct{} // capacity 1: "something new is available"

	resizeMu sync.Mutex // serializes resize calls
}

func newStream(size sandbox.Size) *stream {
	return &stream{size: size, notify: make(chan struct{}, 1)}
}

// setSize records the wanted TTY size; applySize sends it to the runner.
func (s *stream) setSize(size sandbox.Size) {
	s.mu.Lock()
	s.size = size
	s.mu.Unlock()
}

// applySize resizes the running command to the latest wanted size. Calls
// are serialized and always apply the latest size, so a delayed call
// cannot restore an old one. Before the command starts, run picks the
// size up itself.
func (s *stream) applySize(ctx context.Context, runner Runner) error {
	s.resizeMu.Lock()
	defer s.resizeMu.Unlock()
	s.mu.Lock()
	size, started := s.size, s.started
	s.mu.Unlock()
	if !started {
		return nil
	}
	return runner.Resize(ctx, size)
}

// Write implements io.Writer for the runner's output.
func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.buf = append(s.buf, p...)
	s.mu.Unlock()
	s.signal()
	return len(p), nil
}

func (s *stream) signal() {
	select {
	case s.notify <- struct{}{}:
	default: // a notification is already pending
	}
}

// run resets the workspace and runs cmd; it is meant to run in its own
// goroutine and always finishes the stream.
func (s *stream) run(ctx context.Context, runner Runner, cmd string) {
	var res sandbox.Result
	err := runner.Reset(ctx)
	if err == nil {
		// Take the size only now: the window may have been resized while
		// the workspace was reset.
		s.resizeMu.Lock()
		s.mu.Lock()
		size := s.size
		s.started = true
		s.mu.Unlock()
		s.resizeMu.Unlock()
		res, err = runner.Run(ctx, cmd, size, s)
	}
	s.mu.Lock()
	s.done, s.res, s.err = true, res, err
	s.mu.Unlock()
	s.signal()
}

// wait returns a command that blocks until new output or the end of the
// run is available.
func (s *stream) wait(gen int) tea.Cmd {
	return func() tea.Msg {
		<-s.notify
		s.mu.Lock()
		defer s.mu.Unlock()
		msg := streamMsg{gen: gen, data: s.buf, done: s.done, res: s.res, err: s.err}
		s.buf = nil
		return msg
	}
}
