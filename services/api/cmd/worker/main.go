// Command worker runs River background jobs.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/pachanga/pachanga/services/api/internal/db"
	"github.com/pachanga/pachanga/services/api/internal/notifications"
	"github.com/pachanga/pachanga/services/api/internal/platform"
)

func main() {
	platform.InitLogging()
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := platform.LoadConfig()
	if err != nil {
		return err
	}
	pool, err := platform.OpenDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	mail := notifications.Failover{Q: db.New(pool)}
	for _, name := range strings.Split(cfg.EmailProvider, ",") {
		switch strings.TrimSpace(name) {
		case "console":
			mail.Providers = append(mail.Providers, notifications.Capped{Sender: notifications.Console{}})
		case "brevo":
			mail.Providers = append(mail.Providers, notifications.Capped{Sender: notifications.Brevo{Key: cfg.BrevoKey, From: cfg.MailFrom}, DailyCap: cfg.BrevoCap})
		case "mailjet":
			mail.Providers = append(mail.Providers, notifications.Capped{Sender: notifications.Mailjet{Key: cfg.MailjetKey, From: cfg.MailFrom}, DailyCap: cfg.MailjetCap})
		}
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &notifications.SendCodeWorker{Mail: mail})
	rc, err := river.NewClient[pgx.Tx](riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 10}},
		Workers: workers,
	})
	if err != nil {
		return err
	}
	if err := rc.Start(ctx); err != nil {
		return err
	}
	slog.Info("worker started")
	<-ctx.Done()
	return rc.Stop(context.Background())
}
