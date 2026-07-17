package households

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"os"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	subjectOwner = "11000000-0000-4000-8000-000000000001"
	subjectA     = "11000000-0000-4000-8000-000000000002"
	subjectB     = "11000000-0000-4000-8000-000000000003"
	subjectC     = "11000000-0000-4000-8000-000000000004"
)

func integrationDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_TEST_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_TEST_URL is not set")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.PingContext(context.Background()); err != nil {
		t.Fatalf("temporary PostgreSQL is unavailable: %v", err)
	}
	resetIntegrationDatabase(t, database)
	return database
}

func resetIntegrationDatabase(t *testing.T, database *sql.DB) {
	t.Helper()
	_, err := database.Exec(`
		TRUNCATE TABLE
			backup_v5_import_runs, backup_v5_import_previews,
			audit_log, transactions, recurring_transactions, debt_payments, debts,
			goal_contributions, goals, budgets, household_invitations, categories,
			accounts, household_members, households, users
	`)
	if err != nil {
		t.Fatalf("reset temporary database: %v", err)
	}
}

func bootstrapUser(t *testing.T, service *Service, subject, name string) User {
	t.Helper()
	result, err := service.Bootstrap(context.Background(), subject, name)
	if err != nil {
		t.Fatalf("bootstrap %s: %v", subject, err)
	}
	return result.User
}

func requireError(t *testing.T, err, expected error) {
	t.Helper()
	if !errors.Is(err, expected) {
		t.Fatalf("expected error %v, got %v", expected, err)
	}
}

