// Package rest constructs the REST entrypoint; main registers it.
package rest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Wigata-Intech/w-tools/cli"
)

// Command builds the rest subcommand. Root flag state is shared by
// pointer; a real service constructs its logger and dependencies here.
// Plain net/http keeps this example module free of sibling requires —
// Wigata services use w-tools/httpx in this spot.
func Command(logLevel *string) *cli.Command {
	var addr string
	return &cli.Command{
		Name:  "rest",
		Short: "serve the REST API",
		Flags: func(fs *cli.FlagSet) {
			fs.StringVar(&addr, "http-addr", ":8080", "listen address")
		},
		Run: func(ctx context.Context, _ []string) error {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintln(w, "ok")
			})

			srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(shutdownCtx)
			}()

			fmt.Printf("rest: listening on %s (log-level=%s) — Ctrl-C to stop\n", addr, *logLevel)
			if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
}
