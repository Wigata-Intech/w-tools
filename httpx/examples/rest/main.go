// A JSON REST service on httpx: groups, Bind, QUERY, and an ErrorMap
// translating the domain's errors in one place.
//
//	go run ./rest
//	curl localhost:8080/api/v1/orders/ord_1
//	curl localhost:8080/api/v1/orders/nope
//	curl -X QUERY -H 'Content-Type: application/json' -d '{"min_total":40}' localhost:8080/api/v1/orders/search
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Wigata-Intech/w-tools/httpx"
)

type order struct {
	ID    string `json:"id"`
	Total int    `json:"total"`
}

var errOrderNotFound = errors.New("order not found")

var orders = map[string]order{
	"ord_1": {ID: "ord_1", Total: 42},
	"ord_2": {ID: "ord_2", Total: 7},
}

// errMap is the service's whole error taxonomy, registered once.
var errMap = func() *httpx.ErrorMap {
	m := httpx.NewErrorMap()
	m.Map(errOrderNotFound, httpx.Problem{
		Type:   "https://example.com/problems/order-not-found",
		Status: http.StatusNotFound,
	})

	return m
}()

func getOrder(w http.ResponseWriter, r *http.Request) {
	o, ok := orders[r.PathValue("id")]
	if !ok {
		errMap.Respond(w, errOrderNotFound)
		return
	}

	httpx.JSON(w, http.StatusOK, o)
}

// searchOrders serves HTTP QUERY (RFC 10008): a safe, idempotent query
// with its filter in the body — no more giant query strings.
func searchOrders(w http.ResponseWriter, r *http.Request) {
	var filter struct {
		MinTotal int `json:"min_total"`
	}
	if err := httpx.Bind(r, &filter); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid filter")
		return
	}

	matches := []order{}
	for _, o := range orders {
		if o.Total >= filter.MinTotal {
			matches = append(matches, o)
		}
	}

	httpx.JSON(w, http.StatusOK, matches)
}

func main() {
	s := httpx.New(httpx.Config{Addr: ":8080"})

	api := s.Group("/api/v1")
	api.Get("/orders/{id}", getOrder)
	api.Query("/orders/search", searchOrders)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("REST example on :8080 — Ctrl-C to stop")

	if err := s.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
