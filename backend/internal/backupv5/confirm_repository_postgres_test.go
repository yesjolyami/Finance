package backupv5

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresConfirmImportsCanonicalModelAtomically(t *testing.T) {
	fixture := newPreviewDBFixture(t, "owner")
	keyring := testHMACKeyring(t)
	service := newPostgresConfirmService(t, fixture, keyring)
	preview := createConfirmPreview(t, fixture, canonicalFixture, 41)
	input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "canonical-import")
	result, err := service.Confirm(context.Background(), input)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if result.Replayed || result.Response.Status != "completed" || result.Response.PolicyVersion != PolicyVersion {
		t.Fatalf("result = %#v", result)
	}
	model, err := ParseReader(context.Background(), strings.NewReader(canonicalFixture), Input{
		HouseholdID: fixture.householdID, BudgetMonth: fixtureInput.BudgetMonth,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Response.Counts.Accounts != len(model.Accounts) || result.Response.WarningCounts.DebtOverpaid != 1 {
		t.Fatalf("response counts/warnings = %#v", result.Response)
	}
	assertCanonicalImportedFields(t, fixture, model, result.Response)
	assertImportControlAndAudit(t, fixture, model, result.Response, "request-confirm-1", "active")
	assertPreviewCount(t, fixture.database, fixture.householdID, 0)
	replayInput := confirmInputForFixture(fixture, canonicalFixture, "malformed=", "canonical-import")
	replay, err := service.Confirm(context.Background(), replayInput)
	if err != nil || !replay.Replayed || !reflect.DeepEqual(replay.Response, result.Response) {
		t.Fatalf("stored bounded result replay = %#v, error = %v", replay, err)
	}
}

func TestPostgresConfirmReplayOrderingAndRotation(t *testing.T) {
	fixture := newPreviewDBFixture(t, "owner")
	oldActive, err := NewHMACKeyring("old", map[string][]byte{
		"old": bytes.Repeat([]byte{0x61}, 32), "new": bytes.Repeat([]byte{0x62}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	service := newPostgresConfirmService(t, fixture, oldActive)
	preview := createConfirmPreview(t, fixture, canonicalFixture, 42)
	input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "rotation-key")
	created, err := service.Confirm(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	newActive, err := NewHMACKeyring("new", map[string][]byte{
		"new": bytes.Repeat([]byte{0x62}, 32), "old": bytes.Repeat([]byte{0x61}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted := newPostgresConfirmService(t, fixture, newActive)
	if err := restarted.AuditKeyring(context.Background()); err != nil {
		t.Fatalf("retained rotation audit = %v", err)
	}
	missingOld, _ := NewHMACKeyring("new", map[string][]byte{"new": bytes.Repeat([]byte{0x62}, 32)})
	missingService := newPostgresConfirmService(t, fixture, missingOld)
	if err := missingService.AuditKeyring(context.Background()); !errors.Is(err, ErrReferencedKeyMissing) {
		t.Fatalf("missing referenced key audit = %v", err)
	}

	for _, state := range []string{"deleted", "expired", "consumed", "revoked", "malformed"} {
		t.Run(state, func(t *testing.T) {
			if _, err := fixture.database.Exec(`DELETE FROM backup_v5_import_previews WHERE household_id=$1`, fixture.householdID); err != nil {
				t.Fatal(err)
			}
			replayInput := confirmInputForFixture(fixture, canonicalFixture, input.PreviewToken, input.IdempotencyKey)
			switch state {
			case "expired":
				replayInput.PreviewToken = insertConfirmPreviewState(t, fixture, canonicalFixture, -20*time.Minute, -10*time.Minute, "")
			case "consumed":
				replayInput.PreviewToken = insertConfirmPreviewState(t, fixture, canonicalFixture, -time.Minute, 9*time.Minute, "consumed")
			case "revoked":
				replayInput.PreviewToken = insertConfirmPreviewState(t, fixture, canonicalFixture, -time.Minute, 9*time.Minute, "revoked")
			case "malformed":
				replayInput.PreviewToken = "malformed="
			}
			replayed, err := restarted.Confirm(context.Background(), replayInput)
			if err != nil || !replayed.Replayed || replayed.Response.ImportRunID != created.Response.ImportRunID {
				t.Fatalf("replay = %#v, error = %v", replayed, err)
			}
		})
	}

	conflict := confirmInputForFixture(fixture, canonicalFixture+"\n", input.PreviewToken, input.IdempotencyKey)
	conflict.PreviewToken = "malformed="
	if _, err := restarted.Confirm(context.Background(), conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("digest conflict = %v", err)
	}
	monthConflict := confirmInputForFixture(fixture, canonicalFixture, input.PreviewToken, input.IdempotencyKey)
	monthConflict.BudgetMonth = "2026-08-01"
	monthConflict.PreviewToken = "malformed="
	if _, err := restarted.Confirm(context.Background(), monthConflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("month conflict = %v", err)
	}
	newToken := insertConfirmPreviewState(t, fixture, canonicalFixture, -time.Minute, 9*time.Minute, "")
	newKey := confirmInputForFixture(fixture, canonicalFixture, newToken, "new-after-import")
	if _, err := restarted.Confirm(context.Background(), newKey); !errors.Is(err, ErrHouseholdNotEmpty) {
		t.Fatalf("new key after import = %v", err)
	}
}

func TestPostgresConfirmRejectsMultipleLogicalRunsAcrossKeyIDs(t *testing.T) {
	fixture := newPreviewDBFixture(t, "owner")
	keyring := testHMACKeyring(t)
	input := confirmInputForFixture(fixture, canonicalFixture, "malformed=", "corrupt-logical-key")
	command := captureConfirmCommand(t, keyring, input)
	for _, candidate := range command.Candidates {
		_, err := fixture.database.Exec(`
			INSERT INTO backup_v5_import_runs (
			 id,household_id,actor_user_id,hmac_key_id,idempotency_key_hmac,
			 request_fingerprint_hmac,policy_version,status,
			 accounts_count,categories_count,transactions_count,budgets_count,
			 goals_count,goal_contributions_count,debts_count,debt_payments_count,
			 legacy_owner_not_linked_count,archive_time_approximated_count,
			 goal_exceeds_target_count,debt_overpaid_count,system_resource_preserved_count,
			 budget_month_explicit_choice_count,completed_at,created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,'completed',0,0,0,0,0,0,0,0,0,0,0,0,0,0,now(),now())
		`, uuid.New(), fixture.householdID, fixture.userID, candidate.KeyID,
			candidate.Lookup[:], candidate.Fingerprint[:], PolicyVersion)
		if err != nil {
			t.Fatal(err)
		}
	}
	service := newPostgresConfirmService(t, fixture, keyring)
	fresh := confirmInputForFixture(fixture, canonicalFixture, "malformed=", "corrupt-logical-key")
	if _, err := service.Confirm(context.Background(), fresh); !errors.Is(err, ErrImportStateConflict) {
		t.Fatalf("multiple logical runs error = %v", err)
	}
}

func TestPostgresConfirmConcurrency(t *testing.T) {
	t.Run("same key and token", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
		preview := createConfirmPreview(t, fixture, canonicalFixture, 43)
		input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "concurrent-same")
		secondInput := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "concurrent-same")
		results, errs := runConcurrentConfirms(service, input, secondInput)
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("errors = %v", errs)
		}
		if results[0].Replayed == results[1].Replayed || results[0].Response.ImportRunID != results[1].Response.ImportRunID {
			t.Fatalf("results = %#v", results)
		}
		assertImportRowCounts(t, fixture, 1, 1)
	})
	t.Run("different tokens and keys", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
		firstPreview := createConfirmPreview(t, fixture, canonicalFixture, 44)
		secondOwner := insertPreviewUser(t, fixture.database, uuid.New().String(), false)
		if _, err := fixture.database.Exec(`
			INSERT INTO household_members (id,household_id,user_id,role,status)
			VALUES ($1,$2,$3,'owner','active')
		`, uuid.New(), fixture.householdID, secondOwner.id); err != nil {
			t.Fatal(err)
		}
		secondPreview := createConfirmPreviewForActor(t, fixture, secondOwner.subject, fixture.householdID, canonicalFixture, 45)
		first := confirmInputForFixture(fixture, canonicalFixture, firstPreview.ConfirmationToken, "different-one")
		second := confirmInputForFixture(fixture, canonicalFixture, secondPreview.ConfirmationToken, "different-two")
		second.Subject = secondOwner.subject
		results, errs := runConcurrentConfirms(service, first, second)
		successes, nonEmpty := 0, 0
		for index, err := range errs {
			if err == nil && !results[index].Replayed {
				successes++
			} else if errors.Is(err, ErrHouseholdNotEmpty) {
				nonEmpty++
			}
		}
		if successes != 1 || nonEmpty != 1 {
			t.Fatalf("results/errors = %#v / %v", results, errs)
		}
		assertImportRowCounts(t, fixture, 1, 1)
		assertPreviewCount(t, fixture.database, fixture.householdID, 0)
	})
}

func TestPostgresConfirmGenericTokenAndAuthorizationFailures(t *testing.T) {
	t.Run("active non-owner forbidden", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "admin")
		input := confirmInputForFixture(fixture, canonicalFixture, "malformed=", "admin-key")
		service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
		if _, err := service.Confirm(context.Background(), input); !errors.Is(err, ErrForbidden) {
			t.Fatalf("error = %v", err)
		}
	})
	for _, state := range []string{"wrong digest", "wrong month", "expired", "consumed", "revoked"} {
		t.Run(state, func(t *testing.T) {
			fixture := newPreviewDBFixture(t, "owner")
			service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
			token := insertConfirmPreviewState(t, fixture, canonicalFixture, -time.Minute, 9*time.Minute, "")
			switch state {
			case "wrong digest":
				if _, err := fixture.database.Exec(`UPDATE backup_v5_import_previews SET revoked_at=now() WHERE household_id=$1`, fixture.householdID); err != nil {
					t.Fatal(err)
				}
				// The immutable binding cannot be altered; use a different valid raw body.
			case "wrong month":
				// The route month differs from the immutable preview binding.
			case "expired":
				if _, err := fixture.database.Exec(`DELETE FROM backup_v5_import_previews WHERE household_id=$1`, fixture.householdID); err != nil {
					t.Fatal(err)
				}
				token = insertConfirmPreviewState(t, fixture, canonicalFixture, -20*time.Minute, -10*time.Minute, "")
			case "consumed":
				if _, err := fixture.database.Exec(`UPDATE backup_v5_import_previews SET consumed_at=now() WHERE household_id=$1`, fixture.householdID); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if _, err := fixture.database.Exec(`UPDATE backup_v5_import_previews SET revoked_at=now() WHERE household_id=$1`, fixture.householdID); err != nil {
					t.Fatal(err)
				}
			}
			input := confirmInputForFixture(fixture, canonicalFixture, token, "token-state-"+state)
			if state == "wrong digest" {
				input.RawJSON = strings.NewReader(canonicalFixture + "\n")
			}
			if state == "wrong month" {
				input.BudgetMonth = "2026-08-01"
			}
			if _, err := service.Confirm(context.Background(), input); !errors.Is(err, ErrPreviewTokenInvalid) {
				t.Fatalf("error = %v", err)
			}
			assertConfirmRollbackState(t, fixture, 1)
		})
	}
}

