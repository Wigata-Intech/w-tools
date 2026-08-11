package httpx

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"
)

// Config configures New. The zero value of every field is a production
// default (see the Default constants), not Go's dangerous zero —
// New(Config{Addr: ":8080"}) is a server with timeouts on.
type Config struct {
	Addr string

	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	ShutdownGrace     time.Duration
}

// group embeds Group under a lowercase alias: a field literally named
// Group would shadow the promoted Group method.
type group = Group

// Server wraps http.Server and the root route Group — Get, Post, Group
// and the rest are promoted from the embedded root. Register routes,
// then call Run.
type Server struct {
	group

	srv *http.Server

	// mu serializes Use against the handler build; a non-nil handler
	// means the server started serving.
	mu      sync.Mutex
	chain   []Middleware
	handler http.Handler

	grace time.Duration
}

// New returns a Server ready to register routes. Zero-valued config
// fields get the package defaults.
func New(cfg Config) *Server {
	if cfg.ReadHeaderTimeout == 0 {
		cfg.ReadHeaderTimeout = DefaultReadHeaderTimeout
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = DefaultReadTimeout
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = DefaultWriteTimeout
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	if cfg.MaxHeaderBytes == 0 {
		cfg.MaxHeaderBytes = DefaultMaxHeaderBytes
	}
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = DefaultShutdownGrace
	}

	mux := http.NewServeMux()

	return &Server{
		group: group{mux: mux},
		srv: &http.Server{
			Addr:              cfg.Addr,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			ReadTimeout:       cfg.ReadTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
			MaxHeaderBytes:    cfg.MaxHeaderBytes,
		},
		grace: cfg.ShutdownGrace,
	}
}

// Use appends middleware wrapped around the entire mux — every route, and
// unmatched (404) requests too. Must be called before Run or ServeHTTP.
func (s *Server) Use(mw ...Middleware) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.handler != nil {
		panic("httpx: Use called after the server started serving")
	}

	s.chain = append(s.chain, mw...)
}

// ServeHTTP makes Server a plain http.Handler, usable under httptest or
// mounted inside another server without Run.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.build().ServeHTTP(w, r)
}

// Run serves until ctx is canceled, then shuts down gracefully within the
// configured ShutdownGrace. Signal wiring belongs to the caller:
//
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//	defer stop()
//	err := srv.Run(ctx)
//
// It returns the shutdown error on a graceful stop (nil when the drain
// succeeded), or the serve error if the server could not run at all.
func (s *Server) Run(ctx context.Context) error {
	s.srv.Handler = s.build()

	errCh := make(chan error, 1)
	go func() { errCh <- s.srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	// The parent ctx is already dead; the drain needs its own deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.grace)
	defer cancel()

	return s.srv.Shutdown(shutdownCtx) //nolint:contextcheck // deliberate: deriving the drain deadline from the canceled parent would skip the drain
}

// HTTPServer exposes the underlying http.Server for needs httpx does not
// wrap, such as ListenAndServeTLS or connection-state hooks.
func (s *Server) HTTPServer() *http.Server {
	return s.srv
}

func (s *Server) build() http.Handler {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.handler == nil {
		var h http.Handler = s.mux
		for _, mw := range slices.Backward(s.chain) {
			h = mw(h)
		}
		s.handler = h
	}

	return s.handler
}
