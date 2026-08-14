package sshx

import "time"

// Defaults applied wherever the caller leaves a zero value: Dial and Ping
// when ctx carries no deadline, TTYConfig fields left empty, and NewPool
// given a non-positive cap.
const (
	DefaultDialTimeout = 10 * time.Second
	DefaultPingTimeout = 5 * time.Second
	DefaultTerm        = "xterm-256color"
	DefaultCols        = 80
	DefaultRows        = 24
	DefaultMaxDials    = 16
)

// Internal tuning with no user-facing lever: keepalive cadence, the remote
// PTY's nominal baud, reconnect backoff (exponential with full jitter), and
// health-probe cadence.
const (
	keepaliveInterval = 15 * time.Second
	defaultTermSpeed  = 14400
	backoffBase       = 500 * time.Millisecond
	backoffCap        = 30 * time.Second
	probeEvery        = 15 * time.Second
)
