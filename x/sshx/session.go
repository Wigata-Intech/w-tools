package sshx

import (
	"context"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

// TTYConfig requests a remote pseudo-terminal. The library never inspects the
// local environment — the consumer supplies the terminal type and size it
// wants the remote side to see.
type TTYConfig struct {
	Term       string // DefaultTerm when empty
	Cols, Rows int    // DefaultCols x DefaultRows when non-positive
}

// SessionConfig wires an interactive session to the consumer's streams. Any
// nil stream is simply not connected.
type SessionConfig struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	TTY            *TTYConfig // nil: no PTY is requested
}

// Session is a live interactive shell on the remote host.
type Session struct {
	sess *ssh.Session
	stop func() bool // detaches the ctx watcher

	closeOnce sync.Once
	closeErr  error
}

// Shell starts the remote login shell wired to cfg's streams. ctx governs the
// session's lifetime: cancellation closes it and unblocks Wait. The library
// touches no terminal state — raw mode, size discovery, and resize signals
// are the consumer's, fed in through cfg and Resize.
func (c *Client) Shell(ctx context.Context, cfg SessionConfig) (*Session, error) {
	if err := c.closedErr(); err != nil {
		return nil, err
	}
	sess, err := c.c.NewSession()
	if err != nil {
		return nil, err
	}
	sess.Stdin = cfg.Stdin
	sess.Stdout = cfg.Stdout
	sess.Stderr = cfg.Stderr
	if cfg.TTY != nil {
		term := cfg.TTY.Term
		if term == "" {
			term = DefaultTerm
		}
		cols, rows := cfg.TTY.Cols, cfg.TTY.Rows
		if cols <= 0 {
			cols = DefaultCols
		}
		if rows <= 0 {
			rows = DefaultRows
		}
		modes := ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: defaultTermSpeed,
			ssh.TTY_OP_OSPEED: defaultTermSpeed,
		}
		if err := sess.RequestPty(term, rows, cols, modes); err != nil {
			_ = sess.Close()
			return nil, err
		}
	}
	if err := sess.Shell(); err != nil {
		_ = sess.Close()
		return nil, err
	}
	s := &Session{sess: sess}
	s.stop = context.AfterFunc(ctx, func() { _ = s.Close() })
	return s, nil
}

// Resize informs the remote PTY of a new size. It is a no-op server-side
// unless a PTY was requested.
func (s *Session) Resize(cols, rows int) error {
	return s.sess.WindowChange(rows, cols)
}

// Wait blocks until the remote shell exits — by the user ending it, the
// connection dying, or the session's ctx being canceled.
func (s *Session) Wait() error {
	err := s.sess.Wait()
	s.stop()
	return err
}

// Close tears the session down. It is idempotent; every call returns the
// first close's error.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.stop()
		s.closeErr = s.sess.Close()
	})
	return s.closeErr
}
