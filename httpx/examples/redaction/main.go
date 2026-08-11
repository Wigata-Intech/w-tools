// The family story in one runnable proof: w-tools/logger's redaction
// rules applied to request bodies captured by httpx's Logger middleware.
// The program starts a server, POSTs itself a login containing a
// password, and exits — the access line it prints carries the whole
// body as structured JSON with the password already [REDACTED]. Nobody
// wrote a careful log call; the pipeline did it.
//
//	go run ./redaction
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/Wigata-Intech/w-tools/httpx"
	"github.com/Wigata-Intech/w-tools/httpx/middleware"
	"github.com/Wigata-Intech/w-tools/logger"
)

func main() {
	// One logger for the whole service; the redaction rules live here
	// and nowhere else.
	log := logger.New(logger.Config{
		Env:      "example",
		App:      "redaction-demo",
		Protocol: logger.ProtocolHTTP,
		Redact: logger.RedactConfig{
			Redacted: []string{"password"},
			Masked: map[string]logger.Mask{
				"card_number": {ShowFirst: 6, ShowLast: 4},
			},
		},
	})

	s := httpx.New(httpx.Config{})
	s.Use(middleware.Logger(middleware.LoggerConfig{
		Log:            log.Slog(), // the rules travel inside the handler
		LogRequestBody: true,
	}))
	s.Post("/login", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}

	srv := s.HTTPServer()
	srv.Handler = s
	go func() { _ = srv.Serve(ln) }()

	body := `{"user":"dhira","password":"hunter2","card_number":"4111111111111111"}`
	resp, err := http.Post("http://"+ln.Addr().String()+"/login", "application/json", strings.NewReader(body)) //nolint:noctx // one-shot self-call in a demo
	if err != nil {
		panic(err)
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	_ = srv.Shutdown(context.Background())

	fmt.Println("^ the access line above: password [REDACTED], card masked — enforced by config, not by careful call sites")
}
