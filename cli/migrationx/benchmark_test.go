// Benchmarks price the engine's own work: scanning SQL into statements,
// loading and checksumming a migration set, and the per-migration
// bookkeeping around the consumer's database calls. Migrations run once
// per deploy — these numbers answer "what does the engine add", not
// "what does traffic cost".
package migrationx_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Wigata-Intech/w-tools/cli/migrationx"
)

// BenchmarkParseScript scans a 100-statement file with quotes and
// comments — the only per-byte work in the package.
func BenchmarkParseScript(b *testing.B) {
	var sb strings.Builder
	for i := range 100 {
		fmt.Fprintf(&sb, "-- statement %d\nINSERT INTO t (a, b) VALUES ('x; y', %d);\n", i, i)
	}
	src := sb.String()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := migrationx.ParseScript(src, false); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoad reads, parses, and checksums a 100-migration filesystem.
func BenchmarkLoad(b *testing.B) {
	fsys := fstest.MapFS{}
	for i := range 100 {
		up := fmt.Sprintf("%d_m%d.up.sql", 1000+i, i)
		down := fmt.Sprintf("%d_m%d.down.sql", 1000+i, i)
		fsys[up] = &fstest.MapFile{Data: []byte("CREATE TABLE t (x INTEGER);")}
		fsys[down] = &fstest.MapFile{Data: []byte("DROP TABLE t;")}
	}
	db, _ := fakeDB(b)
	cfg := migrationx.Config{Dialect: migrationx.DialectSQLite, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := migrationx.New(db, fsys, cfg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUpTen applies ten migrations through the fake driver: the
// engine's transaction, probe, and history bookkeeping per migration,
// with the database itself costing nothing.
func BenchmarkUpTen(b *testing.B) {
	fsys := fstest.MapFS{}
	for i := range 10 {
		name := fmt.Sprintf("%d_m%d.up.sql", 1000+i, i)
		fsys[name] = &fstest.MapFile{Data: []byte("CREATE TABLE t (x INTEGER);")}
	}
	cfg := migrationx.Config{Dialect: migrationx.DialectSQLite, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		db, _ := fakeDB(b)
		m, err := migrationx.New(db, fsys, cfg)
		if err != nil {
			b.Fatal(err)
		}
		if err := m.Up(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
