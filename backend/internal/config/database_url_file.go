package config

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"unicode/utf8"
)

const maximumDatabaseURLFileBytes int64 = 8 * 1024

var errDatabaseURLFile = errors.New("DATABASE_URL_FILE is invalid")

func loadDatabaseURLFile(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errDatabaseURLFile
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || !secureCredentialFileInfo(pathInfo) || pathInfo.Size() > maximumDatabaseURLFileBytes {
		return "", errDatabaseURLFile
	}
	fileDescriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", errDatabaseURLFile
	}
	file := os.NewFile(uintptr(fileDescriptor), "")
	if file == nil {
		_ = syscall.Close(fileDescriptor)
		return "", errDatabaseURLFile
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil || !secureCredentialFileInfo(fileInfo) || !os.SameFile(pathInfo, fileInfo) ||
		fileInfo.Size() > maximumDatabaseURLFileBytes {
		return "", errDatabaseURLFile
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumDatabaseURLFileBytes+1))
	if err != nil || int64(len(raw)) > maximumDatabaseURLFileBytes {
		clear(raw)
		return "", errDatabaseURLFile
	}
	defer clear(raw)

	if len(raw) > 0 && raw[len(raw)-1] == '\n' {
		raw = raw[:len(raw)-1]
	}
	if len(raw) == 0 || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 ||
		bytes.IndexAny(raw, "\r\n") >= 0 || !bytes.Equal(bytes.TrimSpace(raw), raw) {
		return "", errDatabaseURLFile
	}
	return string(raw), nil
}

func secureCredentialFileInfo(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() {
		return false
	}
	permissions := info.Mode().Perm()
	if permissions != 0o400 && permissions != 0o600 {
		return false
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid != uint32(os.Geteuid()) {
		return false
	}
	return true
}
