package sshx_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Wigata-Intech/w-tools/x/sshx"
)

func TestShell(t *testing.T) {
	t.Parallel()

	t.Run("streams without tty", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{})
		c := dialTestClient(t, s)
		stdinR, stdinW := io.Pipe()
		stdoutR, stdoutW := io.Pipe()
		sess, err := c.Shell(context.Background(), sshx.SessionConfig{Stdin: stdinR, Stdout: stdoutW})
		if err != nil {
			t.Fatalf("Shell() error = %v", err)
		}
		defer func() { _ = sess.Close() }()

		br := bufio.NewReader(stdoutR)
		readLine := func(want string) {
			t.Helper()
			line, err := br.ReadString('\n')
			if err != nil {
				t.Fatalf("read shell output: %v", err)
			}
			if line != want {
				t.Fatalf("shell output = %q, want %q", line, want)
			}
		}
		readLine("ready\n")
		if _, err := fmt.Fprintln(stdinW, "hello"); err != nil {
			t.Fatalf("write stdin: %v", err)
		}
		readLine("echo:hello\n")
		if _, err := fmt.Fprintln(stdinW, "exit"); err != nil {
			t.Fatalf("write stdin: %v", err)
		}
		if err := sess.Wait(); err != nil {
			t.Errorf("Wait() error = %v, want nil", err)
		}
	})

	t.Run("tty custom size", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{})
		c := dialTestClient(t, s)
		sess, err := c.Shell(context.Background(), sshx.SessionConfig{
			TTY: &sshx.TTYConfig{Term: "vt100", Cols: 132, Rows: 43},
		})
		if err != nil {
			t.Fatalf("Shell() error = %v", err)
		}
		defer func() { _ = sess.Close() }()
		waitFor(t, 2*time.Second, func() bool {
			for _, p := range s.ptys() {
				if p.term == "vt100" && p.cols == 132 && p.rows == 43 {
					return true
				}
			}
			return false
		})
	})

	t.Run("tty defaults", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{})
		c := dialTestClient(t, s)
		sess, err := c.Shell(context.Background(), sshx.SessionConfig{TTY: &sshx.TTYConfig{}})
		if err != nil {
			t.Fatalf("Shell() error = %v", err)
		}
		defer func() { _ = sess.Close() }()
		waitFor(t, 2*time.Second, func() bool {
			for _, p := range s.ptys() {
				if p.term == "xterm-256color" && p.cols == 80 && p.rows == 24 {
					return true
				}
			}
			return false
		})
	})

	t.Run("pty request rejected", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{rejectPty: true})
		c := dialTestClient(t, s)
		if _, err := c.Shell(context.Background(), sshx.SessionConfig{TTY: &sshx.TTYConfig{}}); err == nil {
			t.Error("Shell() error = nil, want pty-req failure")
		}
	})

	t.Run("shell request rejected", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{rejectShell: true})
		c := dialTestClient(t, s)
		if _, err := c.Shell(context.Background(), sshx.SessionConfig{}); err == nil {
			t.Error("Shell() error = nil, want shell-request failure")
		}
	})

	t.Run("closed client", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{})
		c := dialTestClient(t, s)
		_ = c.Close()
		if _, err := c.Shell(context.Background(), sshx.SessionConfig{}); !errors.Is(err, sshx.ErrClosed) {
			t.Errorf("Shell() error = %v, want ErrClosed", err)
		}
	})

	t.Run("dead transport", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{})
		c := dialTestClient(t, s)
		s.killConns()
		_, err := c.Shell(context.Background(), sshx.SessionConfig{})
		if err == nil {
			t.Error("Shell() error = nil, want session-open failure")
		}
		if errors.Is(err, sshx.ErrClosed) {
			t.Error("Shell() error = ErrClosed, want transport error: Close was never called")
		}
	})
}

func TestResize(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, serverOptions{})
	c := dialTestClient(t, s)
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	sess, err := c.Shell(context.Background(), sshx.SessionConfig{
		Stdin:  stdinR,
		Stdout: stdoutW,
		TTY:    &sshx.TTYConfig{},
	})
	if err != nil {
		t.Fatalf("Shell() error = %v", err)
	}
	defer func() { _ = sess.Close() }()

	if err := sess.Resize(150, 45); err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		for _, w := range s.windowChanges() {
			if w.cols == 150 && w.rows == 45 {
				return true
			}
		}
		return false
	})
	br := bufio.NewReader(stdoutR)
	if line, err := br.ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("shell output = %q, %v, want %q", line, err, "ready\n")
	}
	if _, err := fmt.Fprintln(stdinW, "after-resize"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if line, err := br.ReadString('\n'); err != nil || line != "echo:after-resize\n" {
		t.Fatalf("shell output after Resize = %q, %v, want %q", line, err, "echo:after-resize\n")
	}
	if _, err := fmt.Fprintln(stdinW, "exit"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := sess.Wait(); err != nil {
		t.Errorf("Wait() error = %v, want nil", err)
	}
}

func TestWait(t *testing.T) {
	t.Parallel()

	t.Run("context cancel unblocks", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{})
		c := dialTestClient(t, s)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sess, err := c.Shell(ctx, sshx.SessionConfig{})
		if err != nil {
			t.Fatalf("Shell() error = %v", err)
		}
		defer func() { _ = sess.Close() }()

		done := make(chan error, 1)
		go func() { done <- sess.Wait() }()
		cancel()
		select {
		case err := <-done:
			var missing *ssh.ExitMissingError
			if !errors.As(err, &missing) {
				t.Errorf("Wait() error = %v, want *ssh.ExitMissingError", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Wait() did not unblock after context cancel")
		}
	})
}

func TestSessionClose(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, serverOptions{})
	c := dialTestClient(t, s)
	sess, err := c.Shell(context.Background(), sshx.SessionConfig{})
	if err != nil {
		t.Fatalf("Shell() error = %v", err)
	}

	err1 := sess.Close()
	err2 := sess.Close()
	if err2 != err1 { //nolint:err113,errorlint // the Close contract is same-value, which errors.Is would weaken
		t.Errorf("second Close() = %v, want same value as first (%v)", err2, err1)
	}
}
