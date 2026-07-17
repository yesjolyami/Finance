package platform

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	ExpectedMigrationVersion int64 = 4
	maxOpenConnections             = 10
	maxIdleConnections             = 2
	connectionMaxLifetime          = 30 * time.Minute
	connectionMaxIdleTime          = 5 * time.Minute
)

var (
	ErrDatabaseConfiguration = errors.New("database configuration invalid")
	ErrDatabaseUnavailable   = errors.New("database unavailable")
	ErrDatabaseMigration     = errors.New("database migration state invalid")
)

type Database struct {
	db *sql.DB
}

func OpenPostgres(databaseURL string) (*Database, error) {
	connectionConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, ErrDatabaseConfiguration
	}
	db := stdlib.OpenDB(*connectionConfig)

	db.SetMaxOpenConns(maxOpenConnections)
	db.SetMaxIdleConns(maxIdleConnections)
	db.SetConnMaxLifetime(connectionMaxLifetime)
	db.SetConnMaxIdleTime(connectionMaxIdleTime)

	return &Database{db: db}, nil
}

func (database *Database) CheckReady(ctx context.Context, expectedMigration int64) error {
	if database == nil || database.db == nil || expectedMigration < 1 {
		return ErrDatabaseConfiguration
	}
	if err := database.db.PingContext(ctx); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return ErrDatabaseUnavailable
	}
	var maximumVersion, appliedVersions int64
	if err := database.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(MAX(version_id) FILTER (WHERE is_applied), 0),
			COUNT(DISTINCT version_id) FILTER (
				WHERE is_applied AND version_id BETWEEN 1 AND $1
			)
		FROM goose_db_version
	`, expectedMigration).Scan(&maximumVersion, &appliedVersions); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return ErrDatabaseMigration
	}
	if maximumVersion != expectedMigration || appliedVersions != expectedMigration {
		return ErrDatabaseMigration
	}
	return nil
}

func (database *Database) PingContext(ctx context.Context) error {
	return database.db.PingContext(ctx)
}

func (database *Database) SQL() *sql.DB {
	return database.db
}

func (database *Database) Close() error {
	return database.db.Close()
}
