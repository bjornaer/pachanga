package platform

import (
	"context"
	"errors"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"strings"
)

// Migrate applies app migrations from dir, then River's queue schema.
func Migrate(ctx context.Context, dbURL, dir string) error {
	m, err := migrate.New("file://"+dir, "pgx5://"+strings.TrimPrefix(strings.TrimPrefix(dbURL, "postgres://"), "postgresql://"))
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	rm, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return err
	}
	_, err = rm.Migrate(ctx, rivermigrate.DirectionUp, nil)
	return err
}
