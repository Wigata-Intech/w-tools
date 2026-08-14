package sshx_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Wigata-Intech/w-tools/x/sshx"
)

// testExecs is the scripted command set the exec tests run against.
func testExecs() map[string]execScript {
	return map[string]execScript{
		"ok":   {stdout: "out", stderr: "err", exit: 0},
		"fail": {stdout: "partial", stderr: "boom", exit: 3},
		"slow": {stdout: "late", exit: 0, delay: 300 * time.Millisecond},
	}
}

func TestOutput(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, serverOptions{execs: testExecs()})
	c := dialTestClient(t, s)

	tests := []struct {
		name     string
		input    string
		expected sshx.Result
	}{
		{
			name:     "scripted success",
			input:    "ok",
			expected: sshx.Result{Stdout: []byte("out"), Stderr: []byte("err"), ExitCode: 0},
		},
		{
			name:     "non-zero exit keeps captured output",
			input:    "fail",
			expected: sshx.Result{Stdout: []byte("partial"), Stderr: []byte("boom"), ExitCode: 3},
		},
		{
			name:     "unknown command exits 127",
			input:    "nope",
			expected: sshx.Result{Stderr: []byte("command not found: nope"), ExitCode: 127},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := c.Output(context.Background(), tt.input)
			if tt.expected.ExitCode == 0 {
				if err != nil {
					t.Fatalf("Output() error = %v, want nil", err)
				}
			} else {
				var exitErr *ssh.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("Output() error = %v, want *ssh.ExitError", err)
				}
				if exitErr.ExitStatus() != tt.expected.ExitCode {
					t.Errorf("ExitStatus() = %d, want %d", exitErr.ExitStatus(), tt.expected.ExitCode)
				}
			}
			if !bytes.Equal(res.Stdout, tt.expected.Stdout) {
				t.Errorf("Stdout = %q, want %q", res.Stdout, tt.expected.Stdout)
			}
			if !bytes.Equal(res.Stderr, tt.expected.Stderr) {
				t.Errorf("Stderr = %q, want %q", res.Stderr, tt.expected.Stderr)
			}
			if res.ExitCode != tt.expected.ExitCode {
				t.Errorf("ExitCode = %d, want %d", res.ExitCode, tt.expected.ExitCode)
			}
		})
	}

	t.Run("closed client", func(t *testing.T) {
		t.Parallel()
		c2 := dialTestClient(t, s)
		_ = c2.Close()
		res, err := c2.Output(context.Background(), "ok")
		if !errors.Is(err, sshx.ErrClosed) {
			t.Fatalf("Output() error = %v, want ErrClosed", err)
		}
		if res.ExitCode != -1 {
			t.Errorf("ExitCode = %d, want -1", res.ExitCode)
		}
	})

	t.Run("exec request rejected", func(t *testing.T) {
		t.Parallel()
		s2 := newTestServer(t, serverOptions{rejectExec: true})
		c2 := dialTestClient(t, s2)
		res, err := c2.Output(context.Background(), "ok")
		if err == nil {
			t.Fatal("Output() error = nil, want start failure")
		}
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			t.Errorf("Output() error = %v, want a non-exit start failure", err)
		}
		if res.ExitCode != -1 {
			t.Errorf("ExitCode = %d, want -1", res.ExitCode)
		}
	})

	t.Run("canceled context abandons the command", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		res, err := c.Output(ctx, "slow")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Output() error = %v, want context.DeadlineExceeded", err)
		}
		if res.ExitCode != -1 {
			t.Errorf("ExitCode = %d, want -1", res.ExitCode)
		}
	})
}

func TestCombinedOutput(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, serverOptions{execs: testExecs()})
	c := dialTestClient(t, s)

	t.Run("folds both streams", func(t *testing.T) {
		t.Parallel()
		out, err := c.CombinedOutput(context.Background(), "ok")
		if err != nil {
			t.Fatalf("CombinedOutput() error = %v", err)
		}
		if !strings.Contains(out, "out") || !strings.Contains(out, "err") {
			t.Errorf("CombinedOutput() = %q, want both stdout and stderr content", out)
		}
	})

	t.Run("partial output on failure", func(t *testing.T) {
		t.Parallel()
		out, err := c.CombinedOutput(context.Background(), "fail")
		var exitErr *ssh.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("CombinedOutput() error = %v, want *ssh.ExitError", err)
		}
		if exitErr.ExitStatus() != 3 {
			t.Errorf("ExitStatus() = %d, want 3", exitErr.ExitStatus())
		}
		if !strings.Contains(out, "partial") || !strings.Contains(out, "boom") {
			t.Errorf("CombinedOutput() = %q, want output captured before the failure", out)
		}
	})
}
