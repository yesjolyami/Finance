package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"finance/backend/internal/auth"
	"finance/backend/internal/backupv5"
	"finance/backend/internal/config"
	"finance/backend/internal/finance"
	"finance/backend/internal/households"
	"finance/backend/internal/httpserver"
	"finance/backend/internal/platform"
)

const shutdownTimeout = 75 * time.Second
const productionDatabaseStartupTimeout = 10 * time.Second

var errProductionDatabaseStartup = errors.New("production database startup validation failed")

var _ httpserver.FinanceService = (*finance.Service)(nil)
var _ httpserver.PreviewImportService = (*backupv5.Service)(nil)
var _ httpserver.ConfirmImportService = (*backupv5.ConfirmService)(nil)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	appConfig, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	verifier, err := auth.NewRemoteVerifier(auth.VerifierConfig{
		Issuer:          appConfig.AuthIssuer,
		Audience:        appConfig.AuthAudience,
		JWKSURL:         appConfig.AuthJWKSURL,
		CacheTTL:        appConfig.AuthJWKSCacheTTL,
		RefreshCooldown: appConfig.AuthJWKSRefreshCooldown,
		ClockSkew:       appConfig.AuthClockSkew,
		HTTPTimeout:     appConfig.AuthHTTPTimeout,
	})
	if err != nil {
		return errors.New("configure authentication: invalid verifier configuration")
	}

	database, err := platform.OpenPostgres(appConfig.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			logger.Error("close database", "error_class", "database_close_failed")
		}
	}()
	if err := validateProductionDatabase(context.Background(), appConfig, database); err != nil {
		return err
	}

	householdRepository := households.NewPostgresRepository(database.SQL())
	householdService := households.NewService(householdRepository)
	financeRepository := finance.NewPostgresRepository(database.SQL())
	financeService := finance.NewService(financeRepository)
	importRuntime, err := configureBackupImport(context.Background(), appConfig, database.SQL(), logger)
	if err != nil {
		return fmt.Errorf("configure backup import: %w", err)
	}
	serverOptions := make([]httpserver.Option, 0, 1)
	if importRuntime != nil {
		serverOptions = append(serverOptions, importRuntime.httpOption)
	}
	server := httpserver.New(appConfig, database, verifier, householdService, financeService, logger, serverOptions...)
	serverErrors := make(chan error, 1)

	shutdownSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cleanupContext, stopCleanup := context.WithCancel(shutdownSignal)
	cleanupDone := importRuntime.startCleanup(cleanupContext, logger)
	defer func() {
		stopCleanup()
		<-cleanupDone
	}()

	go func() {
		logger.Info("api listening", "address", server.Addr, "environment", appConfig.AppEnv)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-shutdownSignal.Done():
		logger.Info("graceful shutdown started")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownContext); err != nil {
		_ = server.Close()
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}

	if err := <-serverErrors; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("stop HTTP server: %w", err)
	}

	logger.Info("graceful shutdown completed")
	return nil
}

type productionDatabaseChecker interface {
	CheckReady(context.Context, int64) error
}

func validateProductionDatabase(
	ctx context.Context,
	appConfig config.Config,
	database productionDatabaseChecker,
) error {
	if appConfig.AppEnv != "production" {
		return nil
	}
	if database == nil {
		return errProductionDatabaseStartup
	}
	checkContext, cancel := boundedContext(ctx, productionDatabaseStartupTimeout)
	defer cancel()
	if err := database.CheckReady(checkContext, platform.ExpectedMigrationVersion); err != nil {
		return errProductionDatabaseStartup
	}
	return nil
}
