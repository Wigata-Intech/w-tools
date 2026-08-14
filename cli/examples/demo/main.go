// Command demo shows the cli package end to end: a command tree with
// inherited flags, the flag > env > *_FILE > config > default precedence,
// a secret flag, generated help, and version stamping.
//
// Explore it:
//
//	go run ./demo --help
//	go run ./demo serve --help
//	go run ./demo serve
//	go run ./demo serve --http-addr :9090
//	DEMO_HTTP_ADDR=:7070 go run ./demo serve
//	DEMO_GREETING=hallo go run ./demo greet World
//	echo '{"greeting": "hei"}' > /tmp/demo.json && go run ./demo --config /tmp/demo.json greet World
//	go run ./demo version
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Wigata-Intech/w-tools/cli"
)

// version is stamped at build time: -ldflags "-X main.version=v1.2.3".
var version = "0.0.0-dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(root().Execute(ctx))
}

func root() *cli.Command {
	var (
		logLevel string
		apiKey   string
		greeting string
		httpAddr string
	)

	return &cli.Command{
		Name:    "demo",
		Short:   "demonstrates the w-tools cli package",
		Version: version,
		Config:  cli.ConfigFile{Flag: "config"},
		Flags: func(fs *cli.FlagSet) {
			fs.StringVar(&logLevel, "log-level", "info", "minimum log level")
			fs.StringVar(&apiKey, "api-key", "", "upstream API key")
			fs.Secret("api-key")
		},
		Commands: []*cli.Command{
			{
				Name:  "greet",
				Short: "greet the operands",
				Flags: func(fs *cli.FlagSet) {
					fs.StringVar(&greeting, "greeting", "hello", "word to greet with")
				},
				Run: func(_ context.Context, args []string) error {
					for _, name := range args {
						fmt.Printf("%s, %s\n", greeting, name)
					}
					fmt.Printf("(log-level=%s, api key set: %t)\n", logLevel, apiKey != "")
					return nil
				},
			},
			{
				Name:  "serve",
				Short: "pretend to start a server",
				Flags: func(fs *cli.FlagSet) {
					fs.StringVar(&httpAddr, "http-addr", ":8080", "listen address")
				},
				Run: func(_ context.Context, _ []string) error {
					fmt.Printf("would listen on %s (log-level=%s)\n", httpAddr, logLevel)
					return nil
				},
			},
		},
	}
}
