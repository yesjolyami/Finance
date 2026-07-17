package backupv5

import (
	"strings"

	"github.com/google/uuid"
)

var namespace = uuid.MustParse("8a7193b0-05ae-4b3f-b50d-3065d04c6843")

func deterministicID(householdID uuid.UUID, entityType, legacyComponent string) uuid.UUID {
	name := "finance-backup-v5\x00" + householdID.String() + "\x00" + entityType + "\x00" + legacyComponent
	return uuid.NewSHA1(namespace, []byte(name))
}

func budgetID(householdID uuid.UUID, categoryLegacyID string, month LocalDate) uuid.UUID {
	return deterministicID(householdID, "budget", categoryLegacyID+"\x00"+month.String())
}

func validLegacyID(value string) bool {
	if value == "" || len(value) > MaxLegacyIDBytes || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if char <= 0x1f {
			return false
		}
	}
	return true
}
