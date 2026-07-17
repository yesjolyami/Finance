package backupv5

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPreviewCleanupBoundariesAndIsolation(t *testing.T) {
	fixture := newPreviewDBFixture(t, "owner")
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-24 * time.Hour)
	insertCleanupPreview := func(expiresAt time.Time, terminal string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		createdAt := expiresAt.Add(-10 * time.Minute)
		tokenHash := sha256.Sum256([]byte(id.String() + "-token"))
		digest := sha256.Sum256([]byte(id.String() + "-digest"))
		var consumedAt, revokedAt any
		switch terminal {
		case "consumed":
			consumedAt = expiresAt.Add(-time.Minute)
		case "revoked":
			revokedAt = expiresAt.Add(time.Hour)
		}
		if _, err := fixture.database.Exec(`
			INSERT INTO backup_v5_import_previews (
				id, household_id, actor_user_id, token_hash, backup_digest,
				budget_month, policy_version, expires_at, consumed_at, revoked_at, created_at
			) VALUES ($1,$2,$3,$4,$5,'2026-07-01',$6,$7,$8,$9,$10)
		`, id, fixture.householdID, fixture.userID, tokenHash[:], digest[:],
			PolicyVersion, expiresAt, consumedAt, revokedAt, createdAt); err != nil {
			t.Fatal(err)
		}
		return id
	}

	oldActive := insertCleanupPreview(cutoff.Add(-time.Hour), "")
	oldRevoked := insertCleanupPreview(cutoff.Add(-time.Minute), "revoked")
	boundary := insertCleanupPreview(cutoff, "")
	recent := insertCleanupPreview(now.Add(-time.Hour), "consumed")
	accountID, auditID := uuid.New(), uuid.New()
	if _, err := fixture.database.Exec(`
		INSERT INTO accounts (id, household_id, name, color)
		VALUES ($1,$2,'Cleanup preserved account','#112233')
	`, accountID, fixture.householdID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Exec(`
		INSERT INTO audit_log (
			id, household_id, actor_user_id, entity_type, entity_id, action, request_id, changes
		) VALUES ($1,$2,$3,'accounts',$4,'created','cleanup-test','{}'::jsonb)
	`, auditID, fixture.householdID, fixture.userID, accountID); err != nil {
		t.Fatal(err)
	}

	repository := NewPostgresPreviewCleanupRepository(fixture.database)
	deleted, err := repository.DeleteExpiredPreviewMetadata(context.Background(), cutoff, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("first cleanup deleted=%d error=%v", deleted, err)
	}
	deleted, err = repository.DeleteExpiredPreviewMetadata(context.Background(), cutoff, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("second cleanup deleted=%d error=%v", deleted, err)
	}
	deleted, err = repository.DeleteExpiredPreviewMetadata(context.Background(), cutoff, 10)
	if err != nil || deleted != 0 {
		t.Fatalf("boundary cleanup deleted=%d error=%v", deleted, err)
	}
	for id, wantPresent := range map[uuid.UUID]bool{
		oldActive: false, oldRevoked: false, boundary: true, recent: true,
	} {
		var present bool
		if err := fixture.database.QueryRow(`
			SELECT EXISTS (SELECT 1 FROM backup_v5_import_previews WHERE id = $1)
		`, id).Scan(&present); err != nil {
			t.Fatal(err)
		}
		if present != wantPresent {
			t.Fatalf("preview %s present=%v want=%v", id, present, wantPresent)
		}
	}

	var financeCount, auditCount, runCount int
	if err := fixture.database.QueryRow(`SELECT count(*) FROM accounts WHERE household_id = $1`, fixture.householdID).Scan(&financeCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.QueryRow(`SELECT count(*) FROM audit_log WHERE household_id = $1`, fixture.householdID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.QueryRow(`SELECT count(*) FROM backup_v5_import_runs WHERE household_id = $1`, fixture.householdID).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if financeCount != 1 || auditCount != 1 || runCount != 0 {
		t.Fatalf("cleanup touched non-preview data: finance=%d audit=%d runs=%d", financeCount, auditCount, runCount)
	}
}

func TestPreviewCleanupValidationAndCancellation(t *testing.T) {
	fixture := newPreviewDBFixture(t, "owner")
	repository := NewPostgresPreviewCleanupRepository(fixture.database)
	for _, limit := range []int{0, MaximumPreviewCleanupBatch + 1} {
		if _, err := repository.DeleteExpiredPreviewMetadata(context.Background(), time.Now(), limit); !errors.Is(err, ErrRepository) {
			t.Fatalf("limit=%d error=%v", limit, err)
		}
	}
	if _, err := repository.DeleteExpiredPreviewMetadata(context.Background(), time.Time{}, 1); !errors.Is(err, ErrRepository) {
		t.Fatalf("zero cutoff error=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repository.DeleteExpiredPreviewMetadata(ctx, time.Now(), 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}
