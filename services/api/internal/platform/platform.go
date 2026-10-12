// Package platform holds process-wide plumbing: config, DB pool, problem errors.
package platform

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Env            string // dev | staging | prod
	Addr           string
	DatabaseURL    string
	JWTSecret      []byte
	TermsVersion   string
	EmailProvider  string // console | brevo,mailjet (ordered failover list)
	BrevoKey       string
	MailjetKey     string // "public:private"
	MailFrom       string
	BrevoCap       int
	MailjetCap     int
	AppleClientID  string
	GoogleClientID string
	TrustedProxies int
}

func LoadConfig() (Config, error) {
	c := Config{
		Env:            env("APP_ENV", "dev"),
		Addr:           env("ADDR", ":8080"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		JWTSecret:      []byte(os.Getenv("JWT_SECRET")),
		TermsVersion:   env("TERMS_VERSION", "2026-10"),
		EmailProvider:  env("EMAIL_PROVIDERS", "console"),
		BrevoKey:       os.Getenv("BREVO_API_KEY"),
		MailjetKey:     os.Getenv("MAILJET_API_KEY"),
		MailFrom:       env("MAIL_FROM", "no-reply@pachanga.app"),
		BrevoCap:       envInt("BREVO_DAILY_CAP", 300),
		MailjetCap:     envInt("MAILJET_DAILY_CAP", 200),
		AppleClientID:  os.Getenv("APPLE_CLIENT_ID"),
		GoogleClientID: os.Getenv("GOOGLE_CLIENT_ID"),
		TrustedProxies: envInt("TRUSTED_PROXIES", 0),
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL required")
	}
	if len(c.JWTSecret) < 32 {
		return c, fmt.Errorf("JWT_SECRET must be at least 32 bytes")
	}
	return c, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func OpenDB(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	return pool, pool.Ping(ctx)
}

// Problem is an RFC 9457 error with a stable code; handlers return it as error.
type Problem struct {
	Status int
	Code   string
	Detail string
}

func (p *Problem) Error() string { return p.Code }

func Err(status int, code string) *Problem { return &Problem{Status: status, Code: code} }