func TestPostgresHouseholdAndInvitationLifecycle(t *testing.T) {
	database := integrationDatabase(t)
	service := NewService(NewPostgresRepository(database))
	ctx := context.Background()

	owner := bootstrapUser(t, service, subjectOwner, "Основной профиль")
	repeated, err := service.Bootstrap(ctx, subjectOwner, "")
	if err != nil || repeated.User.DisplayName != "Основной профиль" {
		t.Fatalf("empty bootstrap reset display name: result=%#v err=%v", repeated, err)
	}
	member := bootstrapUser(t, service, subjectA, "Участник A")
	defaultBootstrap, err := service.Bootstrap(ctx, subjectB, "")
	if err != nil || defaultBootstrap.User.DisplayName != defaultDisplayName {
		t.Fatalf("first empty bootstrap did not use safe default: %#v %v", defaultBootstrap, err)
	}
	third := defaultBootstrap.User
	fourth := bootstrapUser(t, service, subjectC, "Участник C")

	household, err := service.CreateHousehold(ctx, subjectOwner, "Дом", "create-home")
	if err != nil || household.Role != "owner" {
		t.Fatalf("create household: %#v %v", household, err)
	}
	replay, err := service.CreateHousehold(ctx, subjectOwner, "Дом", "create-home")
	if err != nil || replay.ID != household.ID {
		t.Fatalf("household retry was not idempotent: %#v %v", replay, err)
	}
	_, err = service.CreateHousehold(ctx, subjectOwner, "Другое имя", "create-home")
	requireError(t, err, ErrConflict)

	listed, err := service.ListHouseholds(ctx, subjectOwner)
	if err != nil || len(listed) != 1 || listed[0].ID != household.ID {
		t.Fatalf("owner household list: %#v %v", listed, err)
	}
	intruderList, err := service.ListHouseholds(ctx, subjectA)
	if err != nil || len(intruderList) != 0 {
		t.Fatalf("unrelated user saw households: %#v %v", intruderList, err)
	}
	_, err = service.GetHousehold(ctx, subjectA, household.ID)
	requireError(t, err, ErrNotFound)
	_, err = service.UpdateHousehold(ctx, subjectA, household.ID, "Чужое изменение")
	requireError(t, err, ErrNotFound)
	_, err = service.ListMembers(ctx, subjectA, household.ID)
	requireError(t, err, ErrNotFound)

	updated, err := service.UpdateHousehold(ctx, subjectOwner, household.ID, "Общий дом")
	if err != nil || updated.Name != "Общий дом" {
		t.Fatalf("owner update failed: %#v %v", updated, err)
	}

	invitation, err := service.CreateInvitation(ctx, subjectOwner, household.ID, "member", 3600, "invite-member-a")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(invitation.Token)
	if err != nil || len(raw) != inviteTokenBytes {
		t.Fatalf("invite token entropy is not 256 bits: len=%d err=%v", len(raw), err)
	}
	expectedHash := sha256.Sum256(raw)
	var storedHash []byte
	if err := database.QueryRow(`SELECT token_hash FROM household_invitations WHERE id = $1::uuid`, invitation.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if string(storedHash) != string(expectedHash[:]) || string(storedHash) == invitation.Token {
		t.Fatal("invitation did not store only the SHA-256 token hash")
	}
	_, err = service.CreateInvitation(ctx, subjectOwner, household.ID, "member", 3600, "invite-member-a")
	requireError(t, err, ErrIdempotencyReplay)
	_, err = service.CreateInvitation(ctx, subjectOwner, household.ID, "admin", 3600, "invite-member-a")
	requireError(t, err, ErrConflict)
	_, err = service.CreateInvitation(ctx, subjectOwner, household.ID, "member", 7200, "invite-member-a")
	requireError(t, err, ErrConflict)

	accepted, err := service.AcceptInvitation(ctx, subjectA, invitation.Token)
	if err != nil || accepted.ID != household.ID || accepted.Role != "member" {
		t.Fatalf("accept invitation: %#v %v", accepted, err)
	}
	_, err = service.AcceptInvitation(ctx, subjectA, invitation.Token)
	requireError(t, err, ErrInvitationUnavailable)
	_, err = service.CreateInvitation(ctx, subjectA, household.ID, "member", 3600, "member-cannot-invite")
	requireError(t, err, ErrForbidden)
	_, err = service.UpdateHousehold(ctx, subjectA, household.ID, "Запрещено")
	requireError(t, err, ErrForbidden)

	adminRole := "admin"
	admin, err := service.UpdateMember(ctx, subjectOwner, household.ID, member.ID, UpdateMemberInput{Role: &adminRole})
	if err != nil || admin.Role != "admin" {
		t.Fatalf("promote admin: %#v %v", admin, err)
	}
	if _, err := service.UpdateHousehold(ctx, subjectA, household.ID, "Дом администратора"); err != nil {
		t.Fatalf("admin could not update household: %v", err)
	}
	ownerRole := "owner"
	_, err = service.UpdateMember(ctx, subjectA, household.ID, member.ID, UpdateMemberInput{Role: &ownerRole})
	requireError(t, err, ErrForbidden)
	memberRole := "member"
	_, err = service.UpdateMember(ctx, subjectA, household.ID, owner.ID, UpdateMemberInput{Role: &memberRole})
	requireError(t, err, ErrForbidden)
	adminInvitation, err := service.CreateInvitation(ctx, subjectA, household.ID, "member", 3600, "admin-invite")
	if err != nil || adminInvitation.Token == "" {
		t.Fatalf("admin could not create member invitation: %#v %v", adminInvitation, err)
	}
	_, err = service.UpdateMember(ctx, subjectOwner, household.ID, owner.ID, UpdateMemberInput{Role: &memberRole})
	requireError(t, err, ErrConflict)

	revoked, err := service.CreateInvitation(ctx, subjectOwner, household.ID, "member", 3600, "invite-revoke")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeInvitation(ctx, subjectOwner, household.ID, revoked.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeInvitation(ctx, subjectOwner, household.ID, revoked.ID); err != nil {
		t.Fatalf("revoke retry was not idempotent: %v", err)
	}
	_, err = service.AcceptInvitation(ctx, subjectB, revoked.Token)
	requireError(t, err, ErrInvitationUnavailable)

	expiredRaw := []byte("01234567890123456789012345678901")
	expiredHash := sha256.Sum256(expiredRaw)
	_, err = database.Exec(`
		WITH moment AS (SELECT clock_timestamp() AS now)
		INSERT INTO household_invitations (
			id, household_id, role, token_hash, request_idempotency_key,
			invited_by_user_id, ttl_seconds, created_at, expires_at
		)
		SELECT
			'dd000000-0000-4000-8000-000000000001', $1::uuid, 'member', $2,
			'expired-integration', $3::uuid, 3600,
			moment.now - INTERVAL '2 hours', moment.now - INTERVAL '1 hour'
		FROM moment
	`, household.ID, expiredHash[:], owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.AcceptInvitation(ctx, subjectB, base64.RawURLEncoding.EncodeToString(expiredRaw))
	requireError(t, err, ErrInvitationUnavailable)

	_, err = database.Exec(`
		INSERT INTO household_members (
			id, household_id, user_id, role, status, removed_at
		) VALUES (
			'cc000000-0000-4000-8000-000000000004', $1::uuid, $2::uuid,
			'member', 'removed', clock_timestamp()
		)
	`, household.ID, fourth.ID)
	if err != nil {
		t.Fatal(err)
	}
	reactivate, err := service.CreateInvitation(ctx, subjectOwner, household.ID, "member", 3600, "invite-reactivate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcceptInvitation(ctx, subjectC, reactivate.Token); err != nil {
		t.Fatalf("reactivate removed membership: %v", err)
	}
	var reactivationAction string
	err = database.QueryRow(`
		SELECT action
		FROM audit_log
		WHERE household_id = $1::uuid
		  AND entity_type = 'household_members'
		  AND entity_id = 'cc000000-0000-4000-8000-000000000004'::uuid
		ORDER BY occurred_at DESC
		LIMIT 1
	`, household.ID).Scan(&reactivationAction)
	if err != nil || reactivationAction != "updated" {
		t.Fatalf("reactivation audit action: %q %v", reactivationAction, err)
	}

	var leakedAuditRows int
	if err := database.QueryRow(`
		SELECT count(*) FROM audit_log WHERE changes::text LIKE '%' || $1 || '%'
	`, invitation.Token).Scan(&leakedAuditRows); err != nil || leakedAuditRows != 0 {
		t.Fatalf("raw token leaked to audit: count=%d err=%v", leakedAuditRows, err)
	}

	for _, expected := range []struct {
		actorID  string
		entity   string
		entityID string
		action   string
	}{
		{owner.ID, "households", household.ID, "created"},
		{owner.ID, "households", household.ID, "updated"},
		{owner.ID, "household_invitations", invitation.ID, "created"},
		{member.ID, "household_invitations", invitation.ID, "updated"},
	} {
		var count int
		err := database.QueryRow(`
			SELECT count(*)
			FROM audit_log
			WHERE household_id = $1::uuid
			  AND actor_user_id = $2::uuid
			  AND entity_type = $3
			  AND entity_id = $4::uuid
			  AND action = $5
		`, household.ID, expected.actorID, expected.entity, expected.entityID, expected.action).Scan(&count)
		if err != nil || count == 0 {
			t.Fatalf("missing audit actor/household assertion: %#v count=%d err=%v", expected, count, err)
		}
	}

	_ = third
}

func TestPostgresSoftDeletedOwnerDoesNotSatisfyLastOwnerInvariant(t *testing.T) {
	database := integrationDatabase(t)
	service := NewService(NewPostgresRepository(database))
	ctx := context.Background()
	usableOwner := bootstrapUser(t, service, subjectOwner, "Usable owner")
	deletedOwner := bootstrapUser(t, service, subjectA, "Deleted owner")
	household, err := service.CreateHousehold(ctx, subjectOwner, "Usable owner guard", "usable-owner")
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`
		INSERT INTO household_members (id, household_id, user_id, role)
		VALUES (gen_random_uuid(), $1::uuid, $2::uuid, 'owner')
	`, household.ID, deletedOwner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE users SET deleted_at = clock_timestamp() WHERE id = $1::uuid`, deletedOwner.ID); err != nil {
		t.Fatal(err)
	}

	memberRole := "member"
	_, err = service.UpdateMember(ctx, subjectOwner, household.ID, usableOwner.ID, UpdateMemberInput{Role: &memberRole})
	requireError(t, err, ErrConflict)
	var usableOwners int
	if err := database.QueryRow(`
		SELECT count(*)
		FROM household_members m
		JOIN users u ON u.id = m.user_id AND u.deleted_at IS NULL
		WHERE m.household_id = $1::uuid AND m.role = 'owner' AND m.status = 'active'
	`, household.ID).Scan(&usableOwners); err != nil || usableOwners != 1 {
		t.Fatalf("usable owner invariant failed: owners=%d err=%v", usableOwners, err)
	}
}

func TestPostgresFailClosedDeletedProfilesAndIdempotentFallback(t *testing.T) {
	database := integrationDatabase(t)
	service := NewService(NewPostgresRepository(database))
	ctx := context.Background()
	user := bootstrapUser(t, service, subjectOwner, "Профиль")

	archived, err := service.CreateHousehold(ctx, subjectOwner, "Архив", "archive-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE households SET archived_at = clock_timestamp() WHERE id = $1::uuid`, archived.ID); err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateHousehold(ctx, subjectOwner, "Архив", "archive-key")
	requireError(t, err, ErrNotFound)

	removed, err := service.CreateHousehold(ctx, subjectOwner, "Удаленное участие", "removed-membership-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		UPDATE household_members
		SET status = 'removed', removed_at = clock_timestamp()
		WHERE household_id = $1::uuid AND user_id = $2::uuid
	`, removed.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateHousehold(ctx, subjectOwner, "Удаленное участие", "removed-membership-key")
	requireError(t, err, ErrNotFound)

	active, err := service.CreateHousehold(ctx, subjectOwner, "Удаленный профиль", "deleted-profile-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE users SET deleted_at = clock_timestamp() WHERE id = $1::uuid`, user.ID); err != nil {
		t.Fatal(err)
	}
	list, err := service.ListHouseholds(ctx, subjectOwner)
	if err != nil || len(list) != 0 {
		t.Fatalf("soft-deleted profile saw household names: %#v %v", list, err)
	}
	_, err = service.Bootstrap(ctx, subjectOwner, "Новое имя")
	requireError(t, err, ErrNotFound)
	_, err = service.GetHousehold(ctx, subjectOwner, active.ID)
	requireError(t, err, ErrNotFound)
}

func TestPostgresConcurrentLastOwnerMutationsKeepOwner(t *testing.T) {
	database := integrationDatabase(t)
	service := NewService(NewPostgresRepository(database))
	ctx := context.Background()
	ownerA := bootstrapUser(t, service, subjectOwner, "Owner A")
	ownerB := bootstrapUser(t, service, subjectA, "Owner B")
	household, err := service.CreateHousehold(ctx, subjectOwner, "Два owner", "two-owners")
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`
		INSERT INTO household_members (id, household_id, user_id, role)
		VALUES (gen_random_uuid(), $1::uuid, $2::uuid, 'owner')
	`, household.ID, ownerB.ID)
	if err != nil {
		t.Fatal(err)
	}

	memberRole := "member"
	start := make(chan struct{})
	errorsByRequest := make([]error, 2)
	var wait sync.WaitGroup
	for index, request := range []struct {
		subject string
		userID  string
	}{{subjectOwner, ownerA.ID}, {subjectA, ownerB.ID}} {
		wait.Add(1)
		go func(index int, subject, userID string) {
			defer wait.Done()
			<-start
			_, errorsByRequest[index] = service.UpdateMember(ctx, subject, household.ID, userID, UpdateMemberInput{Role: &memberRole})
		}(index, request.subject, request.userID)
	}
	close(start)
	wait.Wait()

	successes := 0
	conflicts := 0
	for _, err := range errorsByRequest {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent role error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("unexpected concurrent result: success=%d conflict=%d", successes, conflicts)
	}
	var activeOwners int
	if err := database.QueryRow(`
		SELECT count(*) FROM household_members
		WHERE household_id = $1::uuid AND role = 'owner' AND status = 'active'
	`, household.ID).Scan(&activeOwners); err != nil || activeOwners != 1 {
		t.Fatalf("last owner invariant failed: owners=%d err=%v", activeOwners, err)
	}
}

func TestPostgresConcurrentInvitationAcceptIsSingleUse(t *testing.T) {
	database := integrationDatabase(t)
	service := NewService(NewPostgresRepository(database))
	ctx := context.Background()
	bootstrapUser(t, service, subjectOwner, "Owner")
	bootstrapUser(t, service, subjectA, "Candidate A")
	bootstrapUser(t, service, subjectB, "Candidate B")
	household, err := service.CreateHousehold(ctx, subjectOwner, "Invite race", "invite-race-household")
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := service.CreateInvitation(ctx, subjectOwner, household.ID, "member", 3600, "invite-race")
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errorsByRequest := make([]error, 2)
	var wait sync.WaitGroup
	for index, subject := range []string{subjectA, subjectB} {
		wait.Add(1)
		go func(index int, subject string) {
			defer wait.Done()
			<-start
			_, errorsByRequest[index] = service.AcceptInvitation(ctx, subject, invitation.Token)
		}(index, subject)
	}
	close(start)
	wait.Wait()

	successes := 0
	for _, err := range errorsByRequest {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrInvitationUnavailable) {
			t.Fatalf("unexpected parallel accept error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("parallel accept successes=%d, want 1", successes)
	}
	var acceptedMemberships int
	if err := database.QueryRow(`
		SELECT count(*) FROM household_members
		WHERE household_id = $1::uuid AND role = 'member' AND status = 'active'
	`, household.ID).Scan(&acceptedMemberships); err != nil || acceptedMemberships != 1 {
		t.Fatalf("parallel accept memberships=%d err=%v", acceptedMemberships, err)
	}
}

func TestServiceRejectsMalformedObjectIDsBeforeRepository(t *testing.T) {
	service := NewService(nil)
	ctx := context.Background()
	_, err := service.GetHousehold(ctx, subjectOwner, "not-a-uuid")
	requireError(t, err, ErrNotFound)
	_, err = service.UpdateHousehold(ctx, subjectOwner, "not-a-uuid", "Name")
	requireError(t, err, ErrNotFound)
	_, err = service.ListMembers(ctx, subjectOwner, "not-a-uuid")
	requireError(t, err, ErrNotFound)
	_, err = service.UpdateMember(ctx, subjectOwner, "not-a-uuid", subjectA, UpdateMemberInput{})
	requireError(t, err, ErrNotFound)
	_, err = service.CreateInvitation(ctx, subjectOwner, "not-a-uuid", "member", 3600, "key")
	requireError(t, err, ErrNotFound)
	err = service.RevokeInvitation(ctx, subjectOwner, subjectA, "not-a-uuid")
	requireError(t, err, ErrNotFound)
}
