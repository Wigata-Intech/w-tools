package sshx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Result is the outcome of a one-shot command.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int // -1 when the command never returned a status
}

// Output runs cmd in a fresh session over the shared transport and returns
// stdout and stderr separately. Like os/exec, output captured before a
// failure is still returned: a non-zero exit populates Result and returns the
// wrapped *ssh.ExitError. ctx cancellation closes the in-flight session (the
// remote command is abandoned, not killed) and returns ctx's error.
func (c *Client) Output(ctx context.Context, cmd string) (Result, error) {
	var stdout, stderr syncBuffer
	err := c.run(ctx, cmd, &stdout, &stderr)
	return Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: exitCode(err)}, err
}

// CombinedOutput runs cmd like Output but folds stdout and stderr into one
// string, preserving their interleaving.
func (c *Client) CombinedOutput(ctx context.Context, cmd string) (string, error) {
	var combined syncBuffer
	err := c.run(ctx, cmd, &combined, &combined)
	return combined.String(), err
}

// run executes cmd with the given writers, honoring ctx cancellation. The
// writers must be safe for concurrent use: on cancellation the session is
// abandoned without waiting for the transport's copy goroutines to finish.
func (c *Client) run(ctx context.Context, cmd string, stdout, stderr io.Writer) error {
	if err := c.closedErr(); err != nil {
		return err
	}
	sess, err := c.c.NewSession()
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	sess.Stdout = stdout
	sess.Stderr = stderr
	if err := sess.Start(cmd); err != nil {
		return err
	}
	// ch is buffered so the Wait goroutine sends and exits unattended: on a
	// black-holed transport Wait unblocks only when the connection dies,
	// which the keepalive guarantees by closing the client on a failed ping.
	ch := make(chan error, 1)
	go func() { ch <- sess.Wait() }()
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		_ = sess.Close()
		return ctx.Err()
	}
}

// exitCode maps a command error onto a shell-style exit code.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return exit.ExitStatus()
	}
	return -1
}

// syncBuffer serializes writes: the transport pumps stdout and stderr from
// separate goroutines, so a shared buffer needs the lock.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncBuffer) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.b.Bytes()...)
}
