package backupv5

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeKeyringFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keyring.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func encodedKey(value byte, size int) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, size))
}

func TestLoadHMACKeyringFileAndRotationRestart(t *testing.T) {
	oldKey := encodedKey(0x11, 32)
	newKey := encodedKey(0x22, 48)
	path := writeKeyringFixture(t, fmt.Sprintf(`{"old":"%s","active":"%s"}`, oldKey, newKey))
	keyring, err := LoadHMACKeyringFile("active", path)
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeConfirmRepository{referenced: []string{"old"}}
	service, err := NewConfirmService(repository, keyring)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AuditKeyring(context.Background()); err != nil {
		t.Fatalf("retained rotation key rejected: %v", err)
	}

	restartedPath := writeKeyringFixture(t, fmt.Sprintf(`{"active":"%s"}`, newKey))
	restarted, err := LoadHMACKeyringFile("active", restartedPath)
	if err != nil {
		t.Fatal(err)
	}
	restartedService, err := NewConfirmService(repository, restarted)
	if err != nil {
		t.Fatal(err)
	}
	if err := restartedService.AuditKeyring(context.Background()); !errors.Is(err, ErrReferencedKeyMissing) {
		t.Fatalf("restart without referenced key error=%v", err)
	}
}

func TestLoadHMACKeyringFileRejectsUnsafeInputWithoutDisclosure(t *testing.T) {
	valid := encodedKey(0x33, 32)
	tooMany := strings.Builder{}
	tooMany.WriteByte('{')
	for index := 0; index < maxHMACKeys+1; index++ {
		if index > 0 {
			tooMany.WriteByte(',')
		}
		fmt.Fprintf(&tooMany, `"key-%02d":"%s"`, index, valid)
	}
	tooMany.WriteByte('}')
	tests := []struct {
		name   string
		body   string
		active string
	}{
		{name: "malformed", body: `{`, active: "active"},
		{name: "root array", body: `[]`, active: "active"},
		{name: "duplicate key", body: fmt.Sprintf(`{"active":"%s","active":"%s"}`, valid, valid), active: "active"},
		{name: "trailing value", body: fmt.Sprintf(`{"active":"%s"} {}`, valid), active: "active"},
		{name: "non string", body: `{"active":true}`, active: "active"},
		{name: "padding", body: fmt.Sprintf(`{"active":"%s="}`, valid), active: "active"},
		{name: "standard base64", body: `{"active":"//////////////////////////////////////////8"}`, active: "active"},
		{name: "short key", body: fmt.Sprintf(`{"active":"%s"}`, encodedKey(1, 31)), active: "active"},
		{name: "invalid key id", body: fmt.Sprintf(`{"bad id":"%s"}`, valid), active: "bad id"},
		{name: "active absent", body: fmt.Sprintf(`{"old":"%s"}`, valid), active: "active"},
		{name: "too many", body: tooMany.String(), active: "key-00"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeKeyringFixture(t, test.body)
			_, err := LoadHMACKeyringFile(test.active, path)
			if !errors.Is(err, ErrKeyringFile) {
				t.Fatalf("error=%v", err)
			}
			for _, secret := range []string{path, test.active, valid} {
				if secret != "" && strings.Contains(err.Error(), secret) {
					t.Fatalf("error disclosed keyring data: %v", err)
				}
			}
		})
	}
}

func TestLoadHMACKeyringFileRejectsOversizeAndNonRegularFiles(t *testing.T) {
	oversize := filepath.Join(t.TempDir(), "oversize.json")
	if err := os.WriteFile(oversize, bytes.Repeat([]byte{'x'}, int(MaximumKeyringFileBytes)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{oversize, t.TempDir(), filepath.Join(t.TempDir(), "missing.json"), "relative.json"} {
		if _, err := LoadHMACKeyringFile("active", path); !errors.Is(err, ErrKeyringFile) {
			t.Fatalf("path type accepted: %v", err)
		}
	}
	target := writeKeyringFixture(t, fmt.Sprintf(`{"active":"%s"}`, encodedKey(1, 32)))
	link := filepath.Join(t.TempDir(), "keyring-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHMACKeyringFile("active", link); !errors.Is(err, ErrKeyringFile) {
		t.Fatalf("symlink accepted: %v", err)
	}
}

func TestLoadHMACKeyringFileRequiresOwnerOnlyPermissions(t *testing.T) {
	valid := fmt.Sprintf(`{"active":"%s"}`, encodedKey(1, 32))
	for _, permissions := range []os.FileMode{0o640, 0o604, 0o644, 0o700, 0o200} {
		t.Run(permissions.String(), func(t *testing.T) {
			path := writeKeyringFixture(t, valid)
			if err := os.Chmod(path, permissions); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadHMACKeyringFile("active", path); !errors.Is(err, ErrKeyringFile) {
				t.Fatalf("unsafe permissions %o accepted: %v", permissions, err)
			}
		})
	}
	for _, permissions := range []os.FileMode{0o400, 0o600} {
		t.Run("accepted-"+permissions.String(), func(t *testing.T) {
			path := writeKeyringFixture(t, valid)
			if err := os.Chmod(path, permissions); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadHMACKeyringFile("active", path); err != nil {
				t.Fatalf("secure permissions %o rejected: %v", permissions, err)
			}
		})
	}
}
