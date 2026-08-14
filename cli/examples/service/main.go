// Command service is the register-based layout for a real service: the
// root is assembled once here, and every subcommand is constructed in its
// own package — rest, cron, consumer, migrate — then registered with a
// plain slice append. No registration API is needed: a *cli.Command is a
// value, and any package can build and return one.
//
// Try:
//
//	go run ./service --help
//	go run ./service rest
//	SERVICE_HTTP_ADDR=:7070 go run ./service rest
//	go run ./service cron --every 500ms
//	go run ./service consumer
//	go run ./service migrate status
//	go run ./service --config config.json rest
//	env $(grep -v '^#' .env.example | xargs) go run ./service rest
//	go run ./service version
//
// config.json shows the config-file layer (keys are bare flag names);
// .env.example shows the environment layer (keys carry the SERVICE_
// prefix because they are real environment variables).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Wigata-Intech/w-tools/cli"
	"github.com/Wigata-Intech/w-tools/cli/examples/service/consumer"
	"github.com/Wigata-Intech/w-tools/cli/examples/service/cron"
	"github.com/Wigata-Intech/w-tools/cli/examples/service/migrate"
	"github.com/Wigata-Intech/w-tools/cli/examples/service/rest"
)

// version is stamped at build time: -ldflags "-X main.version=v1.2.3".
var version = "0.0.0-dev"

// config is the root schema: Bind declares the flags from it, and every
// layer — argv, env, *_FILE, config file — resolves into it before any
// Run executes.
type config struct {
	LogLevel string `cli:"log-level" default:"info" usage:"minimum log level"`
	APIKey   string `cli:"api-key,secret" usage:"upstream API key"`
}

func main() {
	if err := cli.LoadDotEnv(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(root().Execute(ctx))
}

func root() *cli.Command {
	var cfg config

	r := &cli.Command{
		Name:    "service",
		Short:   "register-based service skeleton on w-tools/cli",
		Version: version,
		Config:  cli.ConfigFile{Flag: "config"},
		Flags:   func(fs *cli.FlagSet) { fs.Bind(&cfg) },
	}

	r.Commands = append(r.Commands,
		rest.Command(&cfg.LogLevel),
		cron.Command(&cfg.LogLevel),
		consumer.Command(&cfg.LogLevel),
		migrate.Command(),
	)
	return r
}