func TestPostgresConfirmOpaqueIdentityAndTenantBinding(t *testing.T) {
	t.Run("missing household", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		input := confirmInputForFixture(fixture, canonicalFixture, "malformed=", "missing-household")
		input.HouseholdID = uuid.New()
		service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
		if _, err := service.Confirm(context.Background(), input); !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("foreign actor", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		foreign := insertPreviewUser(t, fixture.database, uuid.New().String(), false)
		input := confirmInputForFixture(fixture, canonicalFixture, "malformed=", "foreign-actor")
		input.Subject = foreign.subject
		service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
		if _, err := service.Confirm(context.Background(), input); !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v", err)
		}
	})
	for _, state := range []string{"deleted user", "inactive membership"} {
		t.Run(state, func(t *testing.T) {
			fixture := newPreviewDBFixture(t, "owner")
			if state == "deleted user" {
				_, _ = fixture.database.Exec(`UPDATE users SET deleted_at=now() WHERE id=$1`, fixture.userID)
			} else {
				_, _ = fixture.database.Exec(`UPDATE household_members SET status='removed',removed_at=now() WHERE id=$1`, fixture.membershipID)
			}
			input := confirmInputForFixture(fixture, canonicalFixture, "malformed=", "inactive-identity")
			service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
			if _, err := service.Confirm(context.Background(), input); !errors.Is(err, ErrNotFound) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	t.Run("cross actor token", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		other := insertPreviewUser(t, fixture.database, uuid.New().String(), false)
		if _, err := fixture.database.Exec(`INSERT INTO household_members (id,household_id,user_id,role,status) VALUES ($1,$2,$3,'owner','active')`, uuid.New(), fixture.householdID, other.id); err != nil {
			t.Fatal(err)
		}
		preview := createConfirmPreviewForActor(t, fixture, other.subject, fixture.householdID, canonicalFixture, 52)
		input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "cross-actor-token")
		service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
		if _, err := service.Confirm(context.Background(), input); !errors.Is(err, ErrPreviewTokenInvalid) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("cross household token", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		otherHousehold := uuid.New()
		if _, err := fixture.database.Exec(`INSERT INTO households (id,name,created_by_user_id) VALUES ($1,'Other household',$2)`, otherHousehold, fixture.userID); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.database.Exec(`INSERT INTO household_members (id,household_id,user_id,role,status) VALUES ($1,$2,$3,'owner','active')`, uuid.New(), otherHousehold, fixture.userID); err != nil {
			t.Fatal(err)
		}
		preview := createConfirmPreviewForActor(t, fixture, fixture.subject, otherHousehold, canonicalFixture, 53)
		input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "cross-household-token")
		service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
		if _, err := service.Confirm(context.Background(), input); !errors.Is(err, ErrPreviewTokenInvalid) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestPostgresConfirmFailuresRollbackAndRetry(t *testing.T) {
	t.Run("reconciliation", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		keyring := testHMACKeyring(t)
		preview := createConfirmPreview(t, fixture, canonicalFixture, 46)
		input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "reconcile-failure")
		command := captureConfirmCommand(t, keyring, input)
		command.Model.Totals.IncomeCents++
		_, err := fixtureConfirmRepository(fixture).Confirm(context.Background(), command)
		if !errors.Is(err, ErrReconciliation) {
			t.Fatalf("error = %v", err)
		}
		assertConfirmRollbackState(t, fixture, 1)
		service := newPostgresConfirmService(t, fixture, keyring)
		retry := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "reconcile-failure")
		if _, err := service.Confirm(context.Background(), retry); err != nil {
			t.Fatalf("retry error = %v", err)
		}
	})
	t.Run("audit", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
		preview := createConfirmPreview(t, fixture, canonicalFixture, 47)
		input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "audit-failure")
		installFailingAuditTrigger(t, fixture.database)
		if _, err := service.Confirm(context.Background(), input); !errors.Is(err, ErrRepository) {
			t.Fatalf("error = %v", err)
		}
		assertConfirmRollbackState(t, fixture, 1)
		removeFailingAuditTrigger(t, fixture.database)
		retry := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "audit-failure")
		if _, err := service.Confirm(context.Background(), retry); err != nil {
			t.Fatalf("retry error = %v", err)
		}
	})
	t.Run("goal reconciliation", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
		preview := createConfirmPreview(t, fixture, canonicalFixture, 51)
		input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "goal-reconcile-failure")
		installGoalMutationTrigger(t, fixture.database)
		if _, err := service.Confirm(context.Background(), input); !errors.Is(err, ErrReconciliation) {
			t.Fatalf("error = %v", err)
		}
		assertConfirmRollbackState(t, fixture, 1)
		removeGoalMutationTrigger(t, fixture.database)
		retry := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "goal-reconcile-failure")
		if _, err := service.Confirm(context.Background(), retry); err != nil {
			t.Fatalf("retry error = %v", err)
		}
	})
	t.Run("import run constraint after reconciliation", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
		preview := createConfirmPreview(t, fixture, canonicalFixture, 54)
		installRunConstraintTrigger(t, fixture.database)
		input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "run-constraint-failure")
		if _, err := service.Confirm(context.Background(), input); !errors.Is(err, ErrRepository) {
			t.Fatalf("error = %v", err)
		}
		assertConfirmRollbackState(t, fixture, 1)
		removeRunConstraintTrigger(t, fixture.database)
		retry := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "run-constraint-failure")
		if _, err := service.Confirm(context.Background(), retry); err != nil {
			t.Fatalf("retry error = %v", err)
		}
	})
	t.Run("preview delete after run and audit", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
		preview := createConfirmPreview(t, fixture, canonicalFixture, 55)
		installPreviewDeleteTrigger(t, fixture.database)
		input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "delete-failure")
		if _, err := service.Confirm(context.Background(), input); !errors.Is(err, ErrRepository) {
			t.Fatalf("error = %v", err)
		}
		assertConfirmRollbackState(t, fixture, 1)
		removePreviewDeleteTrigger(t, fixture.database)
		retry := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "delete-failure")
		if _, err := service.Confirm(context.Background(), retry); err != nil {
			t.Fatalf("retry error = %v", err)
		}
	})
	t.Run("late cancellation on run insert", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
		preview := createConfirmPreview(t, fixture, canonicalFixture, 56)
		installRunSleepTrigger(t, fixture.database)
		input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "late-cancel")
		ctx, cancel := context.WithTimeout(context.Background(), 125*time.Millisecond)
		defer cancel()
		if _, err := service.Confirm(ctx, input); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v", err)
		}
		assertConfirmRollbackState(t, fixture, 1)
		removeRunSleepTrigger(t, fixture.database)
		retry := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "late-cancel")
		if _, err := service.Confirm(context.Background(), retry); err != nil {
			t.Fatalf("retry error = %v", err)
		}
	})
	t.Run("constraint", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		keyring := testHMACKeyring(t)
		preview := createConfirmPreview(t, fixture, canonicalFixture, 48)
		input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "constraint-failure")
		command := captureConfirmCommand(t, keyring, input)
		command.Model.Accounts[0].Name = strings.Repeat("x", 121)
		if _, err := fixtureConfirmRepository(fixture).Confirm(context.Background(), command); !errors.Is(err, ErrRepository) {
			t.Fatalf("error = %v", err)
		}
		assertConfirmRollbackState(t, fixture, 1)
	})
	t.Run("cancellation", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
		preview := createConfirmPreview(t, fixture, canonicalFixture, 49)
		input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "cancel-failure")
		blocker, err := fixture.database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := blocker.Exec(`SELECT 1 FROM households WHERE id=$1 FOR UPDATE`, fixture.householdID); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err = service.Confirm(ctx, input)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v", err)
		}
		_ = blocker.Rollback()
		assertConfirmRollbackState(t, fixture, 1)
		retry := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "cancel-failure")
		if _, err := service.Confirm(context.Background(), retry); err != nil {
			t.Fatalf("retry error = %v", err)
		}
	})
}

