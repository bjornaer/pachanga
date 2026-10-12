// Command api serves the HTTP API. `api migrate` applies migrations and exits.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/pachanga/pachanga/services/api/internal/auth"
	"github.com/pachanga/pachanga/services/api/internal/db"
	"github.com/pachanga/pachanga/services/api/internal/httpapi"
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
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		return platform.Migrate(ctx, cfg.DatabaseURL, envOr("MIGRATIONS_DIR", "db/migrations"))
	}
	pool, err := platform.OpenDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	// Insert-only River client: jobs run in cmd/worker.
	rc, err := river.NewClient[pgx.Tx](riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return err
	}
	verifiers := map[string]*oidc.IDTokenVerifier{}
	for name, p := range map[string]struct{ issuer, clientID string }{
		"apple":  {"https://appleid.apple.com", cfg.AppleClientID},
		"google": {"https://accounts.google.com", cfg.GoogleClientID},
	} {
		if p.clientID == "" {
			continue
		}
		prov, err := oidc.NewProvider(ctx, p.issuer)
		if err != nil {
			return err
		}
		verifiers[name] = prov.Verifier(&oidc.Config{ClientID: p.clientID})
	}
	q := db.New(pool)
	srv := &httpapi.Server{Q: q, Pool: pool, TrustedProxies: cfg.TrustedProxies, Auth: &auth.Service{
		Pool: pool, Q: q, River: rc, Signer: auth.Signer{Secret: cfg.JWTSecret}, Terms: cfg.TermsVersion,
		Devices: auth.NoDeviceCheck{}, OIDC: verifiers, Now: time.Now,
	}}
	h, err := httpapi.NewRouter(srv)
	if err != nil {
		return err
	}
	hs := &http.Server{Addr: cfg.Addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	slog.Info("api listening", "addr", cfg.Addr)
	if err := hs.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
