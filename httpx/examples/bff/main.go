// A BFF page on httpx: html/template through the Renderer interface.
// templ components satisfy httpx.Renderer natively — swap the adapter
// for a templ component and nothing else changes.
//
//	go run ./bff
//	open http://localhost:8081
package main

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Wigata-Intech/w-tools/httpx"
)

var page = template.Must(template.New("dashboard").Parse(`<!doctype html>
<html><body>
<h1>Hello, {{.Name}}</h1>
<p>{{.Orders}} orders today.</p>
</body></html>`))

type dashboard struct {
	Name   string
	Orders int
}

func home(w http.ResponseWriter, r *http.Request) {
	data := dashboard{Name: "Dhira", Orders: 2}

	if err := httpx.Render(w, r, http.StatusOK, httpx.Template(page, "dashboard", data)); err != nil {
		// Headers are gone by now; the error is for the logs.
		log.Printf("render: %v", err)
	}
}

func main() {
	s := httpx.New(httpx.Config{Addr: ":8081"})
	s.Get("/", home)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("BFF example on :8081 — Ctrl-C to stop")

	if err := s.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
