package households

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type PostgresRepository struct {
	database *sql.DB
}

func NewPostgresRepository(database *sql.DB) *PostgresRepository {
	return &PostgresRepository{database: database}
}

func (repository *PostgresRepository) Bootstrap(
	ctx context.Context,
	subject string,
	displayName *string,
) (BootstrapResult, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return BootstrapResult{}, err
	}
	defer transaction.Rollback()

	var user User
	err = transaction.QueryRowContext(ctx, `
		INSERT INTO users (id, auth_subject, display_name)
		VALUES ($1::uuid, $1, COALESCE($2, $3))
		ON CONFLICT (auth_subject) DO UPDATE
		SET display_name = COALESCE($2, users.display_name),
		    updated_at = CURRENT_TIMESTAMP
		WHERE users.deleted_at IS NULL
		RETURNING id::text, display_name
	`, subject, displayName, defaultDisplayName).Scan(&user.ID, &user.DisplayName)
	if errors.Is(err, sql.ErrNoRows) {
		return BootstrapResult{}, ErrNotFound
	}
	if err != nil {
		return BootstrapResult{}, err
	}

	households, err := listHouseholds(ctx, transaction, subject)
	if err != nil {
		return BootstrapResult{}, err
	}
	if err := transaction.Commit(); err != nil {
		return BootstrapResult{}, err
	}
	return BootstrapResult{User: user, Households: households}, nil
}

