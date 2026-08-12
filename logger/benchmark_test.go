package logger_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/Wigata-Intech/w-tools/logger"
)

type benchCard struct {
	Number string `json:"card_number"`
	CVV    string `json:"cvv"`
	Holder string `json:"holder"`
}

type benchUser struct {
	Name     string
	Password string
	Card     benchCard
}

func benchRedact() logger.RedactConfig {
	return logger.RedactConfig{
		Redacted: []string{"password", "cvv"},
		Masked:   map[string]logger.Mask{"card_number": {ShowFirst: 6, ShowLast: 4}},
	}
}

// BenchmarkRawSlog is the baseline every other number is judged against.
func BenchmarkRawSlog(b *testing.B) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	b.ReportAllocs()
	for range b.N {
		log.Info("payment created", "order_id", "ord_123", "amount_jpy", 4980, "region", "ap-northeast-1", "attempt", 1, "ok", true)
	}
}

// BenchmarkPassThrough is the no-rules path — the design requires it within
// a few percent of BenchmarkRawSlog.
func BenchmarkPassThrough(b *testing.B) {
	ctx := context.Background()
	log := logger.New(logger.Config{Writer: io.Discard})
	b.ReportAllocs()
	for range b.N {
		log.Info(ctx, "payment created", "order_id", "ord_123", "amount_jpy", 4980, "region", "ap-northeast-1", "attempt", 1, "ok", true)
	}
}

// BenchmarkRulesNoMatch pays for the walk but rewrites nothing — the
// copy-on-write path.
func BenchmarkRulesNoMatch(b *testing.B) {
	ctx := context.Background()
	log := logger.New(logger.Config{Writer: io.Discard, Redact: benchRedact()})
	b.ReportAllocs()
	for range b.N {
		log.Info(ctx, "payment created", "order_id", "ord_123", "amount_jpy", 4980, "region", "ap-northeast-1", "attempt", 1, "ok", true)
	}
}

// BenchmarkRedactTopLevel matches rules on plain key-value args.
func BenchmarkRedactTopLevel(b *testing.B) {
	ctx := context.Background()
	log := logger.New(logger.Config{Writer: io.Discard, Redact: benchRedact()})
	b.ReportAllocs()
	for range b.N {
		log.Info(ctx, "payment created", "order_id", "ord_123", "card_number", "4111111111111111", "password", "hunter2", "ok", true)
	}
}

// BenchmarkRedactStruct reflects into a struct nested two levels deep — the
// most expensive path the package has.
func BenchmarkRedactStruct(b *testing.B) {
	ctx := context.Background()
	log := logger.New(logger.Config{Writer: io.Discard, Redact: benchRedact()})
	user := benchUser{Name: "d", Password: "hunter2", Card: benchCard{Number: "4111111111111111", CVV: "123", Holder: "D W"}}
	b.ReportAllocs()
	for range b.N {
		log.Info(ctx, "payment created", "order_id", "ord_123", "user", user)
	}
}

// BenchmarkPassThroughParallel is the pass-through path under concurrent
// loggers sharing one handler — run with -cpu 1,4,8 for the curve.
func BenchmarkPassThroughParallel(b *testing.B) {
	ctx := context.Background()
	log := logger.New(logger.Config{Writer: io.Discard})
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			log.Info(ctx, "payment created", "order_id", "ord_123", "amount_jpy", 4980, "region", "ap-northeast-1", "attempt", 1, "ok", true)
		}
	})
}

// BenchmarkRedactStructParallel is the reflection-heavy path under
// concurrent loggers — the plan cache and handler shared across all.
func BenchmarkRedactStructParallel(b *testing.B) {
	ctx := context.Background()
	log := logger.New(logger.Config{Writer: io.Discard, Redact: benchRedact()})
	user := benchUser{Name: "d", Password: "hunter2", Card: benchCard{Number: "4111111111111111", CVV: "123", Holder: "D W"}}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			log.Info(ctx, "payment created", "order_id", "ord_123", "user", user)
		}
	})
}
