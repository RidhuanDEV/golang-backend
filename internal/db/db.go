package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/RidhuanDEV/golang-backend/internal/db/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	config.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func Migrate(url string) error {
	return migrate(url, 0)
}

func MigrateTo(url string, version int64) error { return migrate(url, version) }

func migrate(url string, version int64) error {
	if !strings.Contains(url, "timezone=") {
		separator := "?"
		if strings.Contains(url, "?") {
			separator = "&"
		}
		url += separator + "timezone=UTC"
	}
	database, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer database.Close()
	if err = goose.SetDialect("postgres"); err != nil {
		return err
	}
	goose.SetBaseFS(migrations.FS)
	if version > 0 {
		err = goose.UpTo(database, ".", version)
	} else {
		err = goose.Up(database, ".")
	}
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func IsUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505")
}
func IsForeignKeyViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23503")
}
func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
