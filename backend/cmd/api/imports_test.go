package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"finance/backend/internal/backupv5"
	"finance/backend/internal/config"
	"finance/backend/internal/platform"

	"github.com/google/uuid"
)

type cleanupRepositoryStub struct {
	mu      sync.Mutex
	count   int64
	err     error
	calls   int
	cutoff  time.Time
	limit   int
	started chan struct{}
}

func (stub *cleanupRepositoryStub) DeleteExpiredPreviewMetadata(
	ctx context.Context,
	cutoff time.Time,
	limit int,
) (int64, error) {
	stub.mu.Lock()
	stub.calls++
	stub.cutoff = cutoff
	stub.limit = limit
	started := stub.started
	stub.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return 0, ctx.Err()
	}
	return stub.count, stub.err
}

func importTestLogger(output io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, nil))
}

func writeImportKeyring(t *testing.T, keys map[string][]byte) string {
	t.Helper()
	var body strings.Builder
	body.WriteByte('{')
	first := true
	for keyID, raw := range keys {
		if !first {
			body.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&body, "%q:%q", keyID, base64.RawURLEncoding.EncodeToString(raw))
	}
	body.WriteByte('}')
	path := filepath.Join(t.TempDir(), "import-keyring.json")
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func enabledImportConfig(path, activeKeyID string) config.Config {
	return config.Config{
		AppEnv: "test", ImportBackupV5Enabled: true,
		ImportHMACActiveKeyID: activeKeyID, ImportHMACKeyringFile: path,
	}
}

func TestConfigureBackupImportDisabledDoesNotReadKeyring(t *testing.T) {
	runtime, err := configureBackupImport(context.Background(), config.Config{
		ImportBackupV5Enabled: false,
		ImportHMACKeyringFile: "/definitely/missing/secret-keyring.json",
	}, nil, importTestLogger(io.Discard))
	if err != nil || runtime != nil {
		t.Fatalf("disabled import touched dependencies: runtime=%v error=%v", runtime, err)
	}
}

func TestConfigureBackupImportProductionPrerequisitesFailBeforeKeyringRead(t *testing.T) {
	appConfig := config.Config{
		AppEnv: "production", ImportBackupV5Enabled: true,
		ImportHMACActiveKeyID: "active",
		ImportHMACKeyringFile: "/path/that/must/not/be/read.json",
	}
	runtime, err := configureBackupImport(context.Background(), appConfig, nil, importTestLogger(io.Discard))
	if !errors.Is(err, errImportStartup) || runtime != nil {
		t.Fatalf("unsafe production import configuration accepted: runtime=%v error=%v", runtime, err)
	}
}

func TestConfigureBackupImportFailsClosedOnUnavailableDatabaseWithoutDisclosure(t *testing.T) {
	keyPath := writeImportKeyring(t, map[string][]byte{"active": bytes.Repeat([]byte{0x11}, 32)})
	database, err := platform.OpenPostgres("postgres://finance:finance_dev_only@127.0.0.1:1/finance_dev?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var logs bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runtime, err := configureBackupImport(
		ctx, enabledImportConfig(keyPath, "active"), database.SQL(), importTestLogger(&logs),
	)
	if !errors.Is(err, errImportStartup) || runtime != nil {
		t.Fatalf("unavailable database did not fail closed: runtime=%v error=%v", runtime, err)
	}
	for _, secret := range []string{keyPath, "active", "finance_dev_only"} {
		if strings.Contains(err.Error(), secret) || strings.Contains(logs.String(), secret) {
			t.Fatalf("startup error/log disclosed secret %q: error=%v logs=%s", secret, err, logs.String())
		}
	}
}

func TestConfigureBackupImportAuditsReferencedKeysAcrossRestart(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`TRUNCATE backup_v5_import_runs, backup_v5_import_previews`); err != nil {
		t.Fatal(err)
	}
	userID, householdID, membershipID := uuid.New(), uuid.New(), uuid.New()
	subject := uuid.New().String()
	if _, err := database.Exec(`
		INSERT INTO users (id, auth_subject, display_name)
		VALUES ($1,$2,'Import wiring actor')
	`, userID, subject); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO households (id, name, created_by_user_id)
		VALUES ($1,'Import wiring household',$2)
	`, householdID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO household_members (id, household_id, user_id, role, status)
		VALUES ($1,$2,$3,'owner','active')
	`, membershipID, householdID, userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.Exec(`TRUNCATE backup_v5_import_runs, backup_v5_import_previews`)
		_, _ = database.Exec(`DELETE FROM household_members WHERE id=$1`, membershipID)
		_, _ = database.Exec(`DELETE FROM households WHERE id=$1`, householdID)
		_, _ = database.Exec(`DELETE FROM users WHERE id=$1`, userID)
	})
	lookup := bytes.Repeat([]byte{0x22}, 32)
	fingerprint := bytes.Repeat([]byte{0x33}, 32)
	completed := time.Now().UTC()
	if _, err := database.Exec(`
		INSERT INTO backup_v5_import_runs (
			id, household_id, actor_user_id, hmac_key_id,
			idempotency_key_hmac, request_fingerprint_hmac, policy_version, status,
			accounts_count, categories_count, transactions_count, budgets_count,
			goals_count, goal_contributions_count, debts_count, debt_payments_count,
			legacy_owner_not_linked_count, archive_time_approximated_count,
			goal_exceeds_target_count, debt_overpaid_count,
			system_resource_preserved_count, budget_month_explicit_choice_count,
			completed_at, created_at
		) VALUES (
			$1,$2,$3,'old',$4,$5,$6,'completed',
			0,0,0,0,0,0,0,0,0,0,0,0,0,0,$7,$7
		)
	`, uuid.New(), householdID, userID, lookup, fingerprint, backupv5.PolicyVersion, completed); err != nil {
		t.Fatal(err)
	}

	activeOnly := writeImportKeyring(t, map[string][]byte{"active": bytes.Repeat([]byte{0x44}, 32)})
	runtime, err := configureBackupImport(ctx, enabledImportConfig(activeOnly, "active"), database, importTestLogger(io.Discard))
	if !errors.Is(err, errImportStartup) || runtime != nil {
		t.Fatalf("missing referenced key did not fail closed: runtime=%v error=%v", runtime, err)
	}
	rotated := writeImportKeyring(t, map[string][]byte{
		"active": bytes.Repeat([]byte{0x44}, 32),
		"old":    bytes.Repeat([]byte{0x55}, 32),
	})
	runtime, err = configureBackupImport(ctx, enabledImportConfig(rotated, "active"), database, importTestLogger(io.Discard))
	if err != nil || runtime == nil || runtime.httpOption == nil || runtime.cleanup == nil {
		t.Fatalf("valid rotated keyring was not wired: runtime=%v error=%v", runtime, err)
	}
	productionConfig := enabledImportConfig(rotated, "active")
	productionConfig.AppEnv = "production"
	productionConfig.SecurityProfile = "single-proxy-single-replica-v1"
	productionConfig.ProductionSecurityAck = "ack-single-proxy-single-replica-v1"
	productionConfig.APIReplicaCount = 1
	productionConfig.ImportProductionAck = "ack-backup-v5-single-replica-v1"
	runtime, err = configureBackupImport(ctx, productionConfig, database, importTestLogger(io.Discard))
	if err != nil || runtime == nil {
		t.Fatalf("production import prerequisites were not wired: runtime=%v error=%v", runtime, err)
	}
	var runCount int
	if err := database.QueryRow(`
		SELECT count(*) FROM backup_v5_import_runs WHERE household_id=$1
	`, householdID).Scan(&runCount); err != nil || runCount != 1 {
		t.Fatalf("startup cleanup changed completed runs: count=%d error=%v", runCount, err)
	}
}

