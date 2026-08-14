package sshx_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Wigata-Intech/w-tools/x/sshx"
)

// startBenchServer is a minimal exec-only SSH server for benchmarks; the
// *testing.T-bound server in server_test.go cannot be driven by a *testing.B.
func startBenchServer(b *testing.B) (string, ssh.HostKeyCallback) {
	b.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		b.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		b.Fatalf("signer from key: %v", err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("listen: %v", err)
	}
	b.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer func() { _ = sconn.Close() }()
				go ssh.DiscardRequests(reqs)
				for newCh := range chans {
					ch, chReqs, err := newCh.Accept()
					if err != nil {
						continue
					}
					go func(ch ssh.Channel, chReqs <-chan *ssh.Request) {
						defer func() { _ = ch.Close() }()
						for req := range chReqs {
							if req.Type != "exec" {
								if req.WantReply {
									_ = req.Reply(false, nil)
								}
								continue
							}
							_ = req.Reply(true, nil)
							_, _ = ch.Write([]byte("out"))
							_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							return
						}
					}(ch, chReqs)
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), ssh.FixedHostKey(signer.PublicKey())
}

func benchConfig(hostKey ssh.HostKeyCallback) sshx.Config {
	return sshx.Config{
		User:    "bench",
		Auth:    []ssh.AuthMethod{ssh.Password("unused")},
		HostKey: hostKey,
	}
}

func BenchmarkManagedCombinedOutput(b *testing.B) {
	addr, hostKey := startBenchServer(b)
	p := sshx.NewPool(0)
	b.Cleanup(p.Close)
	m := p.Add(sshx.ManagedConfig{Dial: func(ctx context.Context) (*sshx.Client, error) {
		return sshx.Dial(ctx, addr, benchConfig(hostKey))
	}})
	deadline := time.Now().Add(5 * time.Second)
	for m.State() != sshx.StateReady {
		if time.Now().After(deadline) {
			b.Fatal("managed connection never became ready")
		}
		time.Sleep(time.Millisecond)
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := m.CombinedOutput(context.Background(), "noop"); err != nil {
			b.Fatalf("CombinedOutput() error = %v", err)
		}
	}
}

// BenchmarkPoolColdStart prices bringing a 16-host fleet from Add to Ready
// through the shared dial semaphore.
func BenchmarkPoolColdStart(b *testing.B) {
	addr, hostKey := startBenchServer(b)
	const hosts = 16
	b.ReportAllocs()
	for b.Loop() {
		p := sshx.NewPool(0)
		ms := make([]*sshx.Managed, hosts)
		for i := range ms {
			ms[i] = p.Add(sshx.ManagedConfig{Dial: func(ctx context.Context) (*sshx.Client, error) {
				return sshx.Dial(ctx, addr, benchConfig(hostKey))
			}})
		}
		for _, m := range ms {
			for m.State() != sshx.StateReady {
				time.Sleep(50 * time.Microsecond)
			}
		}
		p.Close()
	}
}

func BenchmarkClientCombinedOutput(b *testing.B) {
	addr, hostKey := startBenchServer(b)
	c, err := sshx.Dial(context.Background(), addr, benchConfig(hostKey))
	if err != nil {
		b.Fatalf("Dial() error = %v", err)
	}
	b.Cleanup(func() { _ = c.Close() })

	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.CombinedOutput(context.Background(), "noop"); err != nil {
			b.Fatalf("CombinedOutput() error = %v", err)
		}
	}
}
