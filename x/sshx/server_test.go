package sshx_test

// This file is the in-process SSH server the whole test suite dials. It
// terminates real x/crypto handshakes on loopback — no network beyond
// 127.0.0.1, no fixtures — so auth, host-key, exec, PTY, and pool-healing
// paths are all exercised against a live peer.

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

var (
	errUnknownPublicKey = errors.New("unknown public key")
	errWrongPassword    = errors.New("wrong password")
)

// execScript is a scripted response for one exec command.
type execScript struct {
	stdout string
	stderr string
	exit   int
	delay  time.Duration // pause before finishing, for cancellation tests
}

// ptyRecord is one pty-req as the server saw it.
type ptyRecord struct {
	term       string
	cols, rows uint32
}

// winRecord is one window-change as the server saw it.
type winRecord struct {
	cols, rows uint32
}

// serverOptions configures a test server's auth and behavior.
type serverOptions struct {
	authorizedKey ssh.PublicKey // nil: public-key auth disabled
	password      string        // "": password auth disabled
	execs         map[string]execScript
	rejectExec    bool // refuse every exec request
	rejectPty     bool // refuse every pty-req
	rejectShell   bool // refuse every shell request
}

type testServer struct {
	t          *testing.T
	ln         net.Listener
	hostSigner ssh.Signer
	opts       serverOptions

	mu      sync.Mutex
	conns   []net.Conn
	ptyReqs []ptyRecord
	winChs  []winRecord
	closed  bool

	wg sync.WaitGroup
}

// newTestSigner generates a fresh ed25519 signer.
//
//nolint:ireturn // ssh.Signer is x/crypto's contract type for private keys
func newTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer from key: %v", err)
	}
	return signer
}

// newTestServer starts an SSH server on loopback and registers cleanup.
func newTestServer(t *testing.T, opts serverOptions) *testServer {
	t.Helper()
	s := &testServer{t: t, hostSigner: newTestSigner(t), opts: opts}

	cfg := &ssh.ServerConfig{}
	if opts.authorizedKey == nil && opts.password == "" {
		cfg.NoClientAuth = true
	}
	if opts.authorizedKey != nil {
		want := string(ssh.MarshalAuthorizedKey(opts.authorizedKey))
		cfg.PublicKeyCallback = func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(ssh.MarshalAuthorizedKey(key)) == want {
				return nil, nil //nolint:nilnil // (nil, nil) is ssh.ServerConfig's documented success return
			}
			return nil, errUnknownPublicKey
		}
	}
	if opts.password != "" {
		cfg.PasswordCallback = func(_ ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == opts.password {
				return nil, nil //nolint:nilnil // (nil, nil) is ssh.ServerConfig's documented success return
			}
			return nil, errWrongPassword
		}
	}
	cfg.AddHostKey(s.hostSigner)

	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.ln = ln
	s.wg.Add(1)
	go s.acceptLoop(cfg)
	t.Cleanup(s.close)
	return s
}

func (s *testServer) addr() string { return s.ln.Addr().String() }

// hostKeyCallback returns a client-side verifier pinned to this server's key.
func (s *testServer) hostKeyCallback() ssh.HostKeyCallback {
	return ssh.FixedHostKey(s.hostSigner.PublicKey())
}

// killConns severs every live transport without stopping the listener —
// the connection dies, the server survives, a redial succeeds.
func (s *testServer) killConns() {
	s.mu.Lock()
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// ptys returns the pty-reqs seen so far.
func (s *testServer) ptys() []ptyRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ptyRecord(nil), s.ptyReqs...)
}

// windowChanges returns the window-change requests seen so far.
func (s *testServer) windowChanges() []winRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]winRecord(nil), s.winChs...)
}

func (s *testServer) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	_ = s.ln.Close()
	s.killConns()
	s.wg.Wait()
}

func (s *testServer) acceptLoop(cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		s.conns = append(s.conns, conn)
		s.wg.Add(1)
		s.mu.Unlock()
		go s.handleConn(conn, cfg)
	}
}

func (s *testServer) handleConn(conn net.Conn, cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer func() { _ = sconn.Close() }()
	go ssh.DiscardRequests(reqs) // replies false to keepalives — a reply is all Ping needs
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only session channels here")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		s.wg.Add(1)
		go s.handleSession(ch, chReqs)
	}
}

func (s *testServer) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer s.wg.Done()
	defer func() { _ = ch.Close() }()
	for req := range reqs {
		switch req.Type {
		case "exec":
			var p struct{ Command string }
			if err := ssh.Unmarshal(req.Payload, &p); err != nil || s.opts.rejectExec {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			s.runExec(ch, p.Command)
			return
		case "shell":
			if s.opts.rejectShell {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			// The shell runs concurrently so this loop keeps draining
			// requests — window-change arrives mid-shell and must be seen.
			go func() {
				s.runShell(ch)
				_ = ch.Close()
			}()
		case "pty-req":
			var p struct {
				Term         string
				Cols, Rows   uint32
				WPx, HPx     uint32
				EncodedModes string
			}
			if err := ssh.Unmarshal(req.Payload, &p); err != nil || s.opts.rejectPty {
				_ = req.Reply(false, nil)
				continue
			}
			s.mu.Lock()
			s.ptyReqs = append(s.ptyReqs, ptyRecord{term: p.Term, cols: p.Cols, rows: p.Rows})
			s.mu.Unlock()
			_ = req.Reply(true, nil)
		case "window-change":
			var p struct {
				Cols, Rows uint32
				WPx, HPx   uint32
			}
			if err := ssh.Unmarshal(req.Payload, &p); err == nil {
				s.mu.Lock()
				s.winChs = append(s.winChs, winRecord{cols: p.Cols, rows: p.Rows})
				s.mu.Unlock()
			}
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

// runExec plays the script for cmd; unknown commands exit 127 like a shell.
func (s *testServer) runExec(ch ssh.Channel, cmd string) {
	script, ok := s.opts.execs[cmd]
	if !ok {
		script = execScript{stderr: "command not found: " + cmd, exit: 127}
	}
	if script.delay > 0 {
		time.Sleep(script.delay)
	}
	if script.stdout != "" {
		_, _ = ch.Write([]byte(script.stdout))
	}
	if script.stderr != "" {
		_, _ = ch.Stderr().Write([]byte(script.stderr))
	}
	s.sendExit(ch, script.exit)
}

// runShell is a minimal line shell: announces readiness, echoes lines back
// prefixed "echo:", exits cleanly on "exit".
func (s *testServer) runShell(ch ssh.Channel) {
	_, _ = ch.Write([]byte("ready\n"))
	sc := bufio.NewScanner(ch)
	for sc.Scan() {
		line := sc.Text()
		if line == "exit" {
			s.sendExit(ch, 0)
			return
		}
		_, _ = fmt.Fprintf(ch, "echo:%s\n", line)
	}
}

func (s *testServer) sendExit(ch ssh.Channel, code int) {
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)})) //#nosec G115 -- test exit codes are tiny non-negatives
}
