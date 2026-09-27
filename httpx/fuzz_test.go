package httpx_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// FuzzProblemRespond feeds arbitrary members and one extension through
// Problem.Respond. Invariants: always valid JSON, the standard members
// encode first and exactly as without extensions, a standard-member name
// never overrides, and any other extension key lands at the top level.
func FuzzProblemRespond(f *testing.F) {
	f.Add("about:blank", "Not Found", 404, "", "", "request_id", "abc")
	f.Add("", "", 0, "", "", "status", "200")
	f.Add("https://example.com/p", "T", 422, "d", "/i", "TYPE", "x")
	f.Add("<&>", "\xff", 599, "\"", "\\", "\xfe", " ")
	f.Add("", "", 500, "", "", "ſtatus", "long s folds to s")

	standard := []string{"type", "title", "status", "detail", "instance"}

	// roundTrip is what a JSON decoder sees for s (invalid UTF-8 is coerced).
	roundTrip := func(t *testing.T, s string) string {
		t.Helper()
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal string: %v", err)
		}
		var out string
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("unmarshal string: %v", err)
		}
		return out
	}

	respond := func(p httpx.Problem) []byte {
		rec := httptest.NewRecorder()
		p.Respond(rec)
		return rec.Body.Bytes()
	}

	f.Fuzz(func(t *testing.T, typ, title string, status int, detail, instance, key, value string) {
		if status != 0 && (status < 100 || status > 999) {
			return // not a status WriteHeader accepts
		}

		base := httpx.Problem{Type: typ, Title: title, Status: status, Detail: detail, Instance: instance}
		plain := respond(base)

		p := base
		p.Extensions = map[string]any{key: value}
		b := respond(p)
		if !json.Valid(b) {
			t.Fatalf("Respond() body = %q, not valid JSON", b)
		}
		if !bytes.HasPrefix(b, plain[:len(plain)-1]) {
			t.Fatalf("Respond() body = %q, does not open with the standard members %q", b, plain)
		}

		var got map[string]any
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		if err := d.Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}

		wantStatus := status
		if wantStatus == 0 {
			wantStatus = http.StatusInternalServerError
		}
		if got["status"] != json.Number(strconv.Itoa(wantStatus)) {
			t.Fatalf("status = %v, want %d", got["status"], wantStatus)
		}
		for name, v := range map[string]string{"type": typ, "title": title, "detail": detail, "instance": instance} {
			if v == "" {
				continue
			}
			if got[name] != roundTrip(t, v) {
				t.Fatalf("%s = %v, want %q", name, got[name], v)
			}
		}

		reserved := false
		for _, s := range standard {
			reserved = reserved || strings.EqualFold(s, key)
		}
		if reserved {
			if !bytes.Equal(b, plain) {
				t.Fatalf("standard-member extension %q changed the body: %q, want %q", key, b, plain)
			}
			return
		}
		if got[roundTrip(t, key)] != roundTrip(t, value) {
			t.Fatalf("extension %q = %v, want %q", key, got[roundTrip(t, key)], value)
		}
	})
}