func TestPostgresConfirmSerializesWithFinanceMutation(t *testing.T) {
	fixture := newPreviewDBFixture(t, "owner")
	service := newPostgresConfirmService(t, fixture, testHMACKeyring(t))
	preview := createConfirmPreview(t, fixture, canonicalFixture, 50)
	input := confirmInputForFixture(fixture, canonicalFixture, preview.ConfirmationToken, "finance-race")
	finance, err := fixture.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := finance.Exec(`SELECT 1 FROM households WHERE id=$1 FOR KEY SHARE`, fixture.householdID); err != nil {
		t.Fatal(err)
	}
	if _, err := finance.Exec(`INSERT INTO accounts (id,household_id,name,color) VALUES ($1,$2,'Concurrent account','#112233')`, uuid.New(), fixture.householdID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, confirmErr := service.Confirm(context.Background(), input)
		result <- confirmErr
	}()
	time.Sleep(75 * time.Millisecond)
	if err := finance.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrHouseholdNotEmpty) {
			t.Fatalf("confirm race error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("finance/confirm lock ordering deadlocked")
	}
	var accounts, importedTransactions int
	if err := fixture.database.QueryRow(`
		SELECT (SELECT count(*) FROM accounts WHERE household_id=$1),
		       (SELECT count(*) FROM transactions WHERE household_id=$1)
	`, fixture.householdID).Scan(&accounts, &importedTransactions); err != nil {
		t.Fatal(err)
	}
	if accounts != 1 || importedTransactions != 0 {
		t.Fatalf("mixed rows: accounts=%d transactions=%d", accounts, importedTransactions)
	}
}

