package backupv5

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func FuzzParseNeverPanics(f *testing.F) {
	f.Add([]byte(canonicalFixture))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"version":5,"version":5}`))
	f.Add([]byte{0xff, 0xfe})
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<20 {
			t.Skip()
		}
		_, _ = ParseBytes(context.Background(), raw, fixtureInput)
	})
}

func FuzzDuplicateKeyInvariant(f *testing.F) {
	f.Add("id")
	f.Add("ключ")
	f.Add("")
	f.Fuzz(func(t *testing.T, key string) {
		if len(key) > MaxRawStringBytes {
			t.Skip()
		}
		encoded, err := json.Marshal(key)
		if err != nil {
			t.Skip()
		}
		raw := append([]byte{'{'}, encoded...)
		raw = append(raw, []byte(`:1,`)...)
		raw = append(raw, encoded...)
		raw = append(raw, []byte(`:2}`)...)
		if err := validateRawJSON(context.Background(), raw); err == nil {
			t.Fatalf("duplicate key accepted")
		}
	})
}

func FuzzTrailingAndNumberInvariants(f *testing.F) {
	f.Add("5", " ")
	f.Add("5.0", "{}")
	f.Add("1e2", "x")
	f.Add("-1", "\n\t")
	f.Fuzz(func(t *testing.T, number, suffix string) {
		if len(number)+len(suffix) > 4096 {
			t.Skip()
		}
		rawNumber := []byte(`{"n":` + number + `}`)
		numberErr := validateRawJSON(context.Background(), rawNumber)
		trimmed := strings.TrimSpace(number)
		if isLexicalInteger(trimmed) {
			if numberErr != nil {
				t.Fatalf("lexical integer rejected: %v", numberErr)
			}
		} else if numberErr == nil {
			t.Fatalf("non-integer token accepted")
		}

		trailingErr := validateRawJSON(context.Background(), []byte(`{}`+suffix))
		if strings.TrimSpace(suffix) == "" {
			if trailingErr != nil {
				t.Fatalf("JSON whitespace rejected: %v", trailingErr)
			}
		} else if trailingErr == nil {
			t.Fatalf("non-whitespace trailing bytes accepted")
		}
	})
}
