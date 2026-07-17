package finance

import (
	"crypto/sha256"
	"encoding/json"
)

type accountCreateFingerprint struct {
	Schema           int     `json:"schema"`
	Name             string  `json:"name"`
	Color            string  `json:"color"`
	SortOrder        int     `json:"sortOrder"`
	AccountType      string  `json:"accountType"`
	BankLabel        string  `json:"bankLabel"`
	LegacyOwnerLabel string  `json:"legacyOwnerLabel"`
	OwnerUserID      *string `json:"ownerUserId"`
}

type categoryCreateFingerprint struct {
	Schema    int    `json:"schema"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	SortOrder int    `json:"sortOrder"`
}

type transactionCreateFingerprint struct {
	Schema              int     `json:"schema"`
	Type                string  `json:"type"`
	AccountID           string  `json:"accountId"`
	ToAccountID         *string `json:"toAccountId"`
	CategoryID          *string `json:"categoryId"`
	AmountCents         string  `json:"amountCents"`
	EventDate           string  `json:"eventDate"`
	Note                string  `json:"note"`
	IsBalanceAdjustment bool    `json:"isBalanceAdjustment"`
}

func accountPayloadHash(value AccountCreate) [sha256.Size]byte {
	var ownerUserID *string
	if value.OwnerUserID != nil {
		text := value.OwnerUserID.String()
		ownerUserID = &text
	}
	return hashTypedPayload(accountCreateFingerprint{
		Schema: 1, Name: value.Name, Color: value.Color, SortOrder: value.SortOrder,
		AccountType: value.AccountType, BankLabel: value.BankLabel,
		LegacyOwnerLabel: value.LegacyOwnerLabel, OwnerUserID: ownerUserID,
	})
}

func categoryPayloadHash(value CategoryCreate) [sha256.Size]byte {
	return hashTypedPayload(categoryCreateFingerprint{
		Schema: 1, Type: value.Type, Name: value.Name, Color: value.Color, SortOrder: value.SortOrder,
	})
}

func transactionPayloadHash(value TransactionValues) [sha256.Size]byte {
	return hashTypedPayload(transactionCreateFingerprint{
		Schema: 1, Type: value.Type, AccountID: value.AccountID.String(),
		ToAccountID: uuidText(value.ToAccountID), CategoryID: uuidText(value.CategoryID),
		AmountCents: formatInt64(value.AmountCents), EventDate: value.EventDate.String(),
		Note: value.Note, IsBalanceAdjustment: value.IsBalanceAdjustment,
	})
}

func hashTypedPayload(value any) [sha256.Size]byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic("finance fingerprint contains an unsupported value: " + err.Error())
	}
	return sha256.Sum256(encoded)
}
