package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDatabaseURLFile(t *testing.T, value string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "database-url")
	if err := os.WriteFile(path, []byte(value), mode); err != nil {
		t.Fatalf("write database credential: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod database credential: %v", err)
	}
	return path
}

func TestLoadDatabaseURLFile(t *testing.T) {
	secret := "postgresql://finance:do-not-log@db.internal/finance?sslmode=verify-full"
	path := writeDatabaseURLFile(t, secret+"\n", 0o400)
	got, err := loadDatabaseURLFile(path)
	if err != nil {
		t.Fatalf("loadDatabaseURLFile() error = %v", err)
	}
	if got != secret {
		t.Fatal("database URL file value changed")
	}
}

func TestLoadDatabaseURLFileRejectsUnsafeFilesAndContent(t *testing.T) {
	valid := "postgresql://finance:secret@127.0.0.1/finance?sslmode=disable"
	tests := []struct {
		name    string
		content string
		mode    os.FileMode
		mutate  func(*testing.T, string) string
	}{
		{name: "empty", mode: 0o400},
		{name: "too open", content: valid, mode: 0o640},
		{name: "outer whitespace", content: " " + valid, mode: 0o400},
		{name: "multiple lines", content: valid + "\nsecond", mode: 0o400},
		{name: "carriage return", content: valid + "\r\n", mode: 0o400},
		{name: "nul", content: valid + "\x00", mode: 0o400},
		{name: "invalid utf8", content: string([]byte{0xff}), mode: 0o400},
		{
			name:    "symlink",
			content: valid,
			mode:    0o400,
			mutate: func(t *testing.T, target string) string {
				t.Helper()
				link := filepath.Join(t.TempDir(), "database-url-link")
				if err := os.Symlink(target, link); err != nil {
					t.Fatalf("create symlink: %v", err)
				}
				return link
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeDatabaseURLFile(t, test.content, test.mode)
			if test.mutate != nil {
				path = test.mutate(t, path)
			}
			if _, err := loadDatabaseURLFile(path); !errors.Is(err, errDatabaseURLFile) {
				t.Fatalf("unsafe credential accepted: %v", err)
			}
		})
	}
}

func TestLoadDatabaseURLFileIsBoundedAndRedacted(t *testing.T) {
	path := writeDatabaseURLFile(t, strings.Repeat("x", int(maximumDatabaseURLFileBytes)+1), 0o400)
	_, err := loadDatabaseURLFile(path)
	if !errors.Is(err, errDatabaseURLFile) {
		t.Fatalf("oversized credential error = %v", err)
	}
	rendered := fmt.Sprintf("%v", err)
	if strings.Contains(rendered, path) || strings.Contains(rendered, "postgres") {
		t.Fatalf("credential error leaked sensitive context: %q", rendered)
	}
}