func TestCleanupPreviewMetadataBoundariesLoggingAndShutdown(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	repository := &cleanupRepositoryStub{count: 7}
	var logs bytes.Buffer
	deleted, err := cleanupPreviewMetadata(
		context.Background(), repository, importTestLogger(&logs), now,
		time.Second, 24*time.Hour, 500, true,
	)
	if err != nil || deleted != 7 || repository.cutoff != now.Add(-24*time.Hour) ||
		repository.limit != 500 || !strings.Contains(logs.String(), `"deleted_count":7`) {
		t.Fatalf("cleanup mismatch: deleted=%d error=%v cutoff=%s limit=%d logs=%s",
			deleted, err, repository.cutoff, repository.limit, logs.String())
	}

	logs.Reset()
	repository.err = errors.New("database secret path key-id")
	if _, err := cleanupPreviewMetadata(
		context.Background(), repository, importTestLogger(&logs), now,
		time.Second, 24*time.Hour, 500, false,
	); err == nil {
		t.Fatal("cleanup repository error was ignored")
	}
	for _, secret := range []string{"database secret", "path", "key-id"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("cleanup log disclosed %q: %s", secret, logs.String())
		}
	}

	blocking := &cleanupRepositoryStub{started: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runPreviewCleanupLoop(ctx, blocking, importTestLogger(io.Discard), time.Millisecond, time.Second, 24*time.Hour, 10)
		close(done)
	}()
	<-blocking.started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup loop did not stop after cancellation")
	}
}
