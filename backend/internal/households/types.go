package households

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound              = errors.New("resource not found")
	ErrForbidden             = errors.New("operation forbidden")
	ErrConflict              = errors.New("operation conflicts with current state")
	ErrIdempotencyReplay     = errors.New("idempotent invitation already created")
	ErrInvalid               = errors.New("invalid input")
	ErrInvitationUnavailable = errors.New("invitation unavailable")
)

type User struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type Household struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CurrencyCode string `json:"currencyCode"`
	Role         string `json:"role"`
}

type Member struct {
	UserID      string     `json:"userId"`
	DisplayName string     `json:"displayName"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	JoinedAt    time.Time  `json:"joinedAt"`
	RemovedAt   *time.Time `json:"removedAt,omitempty"`
}

type BootstrapResult struct {
	User       User        `json:"user"`
	Households []Household `json:"households"`
}

type Invitation struct {
	ID          string    `json:"id"`
	HouseholdID string    `json:"householdId"`
	Role        string    `json:"role"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Token       string    `json:"token,omitempty"`
}

type UpdateMemberInput struct {
	Role   *string
	Status *string
}

type CreateInvitationInput struct {
	Role           string
	TTLSeconds     int
	IdempotencyKey string
	TokenHash      []byte
}

type Repository interface {
	Bootstrap(context.Context, string, *string) (BootstrapResult, error)
	GetMe(context.Context, string) (User, error)
	ListHouseholds(context.Context, string) ([]Household, error)
	CreateHousehold(context.Context, string, string, string) (Household, error)
	GetHousehold(context.Context, string, string) (Household, error)
	UpdateHousehold(context.Context, string, string, string) (Household, error)
	ListMembers(context.Context, string, string) ([]Member, error)
	UpdateMember(context.Context, string, string, string, UpdateMemberInput) (Member, error)
	CreateInvitation(context.Context, string, string, CreateInvitationInput) (Invitation, error)
	AcceptInvitation(context.Context, string, []byte) (Household, error)
	RevokeInvitation(context.Context, string, string, string) error
}
