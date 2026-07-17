package backupv5

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const MaximumKeyringFileBytes int64 = 64 * 1024

var ErrKeyringFile = errors.New("backup v5 HMAC keyring file invalid")

// LoadHMACKeyringFile loads a bounded, strict JSON object whose values are
// canonical unpadded base64url strings. It never includes the path, key IDs, or
// file contents in returned errors.
func LoadHMACKeyringFile(activeKeyID, path string) (*HMACKeyring, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrKeyringFile
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || !secureKeyringFileInfo(pathInfo) || pathInfo.Size() > MaximumKeyringFileBytes {
		return nil, ErrKeyringFile
	}
	fileDescriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrKeyringFile
	}
	file := os.NewFile(uintptr(fileDescriptor), "")
	if file == nil {
		_ = syscall.Close(fileDescriptor)
		return nil, ErrKeyringFile
	}
	defer file.Close()
	fileInfo, err := file.Stat()
	if err != nil || !secureKeyringFileInfo(fileInfo) || !os.SameFile(pathInfo, fileInfo) ||
		fileInfo.Size() > MaximumKeyringFileBytes {
		return nil, ErrKeyringFile
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaximumKeyringFileBytes+1))
	if err != nil || int64(len(raw)) > MaximumKeyringFileBytes {
		clear(raw)
		return nil, ErrKeyringFile
	}
	defer clear(raw)

	source, err := decodeKeyringObject(raw)
	if err != nil {
		return nil, ErrKeyringFile
	}
	defer func() {
		for _, key := range source {
			clear(key)
		}
	}()
	keyring, err := NewHMACKeyring(activeKeyID, source)
	if err != nil {
		return nil, ErrKeyringFile
	}
	return keyring, nil
}

func secureKeyringFileInfo(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() {
		return false
	}
	permissions := info.Mode().Perm()
	if permissions&0o400 == 0 || permissions&^os.FileMode(0o600) != 0 {
		return false
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid != uint32(os.Geteuid()) {
		return false
	}
	return true
}

func decodeKeyringObject(raw []byte) (map[string][]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrKeyringFile
	}
	keys := make(map[string][]byte)
	for decoder.More() {
		keyToken, err := decoder.Token()
		keyID, ok := keyToken.(string)
		if err != nil || !ok {
			clearKeySource(keys)
			return nil, ErrKeyringFile
		}
		if _, duplicate := keys[keyID]; duplicate || len(keys) >= maxHMACKeys {
			clearKeySource(keys)
			return nil, ErrKeyringFile
		}
		var encoded string
		if err := decoder.Decode(&encoded); err != nil || encoded == "" ||
			strings.ContainsRune(encoded, '=') {
			clearKeySource(keys)
			return nil, ErrKeyringFile
		}
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || len(decoded) < 32 || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
			clear(decoded)
			clearKeySource(keys)
			return nil, ErrKeyringFile
		}
		keys[keyID] = decoded
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		clearKeySource(keys)
		return nil, ErrKeyringFile
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		clearKeySource(keys)
		return nil, ErrKeyringFile
	}
	return keys, nil
}

func clearKeySource(source map[string][]byte) {
	for _, key := range source {
		clear(key)
	}
}
