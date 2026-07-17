package platform

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestUnavailableDatabaseReturnsPingError(t *testing.T) {
	database, err := OpenPostgres("postgres://finance:finance_dev_only@127.0.0.1:1/finance_dev?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("OpenPostgres() rejected a valid DSN: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if err := database.PingContext(context.Background()); err == nil {
		t.Fatal("PingContext() unexpectedly succeeded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := database.CheckReady(ctx, ExpectedMigrationVersion); !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("CheckReady() unavailable error=%v", err)
	}
}

func TestOpenPostgresRejectsMalformedDriverDSN(t *testing.T) {
	const secret = "password-that-must-not-appear"
	database, err := OpenPostgres("postgres://finance:" + secret + "@127.0.0.1:5432/finance?sslmode=not-a-mode")
	if !errors.Is(err, ErrDatabaseConfiguration) {
		_ = database.Close()
		t.Fatalf("OpenPostgres() error=%v", err)
	}
	if database != nil {
		t.Fatal("OpenPostgres() returned a database after a parse error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("database error disclosed credentials: %v", err)
	}
}

func TestOpenPostgresConfiguresBoundedPool(t *testing.T) {
	database, err := OpenPostgres("postgres://finance:secret@127.0.0.1:5432/finance?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if stats := database.db.Stats(); stats.MaxOpenConnections != maxOpenConnections {
		t.Fatalf("max open connections=%d", stats.MaxOpenConnections)
	}
	if connectionMaxLifetime <= 0 || connectionMaxIdleTime <= 0 ||
		connectionMaxLifetime > time.Hour || connectionMaxIdleTime > connectionMaxLifetime {
		t.Fatalf("unsafe connection lifetimes: lifetime=%s idle=%s", connectionMaxLifetime, connectionMaxIdleTime)
	}
}

func TestCheckReadyRequiresExactMigrationState(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	database, err := OpenPostgres(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.CheckReady(ctx, ExpectedMigrationVersion); err != nil {
		t.Fatalf("ready migrated database rejected: %v", err)
	}
	if err := database.CheckReady(ctx, ExpectedMigrationVersion+1); !errors.Is(err, ErrDatabaseMigration) {
		t.Fatalf("incomplete migration state error=%v", err)
	}
}
