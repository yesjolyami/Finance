package finance

import (
	"strconv"

	"github.com/google/uuid"
)

func uuidText(value *uuid.UUID) *string {
	if value == nil {
		return nil
	}
	text := value.String()
	return &text
}

func formatInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
