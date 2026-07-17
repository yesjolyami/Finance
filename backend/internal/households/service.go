package households

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	defaultDisplayName = "Пользователь"
	minimumInviteTTL   = 300
	maximumInviteTTL   = 30 * 24 * 60 * 60
	inviteTokenBytes   = 32
)

type Service struct {
	repository Repository
	random     io.Reader
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, random: rand.Reader}
}

func (service *Service) Bootstrap(ctx context.Context, subject, displayName string) (BootstrapResult, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName != "" && !validText(displayName, 120) {
		return BootstrapResult{}, ErrInvalid
	}
	var optionalDisplayName *string
	if displayName != "" {
		optionalDisplayName = &displayName
	}
	return service.repository.Bootstrap(ctx, subject, optionalDisplayName)
}

func (service *Service) GetMe(ctx context.Context, subject string) (User, error) {
	return service.repository.GetMe(ctx, subject)
}

func (service *Service) ListHouseholds(ctx context.Context, subject string) ([]Household, error) {
	return service.repository.ListHouseholds(ctx, subject)
}

func (service *Service) CreateHousehold(ctx context.Context, subject, name, idempotencyKey string) (Household, error) {
	name = strings.TrimSpace(name)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if !validText(name, 120) || !validText(idempotencyKey, 255) {
		return Household{}, ErrInvalid
	}
	return service.repository.CreateHousehold(ctx, subject, name, idempotencyKey)
}

func (service *Service) GetHousehold(ctx context.Context, subject, householdID string) (Household, error) {
	if !validUUID(householdID) {
		return Household{}, ErrNotFound
	}
	return service.repository.GetHousehold(ctx, subject, householdID)
}

func (service *Service) UpdateHousehold(ctx context.Context, subject, householdID, name string) (Household, error) {
	if !validUUID(householdID) {
		return Household{}, ErrNotFound
	}
	name = strings.TrimSpace(name)
	if !validText(name, 120) {
		return Household{}, ErrInvalid
	}
	return service.repository.UpdateHousehold(ctx, subject, householdID, name)
}

func (service *Service) ListMembers(ctx context.Context, subject, householdID string) ([]Member, error) {
	if !validUUID(householdID) {
		return nil, ErrNotFound
	}
	return service.repository.ListMembers(ctx, subject, householdID)
}

func (service *Service) UpdateMember(ctx context.Context, subject, householdID, userID string, input UpdateMemberInput) (Member, error) {
	if !validUUID(householdID) || !validUUID(userID) {
		return Member{}, ErrNotFound
	}
	if input.Role == nil && input.Status == nil {
		return Member{}, ErrInvalid
	}
	if input.Role != nil {
		role := strings.TrimSpace(*input.Role)
		if role != "owner" && role != "admin" && role != "member" {
			return Member{}, ErrInvalid
		}
		input.Role = &role
	}
	if input.Status != nil {
		status := strings.TrimSpace(*input.Status)
		if status != "active" && status != "removed" {
			return Member{}, ErrInvalid
		}
		input.Status = &status
	}
	return service.repository.UpdateMember(ctx, subject, householdID, userID, input)
}

func (service *Service) CreateInvitation(
	ctx context.Context,
	subject, householdID, role string,
	ttlSeconds int,
	idempotencyKey string,
) (Invitation, error) {
	if !validUUID(householdID) {
		return Invitation{}, ErrNotFound
	}
	role = strings.TrimSpace(role)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if (role != "admin" && role != "member") ||
		ttlSeconds < minimumInviteTTL || ttlSeconds > maximumInviteTTL ||
		!validText(idempotencyKey, 255) {
		return Invitation{}, ErrInvalid
	}

	raw := make([]byte, inviteTokenBytes)
	if _, err := io.ReadFull(service.random, raw); err != nil {
		return Invitation{}, err
	}
	hash := sha256.Sum256(raw)
	invitation, err := service.repository.CreateInvitation(ctx, subject, householdID, CreateInvitationInput{
		Role: role, TTLSeconds: ttlSeconds, IdempotencyKey: idempotencyKey, TokenHash: hash[:],
	})
	if err != nil {
		return Invitation{}, err
	}
	invitation.Token = base64.RawURLEncoding.EncodeToString(raw)
	return invitation, nil
}

func (service *Service) AcceptInvitation(ctx context.Context, subject, rawToken string) (Household, error) {
	rawToken = strings.TrimSpace(rawToken)
	raw, err := base64.RawURLEncoding.DecodeString(rawToken)
	if err != nil || len(raw) != inviteTokenBytes {
		return Household{}, ErrInvitationUnavailable
	}
	hash := sha256.Sum256(raw)
	return service.repository.AcceptInvitation(ctx, subject, hash[:])
}

func (service *Service) RevokeInvitation(ctx context.Context, subject, householdID, invitationID string) error {
	if !validUUID(householdID) || !validUUID(invitationID) {
		return ErrNotFound
	}
	return service.repository.RevokeInvitation(ctx, subject, householdID, invitationID)
}

func validText(value string, maximum int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= maximum
}

func validUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil
}
