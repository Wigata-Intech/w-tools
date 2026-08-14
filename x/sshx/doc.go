// Package sshx manages persistent SSH connections.
//
// The core of the package is the [Pool]/[Managed] pair: one self-healing
// connection per host, reconnecting with jittered exponential backoff, capped
// dial concurrency across the pool, and non-blocking not-ready semantics so a
// dead host never stalls a caller polling many. Around that core sit the
// pieces a persistent connection needs: [Dial] with context cancellation,
// one-shot execution shaped like os/exec ([Client.Output],
// [Client.CombinedOutput]), stream-based interactive sessions
// ([Client.Shell]), and fail-closed host-key verification ([KnownHosts],
// [TOFU], [InsecureAcceptAny]).
//
// The package is headless by design: it never touches the process's terminal,
// environment, or standard streams. Interactive decisions — confirming an
// unknown host key, supplying a passphrase — are callbacks the consumer wires
// to a terminal, a GUI, or an automated policy. Private-key parsing, loading,
// and generation live in the keys subpackage.
package sshx