func (repository *PostgresRepository) GetMe(ctx context.Context, subject string) (User, error) {
	var user User
	err := repository.database.QueryRowContext(ctx, `
		SELECT id::text, display_name
		FROM users
		WHERE auth_subject = $1 AND deleted_at IS NULL
	`, subject).Scan(&user.ID, &user.DisplayName)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (repository *PostgresRepository) ListHouseholds(ctx context.Context, subject string) ([]Household, error) {
	return listHouseholds(ctx, repository.database, subject)
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listHouseholds(ctx context.Context, queryer queryer, subject string) ([]Household, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT h.id::text, h.name, h.currency_code, m.role
		FROM users u
		JOIN household_members m
		  ON m.user_id = u.id AND m.status = 'active'
		JOIN households h
		  ON h.id = m.household_id AND h.deleted_at IS NULL AND h.archived_at IS NULL
		WHERE u.auth_subject = $1 AND u.deleted_at IS NULL
		ORDER BY h.created_at, h.id
	`, subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Household, 0)
	for rows.Next() {
		var household Household
		if err := rows.Scan(&household.ID, &household.Name, &household.CurrencyCode, &household.Role); err != nil {
			return nil, err
		}
		result = append(result, household)
	}
	return result, rows.Err()
}

func (repository *PostgresRepository) CreateHousehold(
	ctx context.Context,
	subject, name, idempotencyKey string,
) (Household, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return Household{}, err
	}
	defer transaction.Rollback()

	var household Household
	var actorID string
	err = transaction.QueryRowContext(ctx, `
		WITH actor AS (
			SELECT id
			FROM users
			WHERE auth_subject = $1 AND deleted_at IS NULL
		), inserted AS (
			INSERT INTO households (
				id, name, created_by_user_id, creation_idempotency_key
			)
			SELECT gen_random_uuid(), $2, actor.id, $3
			FROM actor
			ON CONFLICT (created_by_user_id, creation_idempotency_key)
			WHERE creation_idempotency_key IS NOT NULL
			DO NOTHING
			RETURNING id, name, currency_code, created_by_user_id
		)
		SELECT id::text, name, currency_code, created_by_user_id::text
		FROM inserted
	`, subject, name, idempotencyKey).Scan(
		&household.ID, &household.Name, &household.CurrencyCode, &actorID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		err = transaction.QueryRowContext(ctx, `
			SELECT h.id::text, h.name, h.currency_code, u.id::text, m.role
			FROM users u
			JOIN households h
			  ON h.created_by_user_id = u.id
			 AND h.creation_idempotency_key = $2
			 AND h.deleted_at IS NULL
			 AND h.archived_at IS NULL
			JOIN household_members m
			  ON m.household_id = h.id AND m.user_id = u.id AND m.status = 'active'
			WHERE u.auth_subject = $1 AND u.deleted_at IS NULL
		`, subject, idempotencyKey).Scan(
			&household.ID, &household.Name, &household.CurrencyCode, &actorID, &household.Role,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return Household{}, ErrNotFound
		}
		if err != nil {
			return Household{}, err
		}
		if household.Name != name {
			return Household{}, ErrConflict
		}
		if err := transaction.Commit(); err != nil {
			return Household{}, err
		}
		return household, nil
	}
	if err != nil {
		return Household{}, err
	}

	var membershipID string
	err = transaction.QueryRowContext(ctx, `
		INSERT INTO household_members (id, household_id, user_id, role)
		VALUES (gen_random_uuid(), $1::uuid, $2::uuid, 'owner')
		RETURNING id::text
	`, household.ID, actorID).Scan(&membershipID)
	if err != nil {
		return Household{}, err
	}
	if err := insertAudit(ctx, transaction, household.ID, actorID, "households", household.ID, "created", map[string]any{
		"name": name,
	}); err != nil {
		return Household{}, err
	}
	if err := insertAudit(ctx, transaction, household.ID, actorID, "household_members", membershipID, "created", map[string]any{
		"role": "owner",
	}); err != nil {
		return Household{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Household{}, err
	}
	household.Role = "owner"
	return household, nil
}

func (repository *PostgresRepository) GetHousehold(
	ctx context.Context,
	subject, householdID string,
) (Household, error) {
	var household Household
	err := repository.database.QueryRowContext(ctx, `
		SELECT h.id::text, h.name, h.currency_code, actor.role
		FROM households h
		JOIN users u ON u.auth_subject = $1 AND u.deleted_at IS NULL
		JOIN household_members actor
		  ON actor.household_id = h.id
		 AND actor.user_id = u.id
		 AND actor.status = 'active'
		WHERE h.id = $2::uuid AND h.deleted_at IS NULL AND h.archived_at IS NULL
	`, subject, householdID).Scan(&household.ID, &household.Name, &household.CurrencyCode, &household.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return Household{}, ErrNotFound
	}
	return household, err
}

func (repository *PostgresRepository) UpdateHousehold(
	ctx context.Context,
	subject, householdID, name string,
) (Household, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return Household{}, err
	}
	defer transaction.Rollback()

	actorID, actorRole, _, err := lockHouseholdForActor(ctx, transaction, subject, householdID)
	if err != nil {
		return Household{}, err
	}
	if actorRole != "owner" && actorRole != "admin" {
		return Household{}, ErrForbidden
	}

	var household Household
	err = transaction.QueryRowContext(ctx, `
		UPDATE households
		SET name = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1::uuid
		RETURNING id::text, name, currency_code
	`, householdID, name).Scan(&household.ID, &household.Name, &household.CurrencyCode)
	if err != nil {
		return Household{}, err
	}
	household.Role = actorRole
	if err := insertAudit(ctx, transaction, householdID, actorID, "households", householdID, "updated", map[string]any{
		"name": name,
	}); err != nil {
		return Household{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Household{}, err
	}
	return household, nil
}

func (repository *PostgresRepository) ListMembers(
	ctx context.Context,
	subject, householdID string,
) ([]Member, error) {
	rows, err := repository.database.QueryContext(ctx, `
		SELECT target.user_id::text, profile.display_name, target.role, target.status,
		       target.joined_at, target.removed_at
		FROM household_members target
		JOIN users profile ON profile.id = target.user_id
		WHERE target.household_id = $2::uuid
		  AND EXISTS (
			SELECT 1
			FROM users actor_user
			JOIN household_members actor
			  ON actor.user_id = actor_user.id
			 AND actor.household_id = target.household_id
			 AND actor.status = 'active'
			WHERE actor_user.auth_subject = $1 AND actor_user.deleted_at IS NULL
		  )
		ORDER BY target.joined_at, target.user_id
	`, subject, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := make([]Member, 0)
	for rows.Next() {
		var member Member
		if err := rows.Scan(
			&member.UserID, &member.DisplayName, &member.Role, &member.Status,
			&member.JoinedAt, &member.RemovedAt,
		); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, ErrNotFound
	}
	return members, nil
}

func (repository *PostgresRepository) UpdateMember(
	ctx context.Context,
	subject, householdID, userID string,
	input UpdateMemberInput,
) (Member, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return Member{}, err
	}
	defer transaction.Rollback()

	actorID, actorRole, _, err := lockHouseholdForActor(ctx, transaction, subject, householdID)
	if err != nil {
		return Member{}, err
	}
	if actorRole == "member" {
		return Member{}, ErrForbidden
	}

	var target Member
	var membershipID string
	err = transaction.QueryRowContext(ctx, `
		SELECT m.id::text, m.user_id::text, u.display_name, m.role, m.status,
		       m.joined_at, m.removed_at
		FROM household_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.household_id = $1::uuid AND m.user_id = $2::uuid
		FOR UPDATE OF m
	`, householdID, userID).Scan(
		&membershipID, &target.UserID, &target.DisplayName, &target.Role, &target.Status,
		&target.JoinedAt, &target.RemovedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, ErrNotFound
	}
	if err != nil {
		return Member{}, err
	}

	newRole := target.Role
	newStatus := target.Status
	if input.Role != nil {
		newRole = *input.Role
	}
	if input.Status != nil {
		newStatus = *input.Status
	}
	if actorRole == "admin" && (target.Role == "owner" || newRole == "owner") {
		return Member{}, ErrForbidden
	}
	if newRole == "owner" && newStatus != "active" {
		return Member{}, ErrConflict
	}

	removesActiveOwner := target.Role == "owner" && target.Status == "active" &&
		(newRole != "owner" || newStatus != "active")
	if removesActiveOwner {
		var ownerCount int
		if err := transaction.QueryRowContext(ctx, `
			SELECT count(*)
			FROM household_members owner_membership
			JOIN users owner_user
			  ON owner_user.id = owner_membership.user_id
			 AND owner_user.deleted_at IS NULL
			WHERE owner_membership.household_id = $1::uuid
			  AND owner_membership.role = 'owner'
			  AND owner_membership.status = 'active'
		`, householdID).Scan(&ownerCount); err != nil {
			return Member{}, err
		}
		if ownerCount <= 1 {
			return Member{}, ErrConflict
		}
	}

	if target.Role == newRole && target.Status == newStatus {
		if err := transaction.Commit(); err != nil {
			return Member{}, err
		}
		return target, nil
	}

	err = transaction.QueryRowContext(ctx, `
		UPDATE household_members
		SET role = $3,
		    status = $4,
		    removed_at = CASE WHEN $4 = 'removed' THEN CURRENT_TIMESTAMP ELSE NULL END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE household_id = $1::uuid AND user_id = $2::uuid
		RETURNING user_id::text, role, status, joined_at, removed_at
	`, householdID, userID, newRole, newStatus).Scan(
		&target.UserID, &target.Role, &target.Status, &target.JoinedAt, &target.RemovedAt,
	)
	if err != nil {
		return Member{}, err
	}
	if err := insertAudit(ctx, transaction, householdID, actorID, "household_members", membershipID, "updated", map[string]any{
		"role": target.Role, "status": target.Status,
	}); err != nil {
		return Member{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Member{}, err
	}
	return target, nil
}

func (repository *PostgresRepository) CreateInvitation(
	ctx context.Context,
	subject, householdID string,
	input CreateInvitationInput,
) (Invitation, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return Invitation{}, err
	}
	defer transaction.Rollback()

	actorID, actorRole, _, err := lockHouseholdForActor(ctx, transaction, subject, householdID)
	if err != nil {
		return Invitation{}, err
	}
	if actorRole != "owner" && actorRole != "admin" {
		return Invitation{}, ErrForbidden
	}

	var invitation Invitation
	err = transaction.QueryRowContext(ctx, `
		INSERT INTO household_invitations (
			id, household_id, role, token_hash, request_idempotency_key,
			invited_by_user_id, ttl_seconds, expires_at
		)
		VALUES (
			gen_random_uuid(), $1::uuid, $2, $3, $4, $5::uuid, $6::integer,
			CURRENT_TIMESTAMP + make_interval(secs => ($6::integer)::double precision)
		)
		ON CONFLICT (household_id, invited_by_user_id, request_idempotency_key)
		DO NOTHING
		RETURNING id::text, household_id::text, role, expires_at
	`, householdID, input.Role, input.TokenHash, input.IdempotencyKey, actorID, input.TTLSeconds).Scan(
		&invitation.ID, &invitation.HouseholdID, &invitation.Role, &invitation.ExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		var existingRole string
		var existingTTL int
		err = transaction.QueryRowContext(ctx, `
			SELECT role, ttl_seconds
			FROM household_invitations
			WHERE household_id = $1::uuid
			  AND invited_by_user_id = $2::uuid
			  AND request_idempotency_key = $3
		`, householdID, actorID, input.IdempotencyKey).Scan(&existingRole, &existingTTL)
		if err != nil {
			return Invitation{}, err
		}
		if existingRole != input.Role || existingTTL != input.TTLSeconds {
			return Invitation{}, ErrConflict
		}
		return Invitation{}, ErrIdempotencyReplay
	}
	if err != nil {
		return Invitation{}, err
	}
	if err := insertAudit(ctx, transaction, householdID, actorID, "household_invitations", invitation.ID, "created", map[string]any{
		"role": input.Role, "expiresAt": invitation.ExpiresAt,
	}); err != nil {
		return Invitation{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Invitation{}, err
	}
	return invitation, nil
}

func (repository *PostgresRepository) AcceptInvitation(
	ctx context.Context,
	subject string,
	tokenHash []byte,
) (Household, error) {
	var householdID string
	err := repository.database.QueryRowContext(ctx, `
		SELECT household_id::text
		FROM household_invitations
		WHERE token_hash = $1
	`, tokenHash).Scan(&householdID)
	if errors.Is(err, sql.ErrNoRows) {
		return Household{}, ErrInvitationUnavailable
	}
	if err != nil {
		return Household{}, err
	}

	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return Household{}, err
	}
	defer transaction.Rollback()

	var household Household
	err = transaction.QueryRowContext(ctx, `
		SELECT id::text, name, currency_code
		FROM households
		WHERE id = $1::uuid AND deleted_at IS NULL AND archived_at IS NULL
		FOR UPDATE
	`, householdID).Scan(&household.ID, &household.Name, &household.CurrencyCode)
	if errors.Is(err, sql.ErrNoRows) {
		return Household{}, ErrInvitationUnavailable
	}
	if err != nil {
		return Household{}, err
	}

	var invitationID, role string
	var expiresAt, databaseNow time.Time
	var acceptedAt, revokedAt *time.Time
	err = transaction.QueryRowContext(ctx, `
		SELECT id::text, role, expires_at, accepted_at, revoked_at, clock_timestamp()
		FROM household_invitations
		WHERE household_id = $1::uuid AND token_hash = $2
		FOR UPDATE
	`, householdID, tokenHash).Scan(
		&invitationID, &role, &expiresAt, &acceptedAt, &revokedAt, &databaseNow,
	)
	if err != nil {
		return Household{}, ErrInvitationUnavailable
	}
	if acceptedAt != nil || revokedAt != nil || databaseNow.After(expiresAt) {
		return Household{}, ErrInvitationUnavailable
	}

	var actorID string
	err = transaction.QueryRowContext(ctx, `
		SELECT id::text
		FROM users
		WHERE auth_subject = $1 AND deleted_at IS NULL
	`, subject).Scan(&actorID)
	if errors.Is(err, sql.ErrNoRows) {
		return Household{}, ErrNotFound
	}
	if err != nil {
		return Household{}, err
	}

	var membershipID, membershipRole, membershipStatus string
	membershipAction := "created"
	err = transaction.QueryRowContext(ctx, `
		SELECT id::text, role, status
		FROM household_members
		WHERE household_id = $1::uuid AND user_id = $2::uuid
		FOR UPDATE
	`, householdID, actorID).Scan(&membershipID, &membershipRole, &membershipStatus)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		err = transaction.QueryRowContext(ctx, `
			INSERT INTO household_members (id, household_id, user_id, role)
			VALUES (gen_random_uuid(), $1::uuid, $2::uuid, $3)
			RETURNING id::text
		`, householdID, actorID, role).Scan(&membershipID)
	case err != nil:
		return Household{}, err
	case membershipStatus != "removed" || membershipRole == "owner":
		return Household{}, ErrConflict
	default:
		membershipAction = "updated"
		_, err = transaction.ExecContext(ctx, `
			UPDATE household_members
			SET role = $3,
			    status = 'active',
			    removed_at = NULL,
			    updated_at = clock_timestamp()
			WHERE household_id = $1::uuid AND user_id = $2::uuid
		`, householdID, actorID, role)
	}
	if err != nil {
		return Household{}, err
	}

	result, err := transaction.ExecContext(ctx, `
		UPDATE household_invitations
		SET accepted_at = clock_timestamp(),
		    accepted_by_user_id = $2::uuid
		WHERE id = $1::uuid
		  AND accepted_at IS NULL
		  AND revoked_at IS NULL
		  AND expires_at >= clock_timestamp()
	`, invitationID, actorID)
	if err != nil {
		return Household{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Household{}, ErrInvitationUnavailable
	}
	if err := insertAudit(ctx, transaction, householdID, actorID, "household_invitations", invitationID, "updated", map[string]any{
		"status": "accepted",
	}); err != nil {
		return Household{}, err
	}
	if err := insertAudit(ctx, transaction, householdID, actorID, "household_members", membershipID, membershipAction, map[string]any{
		"role": role,
	}); err != nil {
		return Household{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Household{}, err
	}
	household.Role = role
	return household, nil
}

func (repository *PostgresRepository) RevokeInvitation(
	ctx context.Context,
	subject, householdID, invitationID string,
) error {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()

	actorID, actorRole, _, err := lockHouseholdForActor(ctx, transaction, subject, householdID)
	if err != nil {
		return err
	}
	if actorRole != "owner" && actorRole != "admin" {
		return ErrForbidden
	}

	var acceptedAt, revokedAt *time.Time
	err = transaction.QueryRowContext(ctx, `
		SELECT accepted_at, revoked_at
		FROM household_invitations
		WHERE id = $1::uuid AND household_id = $2::uuid
		FOR UPDATE
	`, invitationID, householdID).Scan(&acceptedAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if acceptedAt != nil {
		return ErrConflict
	}
	if revokedAt != nil {
		return transaction.Commit()
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE household_invitations
		SET revoked_at = clock_timestamp()
		WHERE id = $1::uuid AND household_id = $2::uuid
	`, invitationID, householdID); err != nil {
		return err
	}
	if err := insertAudit(ctx, transaction, householdID, actorID, "household_invitations", invitationID, "updated", map[string]any{
		"status": "revoked",
	}); err != nil {
		return err
	}
	return transaction.Commit()
}

func lockHouseholdForActor(
	ctx context.Context,
	transaction *sql.Tx,
	subject, householdID string,
) (actorID, actorRole, householdName string, err error) {
	err = transaction.QueryRowContext(ctx, `
		SELECT u.id::text, actor.role, h.name
		FROM households h
		JOIN users u ON u.auth_subject = $1 AND u.deleted_at IS NULL
		JOIN household_members actor
		  ON actor.household_id = h.id
		 AND actor.user_id = u.id
		 AND actor.status = 'active'
		WHERE h.id = $2::uuid AND h.deleted_at IS NULL AND h.archived_at IS NULL
		FOR UPDATE OF h
	`, subject, householdID).Scan(&actorID, &actorRole, &householdName)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func insertAudit(
	ctx context.Context,
	transaction *sql.Tx,
	householdID, actorID, entityType, entityID, action string,
	changes map[string]any,
) error {
	serialized, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("encode audit changes: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO audit_log (
			id, household_id, actor_user_id, entity_type, entity_id, action, changes
		)
		VALUES (gen_random_uuid(), $1::uuid, $2::uuid, $3, $4::uuid, $5, $6::jsonb)
	`, householdID, actorID, entityType, entityID, action, string(serialized))
	return err
}
