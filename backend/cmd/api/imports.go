package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"finance/backend/internal/backupv5"
	"finance/backend/internal/config"
	"finance/backend/internal/httpserver"
)

const (
	importStartupTimeout   = 10 * time.Second
	importCleanupTimeout   = 5 * time.Second
	importCleanupRetention = 24 * time.Hour
	importCleanupInterval  = time.Hour
	importCleanupBatch     = 500
)

var errImportStartup = errors.New("backup v5 import startup validation failed")

type backupImportRuntime struct {
	httpOption httpserver.Option
	cleanup    backupv5.PreviewCleanupRepository
}

func configureBackupImport(
	ctx context.Context,
	appConfig config.Config,
	database *sql.DB,
	logger *slog.Logger,
) (*backupImportRuntime, error) {
	if !appConfig.ImportBackupV5Enabled {
		return nil, nil
	}
	if appConfig.AppEnv == "production" && !appConfig.ProductionImportReady() {
		return nil, errImportStartup
	}
	keyring, err := backupv5.LoadHMACKeyringFile(
		appConfig.ImportHMACActiveKeyID,
		appConfig.ImportHMACKeyringFile,
	)
	if err != nil {
		return nil, errImportStartup
	}
	previewRepository := backupv5.NewPostgresPreviewRepository(database)
	previewService, err := backupv5.NewPreviewService(previewRepository)
	if err != nil {
		return nil, errImportStartup
	}
	confirmRepository := backupv5.NewPostgresConfirmRepository(database)
	confirmService, err := backupv5.NewConfirmService(confirmRepository, keyring)
	if err != nil {
		return nil, errImportStartup
	}
	auditContext, auditCancel := boundedContext(ctx, importStartupTimeout)
	err = confirmService.AuditKeyring(auditContext)
	auditCancel()
	if err != nil {
		return nil, errImportStartup
	}

	cleanup := backupv5.NewPostgresPreviewCleanupRepository(database)
	if _, err := cleanupPreviewMetadata(
		ctx, cleanup, logger, time.Now().UTC(), importCleanupTimeout,
		importCleanupRetention, importCleanupBatch, true,
	); err != nil {
		return nil, errImportStartup
	}
	return &backupImportRuntime{
		httpOption: httpserver.WithBackupV5Imports(previewService, confirmService, nil),
		cleanup:    cleanup,
	}, nil
}

func (runtime *backupImportRuntime) startCleanup(ctx context.Context, logger *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	if runtime == nil || runtime.cleanup == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		runPreviewCleanupLoop(
			ctx, runtime.cleanup, logger, importCleanupInterval, importCleanupTimeout,
			importCleanupRetention, importCleanupBatch,
		)
	}()
	return done
}

func runPreviewCleanupLoop(
	ctx context.Context,
	repository backupv5.PreviewCleanupRepository,
	logger *slog.Logger,
	interval, timeout, retention time.Duration,
	batch int,
) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_, _ = cleanupPreviewMetadata(
				ctx, repository, logger, now.UTC(), timeout, retention, batch, false,
			)
		}
	}
}

func cleanupPreviewMetadata(
	ctx context.Context,
	repository backupv5.PreviewCleanupRepository,
	logger *slog.Logger,
	now time.Time,
	timeout, retention time.Duration,
	batch int,
	startup bool,
) (int64, error) {
	if repository == nil || logger == nil || now.IsZero() || timeout <= 0 || retention <= 0 ||
		batch < 1 || batch > backupv5.MaximumPreviewCleanupBatch {
		return 0, errImportStartup
	}
	cleanupContext, cancel := boundedContext(ctx, timeout)
	defer cancel()
	deleted, err := repository.DeleteExpiredPreviewMetadata(cleanupContext, now.Add(-retention), batch)
	if err != nil {
		if ctx.Err() == nil {
			logger.WarnContext(ctx, "backup preview cleanup failed", "error_class", "cleanup_unavailable")
		}
		return 0, err
	}
	if deleted > 0 {
		phase := "periodic"
		if startup {
			phase = "startup"
		}
		logger.InfoContext(ctx, "backup preview cleanup completed", "phase", phase, "deleted_count", deleted)
	}
	return deleted, nil
}

func boundedContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, exists := parent.Deadline(); exists && time.Until(deadline) <= timeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}
