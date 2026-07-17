package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"finance/backend/internal/config"
	"finance/backend/internal/finance"
	"finance/backend/internal/httpserver"
	"finance/backend/internal/platform"
)

type productionDatabaseStub struct {
	err      error
	calls    int
	version  int64
	deadline bool
}

func (stub *productionDatabaseStub) CheckReady(ctx context.Context, version int64) error {
	stub.calls++
	stub.version = version
	_, stub.deadline = ctx.Deadline()
	return stub.err
}

func TestRunFailsClosedWhenPostgresRejectsDSN(t *testing.T) {
	const secret = "password-that-must-not-appear"
	t.Setenv("APP_ENV", "test")
	t.Setenv("DATABASE_URL", "postgres://finance:"+secret+"@127.0.0.1:5432/finance?sslmode=not-a-mode")
	t.Setenv("AUTH_ISSUER", "https://project.test/auth/v1")
	t.Setenv("AUTH_AUDIENCE", "authenticated")
	t.Setenv("AUTH_JWKS_URL", "https://project.test/auth/v1/.well-known/jwks.json")

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	err := run(logger)
	if err == nil || !strings.Contains(err.Error(), "open database:") {
		t.Fatalf("run() did not fail closed on driver-invalid DSN: %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("startup error disclosed database credentials: %v", err)
	}
}

func TestShutdownTimeoutCoversImportAndRemainsBounded(t *testing.T) {
	if shutdownTimeout < 60*time.Second || shutdownTimeout > 120*time.Second {
		t.Fatalf("shutdown timeout does not safely bound import operations: %s", shutdownTimeout)
	}
}

func TestProductionFinanceServiceSatisfiesHTTPWiring(t *testing.T) {
	database := &sql.DB{}
	var service httpserver.FinanceService = finance.NewService(finance.NewPostgresRepository(database))
	if service == nil {
		t.Fatal("production finance service was not wired")
	}
}

func TestProductionDatabaseStartupIsFailClosedAndBounded(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "unavailable", err: errors.New("dsn password host details")},
		{name: "migration incomplete", err: errors.New("goose version 3")},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &productionDatabaseStub{err: test.err}
			err := validateProductionDatabase(context.Background(), config.Config{AppEnv: "production"}, stub)
			if !errors.Is(err, errProductionDatabaseStartup) || stub.calls != 1 ||
				stub.version != platform.ExpectedMigrationVersion || !stub.deadline {
				t.Fatalf("startup validation mismatch: error=%v stub=%#v", err, stub)
			}
			for _, secret := range []string{"password", "host", "goose", "3"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("startup error disclosed %q: %v", secret, err)
				}
			}
		})
	}
}

func TestNonProductionDatabaseStartupKeepsLocalDegradedMode(t *testing.T) {
	stub := &productionDatabaseStub{err: errors.New("unavailable")}
	if err := validateProductionDatabase(context.Background(), config.Config{AppEnv: "development"}, stub); err != nil {
		t.Fatalf("local startup unexpectedly required database: %v", err)
	}
	if stub.calls != 0 {
		t.Fatal("local startup performed production database check")
	}
}
