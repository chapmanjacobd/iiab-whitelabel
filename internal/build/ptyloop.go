package build

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

var ErrTimeout = errors.New("ptyloop: timed out waiting for output")

// PTYLoop provides a buffered expect loop similar to TCL's `expect { ... exp_continue }`.
// A goroutine continuously reads from the PTY master into an internal buffer.
// WaitForAny() scans the accumulated buffer against multiple patterns in a single pass,
// avoiding the N-calls = N-reads overhead of per-call expect libraries.
//
// PTY lifecycle:
//
//	el, _ := NewPTYLoop(...)        // open master + slave
//	el.StartCommand(cmd)            // wire slave to cmd, start it, close our slave copy
//	// read/write el.Master() from our side
//	cmd.Wait()                      // child exits → slave closes → master reads EOF → el.Close()
//	el.Close()                      // close master, wait for reader goroutine
type PTYLoop struct {
	mu       sync.Mutex
	cond     *sync.Cond
	buf      strings.Builder
	consumed int // bytes already matched and consumed
	master   *os.File
	slave    *os.File
	stdout   io.Writer
	done     chan struct{}
	err      error // set when reader goroutine exits with error
	eof      bool  // set when PTY master returns EOF
}

// PTYLoopConfig holds options for creating a PTYLoop.
type PTYLoopConfig struct {
	Stdout io.Writer // optional, output is mirrored here
}

// NewPTYLoop creates a PTY, starts the reader goroutine, and returns the loop.
func NewPTYLoop(cfg PTYLoopConfig) (*PTYLoop, error) {
	master, slave, err := pty.Open()
	if err != nil {
		return nil, err
	}

	el := &PTYLoop{
		master: master,
		slave:  slave,
		stdout: cfg.Stdout,
		done:   make(chan struct{}),
	}
	el.cond = sync.NewCond(&el.mu)

	el.startReader()

	return el, nil
}

func (el *PTYLoop) startReader() {
	go func() {
		defer close(el.done)

		buf := make([]byte, 8192)
		for {
			n, readErr := el.master.Read(buf)
			if n > 0 {
				if el.stdout != nil {
					_, _ = el.stdout.Write(buf[:n])
				}

				el.mu.Lock()
				el.buf.Write(buf[:n])
				el.cond.Broadcast()
				el.mu.Unlock()
			}
			if readErr != nil {
				el.mu.Lock()
				el.eof = true
				if !errors.Is(readErr, io.EOF) {
					el.err = readErr
				}
				el.cond.Broadcast()
				el.mu.Unlock()
				return
			}
		}
	}()
}

// Master returns the PTY master file descriptor for reading/writing from
// our side of the connection.
func (el *PTYLoop) Master() *os.File {
	return el.master
}

// Slave returns the PTY slave file descriptor for wiring to a child process.
func (el *PTYLoop) Slave() *os.File {
	return el.slave
}

// StartCommand wires the PTY slave to cmd.Stdin/Stdout/Stderr and starts it.
// After a successful start it closes the PTYLoop's copy of the slave so that
// EOF propagates correctly once the child exits.
func (el *PTYLoop) StartCommand(cmd *exec.Cmd) error {
	cmd.Stdin = el.slave
	cmd.Stdout = el.slave
	cmd.Stderr = el.slave
	if err := cmd.Start(); err != nil {
		return err
	}
	// Close our copy -- the child holds its own fd via dup(2).
	// EOF arrives only when all copies (child + ours) are closed.
	_ = el.slave.Close()
	el.slave = nil
	return nil
}

// Send writes text to the PTY master.
func (el *PTYLoop) Send(s string) error {
	_, err := el.master.Write([]byte(s))
	return err
}

// SendLine writes text followed by a carriage return.
func (el *PTYLoop) SendLine(s string) error {
	return el.Send(s + "\r")
}

// WaitForAny scans the accumulated buffer against all patterns. It returns as soon as
// any pattern matches, returning the matched string, the index of the matching pattern,
// and nil error. If the PTY reaches EOF before any pattern matches, it returns
// ("", -1, [io.EOF]).
// If the timeout elapses, it returns ("", -1, [ErrTimeout]).
func (el *PTYLoop) WaitForAny(patterns []*regexp.Regexp, timeout time.Duration) (match string, idx int, err error) {
	deadline := time.Now().Add(timeout)

	el.mu.Lock()
	defer el.mu.Unlock()

	for {
		// Check EOF condition
		if el.eof {
			// Drain: check if any pattern matches in remaining buffer
			full := el.buf.String()
			unconsumed := full[el.consumed:]
			for i, re := range patterns {
				if m := re.FindString(unconsumed); m != "" {
					pos := strings.Index(unconsumed, m)
					el.consumed += pos + len(m)
					return m, i, nil
				}
			}
			if el.err != nil {
				return "", -1, el.err
			}
			return "", -1, io.EOF
		}

		// Scan unconsumed buffer against all patterns
		full := el.buf.String()
		unconsumed := full[el.consumed:]
		for i, re := range patterns {
			if m := re.FindString(unconsumed); m != "" {
				pos := strings.Index(unconsumed, m)
				el.consumed += pos + len(m)
				return m, i, nil
			}
		}

		// No match yet -- wait for more data with timeout
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return "", -1, ErrTimeout
		}

		// Set a timer to wake us up at the deadline
		timer := time.AfterFunc(remaining, func() {
			el.cond.Broadcast()
		})

		el.cond.Wait()
		timer.Stop()
	}
}

// WaitForString is a convenience wrapper around WaitForAny for a literal string match.
func (el *PTYLoop) WaitForString(s string, timeout time.Duration) (string, error) {
	re := regexp.MustCompile(regexp.QuoteMeta(s))
	match, _, err := el.WaitForAny([]*regexp.Regexp{re}, timeout)
	return match, err
}

// AwaitPrompt waits for the shell prompt `#` to appear and returns an error
// if the timeout elapses. It discards the matched text since callers only
// care that the prompt was seen.
func (el *PTYLoop) AwaitPrompt(timeout time.Duration) error {
	_, _, err := el.WaitForAny([]*regexp.Regexp{rePrompt}, timeout)
	if err != nil {
		return fmt.Errorf("timeout waiting for prompt: %w", err)
	}
	return nil
}

// WaitEOF waits until the PTY reaches EOF or the timeout elapses.
func (el *PTYLoop) WaitEOF(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	el.mu.Lock()
	defer el.mu.Unlock()

	for {
		if el.eof {
			if el.err != nil {
				return el.err
			}
			return nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return ErrTimeout
		}

		timer := time.AfterFunc(remaining, func() {
			el.cond.Broadcast()
		})

		el.cond.Wait()
		timer.Stop()
	}
}

// Close closes the PTY master and waits for the reader goroutine to exit.
func (el *PTYLoop) Close() error {
	if el.master != nil {
		_ = el.master.Close()
	}
	<-el.done
	return nil
}
