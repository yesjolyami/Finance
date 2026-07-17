package backupv5

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const MaximumPreviewCleanupBatch = 1000

type PreviewCleanupRepository interface {
	DeleteExpiredPreviewMetadata(context.Context, time.Time, int) (int64, error)
}

type PostgresPreviewCleanupRepository struct {
	database *sql.DB
}

var _ PreviewCleanupRepository = (*PostgresPreviewCleanupRepository)(nil)

func NewPostgresPreviewCleanupRepository(database *sql.DB) *PostgresPreviewCleanupRepository {
	return &PostgresPreviewCleanupRepository{database: database}
}

// DeleteExpiredPreviewMetadata removes one bounded batch. The strict cutoff
// keeps the boundary row and never touches completed runs, audit, or finance.
func (repository *PostgresPreviewCleanupRepository) DeleteExpiredPreviewMetadata(
	ctx context.Context,
	cutoff time.Time,
	limit int,
) (int64, error) {
	if repository == nil || repository.database == nil || cutoff.IsZero() ||
		limit < 1 || limit > MaximumPreviewCleanupBatch {
		return 0, ErrRepository
	}
	var deleted int64
	err := repository.database.QueryRowContext(ctx, `
		WITH candidates AS (
			SELECT id
			FROM backup_v5_import_previews
			WHERE expires_at < $1
			ORDER BY expires_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		),
		deleted AS (
			DELETE FROM backup_v5_import_previews AS preview
			USING candidates
			WHERE preview.id = candidates.id
			RETURNING 1
		)
		SELECT count(*) FROM deleted
	`, cutoff, limit).Scan(&deleted)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return 0, err
		}
		return 0, ErrRepository
	}
	return deleted, nil
}