func newPostgresConfirmService(t *testing.T, fixture *previewDBFixture, keyring *HMACKeyring) *ConfirmService {
	t.Helper()
	service, err := NewConfirmService(fixtureConfirmRepository(fixture), keyring)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func fixtureConfirmRepository(fixture *previewDBFixture) *PostgresConfirmRepository {
	return NewPostgresConfirmRepository(fixture.database)
}

func createConfirmPreview(t *testing.T, fixture *previewDBFixture, raw string, fill byte) PreviewResponse {
	return createConfirmPreviewForActor(t, fixture, fixture.subject, fixture.householdID, raw, fill)
}

func createConfirmPreviewForActor(t *testing.T, fixture *previewDBFixture, subject string, householdID uuid.UUID, raw string, fill byte) PreviewResponse {
	t.Helper()
	entropy := bytes.Repeat([]byte{fill}, previewTokenBytes+16)
	service, err := NewPreviewService(
		fixture.repository,
		WithClock(fixedClock{now: time.Now().UTC().Truncate(time.Microsecond)}),
		WithEntropy(bytes.NewReader(entropy)),
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.Preview(
		context.Background(), subject, householdID,
		fixtureInput.BudgetMonth, strings.NewReader(raw),
	)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func confirmInputForFixture(fixture *previewDBFixture, raw, token, key string) ConfirmInput {
	return ConfirmInput{
		Subject: fixture.subject, HouseholdID: fixture.householdID,
		BudgetMonth: fixtureInput.BudgetMonth, RawJSON: strings.NewReader(raw),
		PreviewToken: token, IdempotencyKey: key, ServerRequestID: "request-confirm-1",
	}
}

func insertConfirmPreviewState(t *testing.T, fixture *previewDBFixture, raw string, createdOffset, expiresOffset time.Duration, terminal string) string {
	t.Helper()
	sequence := previewFixtureSequence.Add(1)
	secret := sha256.Sum256([]byte(fmt.Sprintf("confirm-preview-secret-%d", sequence)))
	token := base64RawURL(secret[:])
	tokenHash := sha256.Sum256(secret[:])
	digest := sha256.Sum256([]byte(raw))
	now := time.Now().UTC().Truncate(time.Microsecond)
	var consumed, revoked any
	if terminal == "consumed" {
		consumed = now
	}
	if terminal == "revoked" {
		revoked = now
	}
	_, err := fixture.database.Exec(`
		INSERT INTO backup_v5_import_previews (
		 id,household_id,actor_user_id,token_hash,backup_digest,budget_month,
		 policy_version,expires_at,consumed_at,revoked_at,created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`, uuid.New(), fixture.householdID, fixture.userID, tokenHash[:], digest[:],
		fixtureInput.BudgetMonth, PolicyVersion, now.Add(expiresOffset), consumed, revoked, now.Add(createdOffset))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func captureConfirmCommand(t *testing.T, keyring *HMACKeyring, input ConfirmInput) ConfirmCommand {
	t.Helper()
	repository := &fakeConfirmRepository{result: ConfirmResult{Response: ConfirmResponse{Status: "completed"}}}
	service, err := NewConfirmService(repository, keyring)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Confirm(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	return repository.command
}

func runConcurrentConfirms(service *ConfirmService, inputs ...ConfirmInput) ([]ConfirmResult, []error) {
	results := make([]ConfirmResult, len(inputs))
	errs := make([]error, len(inputs))
	var wait sync.WaitGroup
	wait.Add(len(inputs))
	for index := range inputs {
		go func(index int) {
			defer wait.Done()
			results[index], errs[index] = service.Confirm(context.Background(), inputs[index])
		}(index)
	}
	wait.Wait()
	return results, errs
}

func assertCanonicalImportedFields(t *testing.T, fixture *previewDBFixture, model *Model, response ConfirmResponse) {
	t.Helper()
	for _, account := range model.Accounts {
		var name, color, kind, bank, owner, currency string
		var sortOrder int
		var system bool
		var ownerID, creationKey sql.NullString
		var created, updated time.Time
		var archived, deleted sql.NullTime
		var version int64
		var creationHash []byte
		if err := fixture.database.QueryRow(`
		SELECT name,color,sort_order,account_type,bank_label,legacy_owner_label,
		 currency_code,is_system,owner_user_id::text,created_at,updated_at,archived_at,deleted_at,
		 version,creation_idempotency_key,creation_payload_hash
		FROM accounts WHERE id=$1
	`, account.ID).Scan(&name, &color, &sortOrder, &kind, &bank, &owner, &currency,
			&system, &ownerID, &created, &updated, &archived, &deleted, &version, &creationKey, &creationHash); err != nil {
			t.Fatal(err)
		}
		if name != account.Name || color != account.Color || sortOrder != int(account.SortOrder) || kind != account.Kind ||
			bank != account.BankLabel || owner != account.LegacyOwnerLabel || currency != "RUB" || system != account.IsSystem ||
			ownerID.Valid || !created.Equal(account.CreatedAt) || !updated.Equal(account.CreatedAt) || archived.Valid || deleted.Valid ||
			version != 1 || creationKey.Valid || creationHash != nil {
			t.Fatalf("account %s mapping mismatch", account.ID)
		}
	}
	for _, category := range model.Categories {
		var categoryType, name, color string
		var sortOrder int
		var system bool
		var created, updated time.Time
		var archived, deleted sql.NullTime
		var version int64
		var creationKey sql.NullString
		var creationHash []byte
		if err := fixture.database.QueryRow(`
			SELECT category_type,name,color,sort_order,is_system,created_at,updated_at,
			 archived_at,deleted_at,version,creation_idempotency_key,creation_payload_hash
			FROM categories WHERE id=$1
		`, category.ID).Scan(&categoryType, &name, &color, &sortOrder, &system, &created, &updated,
			&archived, &deleted, &version, &creationKey, &creationHash); err != nil {
			t.Fatal(err)
		}
		if categoryType != category.Type || name != category.Name || color != category.Color || sortOrder != int(category.SortOrder) ||
			system != category.IsSystem || !created.Equal(category.CreatedAt) || !updated.Equal(category.CreatedAt) ||
			archived.Valid || deleted.Valid || version != 1 || creationKey.Valid || creationHash != nil {
			t.Fatalf("category %s mapping mismatch", category.ID)
		}
	}
	for _, budget := range model.Budgets {
		var categoryID, categoryType, month string
		var amount int64
		var created, updated time.Time
		var deleted sql.NullTime
		if err := fixture.database.QueryRow(`
			SELECT category_id::text,category_type,budget_month::text,amount_cents,created_at,updated_at,deleted_at
			FROM budgets WHERE id=$1
		`, budget.ID).Scan(&categoryID, &categoryType, &month, &amount, &created, &updated, &deleted); err != nil {
			t.Fatal(err)
		}
		if categoryID != budget.CategoryID.String() || categoryType != "expense" || month != budget.Month.String() ||
			amount != budget.AmountCents || !created.Equal(response.CompletedAt) || !updated.Equal(response.CompletedAt) || deleted.Valid {
			t.Fatalf("budget %s mapping mismatch", budget.ID)
		}
	}
	for _, transaction := range model.Transactions {
		var transactionType, accountID, eventDate, note, source, idempotency, actorCreated, actorUpdated string
		var toAccountID, categoryID, actorDeleted, deletionReason sql.NullString
		var amount, version int64
		var adjustment bool
		var payload []byte
		var created, updated time.Time
		var deleted sql.NullTime
		if err := fixture.database.QueryRow(`
			SELECT transaction_type,account_id::text,to_account_id::text,category_id::text,
			 amount_cents,event_date::text,note,is_balance_adjustment,source,idempotency_key,
			 idempotency_payload_hash,created_by_user_id::text,updated_by_user_id::text,
			 deleted_by_user_id::text,deletion_reason,created_at,updated_at,deleted_at,version
			FROM transactions WHERE id=$1
		`, transaction.ID).Scan(&transactionType, &accountID, &toAccountID, &categoryID, &amount, &eventDate,
			&note, &adjustment, &source, &idempotency, &payload, &actorCreated, &actorUpdated,
			&actorDeleted, &deletionReason, &created, &updated, &deleted, &version); err != nil {
			t.Fatal(err)
		}
		if transactionType != transaction.Type || accountID != transaction.AccountID.String() ||
			nullableUUIDValue(toAccountID) != uuidPointerValue(transaction.ToAccountID) ||
			nullableUUIDValue(categoryID) != uuidPointerValue(transaction.CategoryID) || amount != transaction.AmountCents ||
			eventDate != transaction.EventDate.String() || note != transaction.Note || adjustment != transaction.IsBalanceAdjustment ||
			source != "import" || idempotency != transaction.IdempotencyKey || !bytes.Equal(payload, transaction.PayloadHash[:]) ||
			actorCreated != fixture.userID.String() || actorUpdated != fixture.userID.String() || actorDeleted.Valid || deletionReason.Valid ||
			!created.Equal(transaction.CreatedAt) || !updated.Equal(transaction.CreatedAt) || deleted.Valid || version != 1 {
			t.Fatalf("transaction %s mapping mismatch", transaction.ID)
		}
	}
	for _, goal := range model.Goals {
		var name, color string
		var target, initial int64
		var targetDate sql.NullString
		var created, updated time.Time
		var archived, deleted sql.NullTime
		if err := fixture.database.QueryRow(`
			SELECT name,target_amount_cents,initial_saved_cents,target_date::text,color,
			 created_at,updated_at,archived_at,deleted_at FROM goals WHERE id=$1
		`, goal.ID).Scan(&name, &target, &initial, &targetDate, &color, &created, &updated, &archived, &deleted); err != nil {
			t.Fatal(err)
		}
		if name != goal.Name || target != goal.TargetCents || initial != goal.InitialSavedCents ||
			nullableStringValue(targetDate) != localDatePointerValue(goal.TargetDate) || color != goal.Color ||
			!created.Equal(goal.CreatedAt) || !updated.Equal(goal.CreatedAt) || deleted.Valid ||
			archived.Valid != goal.Archived || (goal.Archived && !archived.Time.Equal(response.CompletedAt)) {
			t.Fatalf("goal %s mapping mismatch", goal.ID)
		}
	}
	for _, debt := range model.Debts {
		var person, direction, note string
		var original int64
		var dueDate sql.NullString
		var created, updated time.Time
		var archived, deleted sql.NullTime
		if err := fixture.database.QueryRow(`
			SELECT person_label,direction,original_amount_cents,due_date::text,note,
			 created_at,updated_at,archived_at,deleted_at FROM debts WHERE id=$1
		`, debt.ID).Scan(&person, &direction, &original, &dueDate, &note, &created, &updated, &archived, &deleted); err != nil {
			t.Fatal(err)
		}
		if person != debt.PersonLabel || direction != debt.Direction || original != debt.OriginalAmountCents ||
			nullableStringValue(dueDate) != localDatePointerValue(debt.DueDate) || note != debt.Note ||
			!created.Equal(debt.CreatedAt) || !updated.Equal(debt.CreatedAt) || deleted.Valid ||
			archived.Valid != debt.Archived || (debt.Archived && !archived.Time.Equal(response.CompletedAt)) {
			t.Fatalf("debt %s mapping mismatch", debt.ID)
		}
	}
	for _, payment := range model.DebtPayments {
		var debtID, eventDate, note, source string
		var amount int64
		var created, updated time.Time
		var deleted sql.NullTime
		if err := fixture.database.QueryRow(`
			SELECT debt_id::text,amount_cents,event_date::text,note,source,created_at,updated_at,deleted_at
			FROM debt_payments WHERE id=$1
		`, payment.ID).Scan(&debtID, &amount, &eventDate, &note, &source, &created, &updated, &deleted); err != nil {
			t.Fatal(err)
		}
		if debtID != payment.DebtID.String() || amount != payment.AmountCents || eventDate != payment.EventDate.String() ||
			note != payment.Note || source != "import" || !created.Equal(payment.CreatedAt) || !updated.Equal(payment.CreatedAt) || deleted.Valid {
			t.Fatalf("payment %s mapping mismatch", payment.ID)
		}
	}
}

func nullableUUIDValue(value sql.NullString) string { return nullableStringValue(value) }
func nullableStringValue(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}
func uuidPointerValue(value *uuid.UUID) string {
	if value == nil {
		return ""
	}
	return value.String()
}
func localDatePointerValue(value *LocalDate) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func assertImportControlAndAudit(t *testing.T, fixture *previewDBFixture, model *Model, response ConfirmResponse, requestID, keyID string) {
	t.Helper()
	assertImportRunColumnAllowlist(t, fixture.database)
	var rows, audits int
	var storedKey, status, policy string
	var lookup, fingerprint []byte
	if err := fixture.database.QueryRow(`SELECT count(*) FROM backup_v5_import_runs WHERE household_id=$1`, fixture.householdID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.QueryRow(`
		SELECT hmac_key_id,status,policy_version,idempotency_key_hmac,request_fingerprint_hmac
		FROM backup_v5_import_runs WHERE household_id=$1
	`, fixture.householdID).Scan(&storedKey, &status, &policy, &lookup, &fingerprint); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || storedKey != keyID || status != "completed" || policy != PolicyVersion || len(lookup) != 32 || len(fingerprint) != 32 {
		t.Fatalf("run metadata mismatch")
	}
	var entityType, action, storedRequestID string
	var entityID, actorID string
	var changes []byte
	if err := fixture.database.QueryRow(`
		SELECT count(*),min(entity_type),min(action),min(entity_id::text),min(actor_user_id::text),min(request_id),min(changes::text)
		FROM audit_log WHERE household_id=$1
	`, fixture.householdID).Scan(&audits, &entityType, &action, &entityID, &actorID, &storedRequestID, &changes); err != nil {
		t.Fatal(err)
	}
	if audits != 1 || entityType != "households" || action != "imported" || entityID != fixture.householdID.String() ||
		actorID != fixture.userID.String() || storedRequestID != requestID {
		t.Fatalf("audit binding mismatch")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(changes, &object); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, object, []string{"counts", "policyVersion", "source"})
	for _, forbidden := range []string{"incomeCents", "backupDigest", "budgetMonth", "token", "Main account", model.Accounts[0].ID.String()} {
		if strings.Contains(string(changes), forbidden) {
			t.Fatalf("audit leaked %q: %s", forbidden, changes)
		}
	}
	_ = response
}

func assertImportRunColumnAllowlist(t *testing.T, database *sql.DB) {
	t.Helper()
	rows, err := database.Query(`
		SELECT column_name FROM information_schema.columns
		WHERE table_schema='public' AND table_name='backup_v5_import_runs'
		ORDER BY ordinal_position
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := make([]string, 0, 24)
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	want := []string{
		"id", "household_id", "actor_user_id", "hmac_key_id", "idempotency_key_hmac", "request_fingerprint_hmac",
		"policy_version", "status", "accounts_count", "categories_count", "transactions_count", "budgets_count",
		"goals_count", "goal_contributions_count", "debts_count", "debt_payments_count",
		"legacy_owner_not_linked_count", "archive_time_approximated_count", "goal_exceeds_target_count",
		"debt_overpaid_count", "system_resource_preserved_count", "budget_month_explicit_choice_count", "completed_at", "created_at",
	}
	if strings.Join(columns, ",") != strings.Join(want, ",") {
		t.Fatalf("run columns = %v", columns)
	}
}

func assertImportRowCounts(t *testing.T, fixture *previewDBFixture, runs, audits int) {
	t.Helper()
	var gotRuns, gotAudits int
	if err := fixture.database.QueryRow(`
		SELECT (SELECT count(*) FROM backup_v5_import_runs WHERE household_id=$1),
		       (SELECT count(*) FROM audit_log WHERE household_id=$1)
	`, fixture.householdID).Scan(&gotRuns, &gotAudits); err != nil {
		t.Fatal(err)
	}
	if gotRuns != runs || gotAudits != audits {
		t.Fatalf("run/audit counts = %d/%d", gotRuns, gotAudits)
	}
}

func assertConfirmRollbackState(t *testing.T, fixture *previewDBFixture, previews int) {
	t.Helper()
	assertPreviewCount(t, fixture.database, fixture.householdID, previews)
	counts := financialAndAuditCounts(t, fixture.database, fixture.householdID)
	if counts != ([10]int{}) {
		t.Fatalf("rollback left finance/audit rows: %v", counts)
	}
	assertImportRowCounts(t, fixture, 0, 0)
}

func installFailingAuditTrigger(t *testing.T, database *sql.DB) {
	t.Helper()
	_, err := database.Exec(`
		CREATE FUNCTION backupv5_test_reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'synthetic audit failure'; END; $$;
		CREATE TRIGGER backupv5_test_reject_audit BEFORE INSERT ON audit_log
		FOR EACH ROW EXECUTE FUNCTION backupv5_test_reject_audit();
	`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeFailingAuditTrigger(t, database) })
}

func removeFailingAuditTrigger(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`
		DROP TRIGGER IF EXISTS backupv5_test_reject_audit ON audit_log;
		DROP FUNCTION IF EXISTS backupv5_test_reject_audit();
	`); err != nil {
		t.Fatal(err)
	}
}

func installGoalMutationTrigger(t *testing.T, database *sql.DB) {
	t.Helper()
	_, err := database.Exec(`
		CREATE FUNCTION backupv5_test_mutate_goal() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN NEW.initial_saved_cents := NEW.initial_saved_cents + 1; RETURN NEW; END; $$;
		CREATE TRIGGER backupv5_test_mutate_goal BEFORE INSERT ON goals
		FOR EACH ROW EXECUTE FUNCTION backupv5_test_mutate_goal();
	`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeGoalMutationTrigger(t, database) })
}

func removeGoalMutationTrigger(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`
		DROP TRIGGER IF EXISTS backupv5_test_mutate_goal ON goals;
		DROP FUNCTION IF EXISTS backupv5_test_mutate_goal();
	`); err != nil {
		t.Fatal(err)
	}
}

func installRunConstraintTrigger(t *testing.T, database *sql.DB) {
	t.Helper()
	_, err := database.Exec(`
		CREATE FUNCTION backupv5_test_invalid_run() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN NEW.accounts_count := -1; RETURN NEW; END; $$;
		CREATE TRIGGER backupv5_test_invalid_run BEFORE INSERT ON backup_v5_import_runs
		FOR EACH ROW EXECUTE FUNCTION backupv5_test_invalid_run();
	`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeRunConstraintTrigger(t, database) })
}

func removeRunConstraintTrigger(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`DROP TRIGGER IF EXISTS backupv5_test_invalid_run ON backup_v5_import_runs; DROP FUNCTION IF EXISTS backupv5_test_invalid_run();`); err != nil {
		t.Fatal(err)
	}
}

func installPreviewDeleteTrigger(t *testing.T, database *sql.DB) {
	t.Helper()
	_, err := database.Exec(`
		CREATE FUNCTION backupv5_test_reject_preview_delete() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'synthetic preview delete failure'; END; $$;
		CREATE TRIGGER backupv5_test_reject_preview_delete BEFORE DELETE ON backup_v5_import_previews
		FOR EACH ROW EXECUTE FUNCTION backupv5_test_reject_preview_delete();
	`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removePreviewDeleteTrigger(t, database) })
}

func removePreviewDeleteTrigger(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`DROP TRIGGER IF EXISTS backupv5_test_reject_preview_delete ON backup_v5_import_previews; DROP FUNCTION IF EXISTS backupv5_test_reject_preview_delete();`); err != nil {
		t.Fatal(err)
	}
}

func installRunSleepTrigger(t *testing.T, database *sql.DB) {
	t.Helper()
	_, err := database.Exec(`
		CREATE FUNCTION backupv5_test_sleep_run() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM pg_sleep(5); RETURN NEW; END; $$;
		CREATE TRIGGER backupv5_test_sleep_run BEFORE INSERT ON backup_v5_import_runs
		FOR EACH ROW EXECUTE FUNCTION backupv5_test_sleep_run();
	`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeRunSleepTrigger(t, database) })
}

func removeRunSleepTrigger(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`DROP TRIGGER IF EXISTS backupv5_test_sleep_run ON backup_v5_import_runs; DROP FUNCTION IF EXISTS backupv5_test_sleep_run();`); err != nil {
		t.Fatal(err)
	}
}
