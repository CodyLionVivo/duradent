// Command duradent is the single backend binary. The first argument selects
// the process mode, so the same image runs as api, worker or outbox-relay on
// ECS, and as a one-off migration task before each rollout.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/CodyLionVivo/duradent/services/api/internal/platform/config"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/logging"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	"github.com/CodyLionVivo/duradent/services/api/migrations"
)

const usage = `usage: duradent <command>

commands:
  api            serve the HTTP API
  worker         consume domain events from SQS
  outbox-relay   publish pending outbox rows to SNS
  migrate        apply database migrations and exit`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	mode := os.Args[1]

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	log := logging.New(os.Stdout, cfg.LogLevel, "duradent-"+mode).With("env", cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, mode, cfg, log)
	stop()
	if err != nil {
		log.Error("exit", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, mode string, cfg config.Config, log *slog.Logger) error {
	switch mode {
	case "api", "worker", "outbox-relay", "migrate":
	default:
		return fmt.Errorf("unknown command %q\n%s", mode, usage)
	}

	db, err := mysql.Open(ctx, cfg.MySQLDSN)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if mode == "migrate" {
		if err := mysql.Migrate(db, migrations.FS, "."); err != nil {
			return err
		}
		log.Info("migrations applied")
		return nil
	}

	deps := map[string]httpserver.Pinger{"mysql": db}
	mux := httpserver.NewMux(deps)

	switch mode {
	case "worker", "outbox-relay":
		// Consumers and the relay arrive in phase 5. Until then these modes
		// only expose health and metrics so the deployment shape is final.
		log.Info("no handlers registered yet", "mode", mode)
	}

	return httpserver.Run(ctx, log, cfg.HTTPAddr, mux, cfg.ShutdownTimeout)
}
