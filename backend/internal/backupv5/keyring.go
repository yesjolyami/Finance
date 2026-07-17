package backupv5

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"regexp"
	"sort"
)

var hmacKeyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type HMACKeyring struct {
	activeID string
	keyIDs   []string
	keys     map[string][]byte
}

func NewHMACKeyring(activeID string, source map[string][]byte) (*HMACKeyring, error) {
	if !hmacKeyIDPattern.MatchString(activeID) || len(source) == 0 || len(source) > maxHMACKeys {
		return nil, ErrInvalidKeyring
	}
	keys := make(map[string][]byte, len(source))
	keyIDs := make([]string, 0, len(source))
	for keyID, raw := range source {
		if !hmacKeyIDPattern.MatchString(keyID) || len(raw) < 32 {
			return nil, ErrInvalidKeyring
		}
		keys[keyID] = append([]byte(nil), raw...)
		keyIDs = append(keyIDs, keyID)
	}
	if _, exists := keys[activeID]; !exists {
		return nil, ErrInvalidKeyring
	}
	sort.Strings(keyIDs)
	return &HMACKeyring{activeID: activeID, keyIDs: keyIDs, keys: keys}, nil
}

func (HMACKeyring) String() string { return "backupv5.HMACKeyring[redacted]" }

func (HMACKeyring) GoString() string { return "backupv5.HMACKeyring[redacted]" }

func (HMACKeyring) MarshalJSON() ([]byte, error) {
	return nil, ErrInvalidKeyring
}

func (keyring *HMACKeyring) candidates(idempotencyKey string, digest [32]byte, budgetMonth string) []HMACCandidate {
	candidates := make([]HMACCandidate, 0, len(keyring.keyIDs))
	for _, keyID := range keyring.keyIDs {
		key := keyring.keys[keyID]
		candidates = append(candidates, HMACCandidate{
			KeyID:       keyID,
			Lookup:      hmacSHA256(key, []byte(idempotencyKey)),
			Fingerprint: requestFingerprint(key, digest, budgetMonth),
		})
	}
	return candidates
}

func (keyring *HMACKeyring) active(candidates []HMACCandidate) (HMACCandidate, error) {
	for _, candidate := range candidates {
		if candidate.KeyID == keyring.activeID {
			return candidate, nil
		}
	}
	return HMACCandidate{}, ErrInvalidKeyring
}

func (keyring *HMACKeyring) validateReferenced(keyIDs []string) error {
	for _, keyID := range keyIDs {
		if _, exists := keyring.keys[keyID]; !exists {
			return ErrReferencedKeyMissing
		}
	}
	return nil
}

func hmacSHA256(key, payload []byte) [32]byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}

func requestFingerprint(key []byte, digest [32]byte, budgetMonth string) [32]byte {
	mac := hmac.New(sha256.New, key)
	writeFramed := func(value []byte) {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(value)))
		_, _ = mac.Write(length[:])
		_, _ = mac.Write(value)
	}
	writeFramed(digest[:])
	writeFramed([]byte(budgetMonth))
	writeFramed([]byte(PolicyVersion))
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
