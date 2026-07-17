package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

const (
	maxCachedKeys    = 64
	maxNegativeKids  = 256
	maxKeyIDLength   = 128
	defaultTokenSize = 16 << 10
	defaultJWKSSize  = 1 << 20
	minTokenSize     = 1 << 10
	maxTokenSize     = 64 << 10
	minJWKSSize      = 1 << 10
	maxJWKSSize      = 4 << 20
)

var allowedAlgorithms = []string{"RS256", "ES256"}

type VerifierConfig struct {
	Issuer          string
	Audience        string
	JWKSURL         string
	CacheTTL        time.Duration
	RefreshCooldown time.Duration
	ClockSkew       time.Duration
	HTTPTimeout     time.Duration
	MaxTokenBytes   int
	MaxJWKSBytes    int64
}

type RemoteVerifier struct {
	config VerifierConfig
	client *http.Client
	now    func() time.Time

	mu                 sync.RWMutex
	keys               map[string]verificationKey
	expiresAt          time.Time
	nextRefreshAllowed time.Time
	negativeKids       map[string]time.Time
	refreshGroup       singleflight.Group
}

type verificationKey struct {
	algorithm string
	publicKey any
}

type jwksDocument struct {
	Keys []jsonWebKey `json:"keys"`
}

type jsonWebKey struct {
	KeyID       string          `json:"kid"`
	Algorithm   string          `json:"alg"`
	KeyType     string          `json:"kty"`
	Use         string          `json:"use"`
	KeyOps      []string        `json:"key_ops"`
	Curve       string          `json:"crv"`
	Modulus     string          `json:"n"`
	Exponent    string          `json:"e"`
	X           string          `json:"x"`
	Y           string          `json:"y"`
	SymmetricK  string          `json:"k"`
	PrivateD    string          `json:"d"`
	PrimeP      string          `json:"p"`
	PrimeQ      string          `json:"q"`
	ExponentDP  string          `json:"dp"`
	ExponentDQ  string          `json:"dq"`
	Coefficient string          `json:"qi"`
	OtherPrimes json.RawMessage `json:"oth,omitempty"`
}

func NewRemoteVerifier(config VerifierConfig) (*RemoteVerifier, error) {
	if config.MaxTokenBytes == 0 {
		config.MaxTokenBytes = defaultTokenSize
	}
	if config.MaxJWKSBytes == 0 {
		config.MaxJWKSBytes = defaultJWKSSize
	}
	if err := validateVerifierConfig(config); err != nil {
		return nil, err
	}

	return &RemoteVerifier{
		config: config,
		client: &http.Client{
			Timeout: config.HTTPTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now:          time.Now,
		keys:         make(map[string]verificationKey),
		negativeKids: make(map[string]time.Time),
	}, nil
}

func validateVerifierConfig(config VerifierConfig) error {
	if config.Issuer == "" || config.Audience == "" || config.JWKSURL == "" {
		return errors.New("issuer, audience and JWKS URL are required")
	}
	if _, err := url.ParseRequestURI(config.Issuer); err != nil {
		return errors.New("issuer must be a valid URL")
	}
	if _, err := url.ParseRequestURI(config.JWKSURL); err != nil {
		return errors.New("JWKS URL must be a valid URL")
	}
	if config.CacheTTL <= 0 || config.CacheTTL > 10*time.Minute {
		return errors.New("JWKS cache TTL must be between zero and ten minutes")
	}
	if config.RefreshCooldown < time.Second || config.RefreshCooldown > time.Minute || config.CacheTTL < config.RefreshCooldown {
		return errors.New("JWKS refresh cooldown must be between one second and one minute and not exceed cache TTL")
	}
	if config.ClockSkew < 0 || config.ClockSkew > 2*time.Minute {
		return errors.New("clock skew must be between zero and two minutes")
	}
	if config.HTTPTimeout <= 0 || config.HTTPTimeout > 10*time.Second {
		return errors.New("JWKS HTTP timeout must be between zero and ten seconds")
	}
	if config.MaxTokenBytes < minTokenSize || config.MaxTokenBytes > maxTokenSize {
		return errors.New("maximum JWT size is outside the safe range")
	}
	if config.MaxJWKSBytes < minJWKSSize || config.MaxJWKSBytes > maxJWKSSize {
		return errors.New("maximum JWKS size is outside the safe range")
	}
	return nil
}

func (verifier *RemoteVerifier) Verify(ctx context.Context, serialized string) (Identity, error) {
	if len(serialized) == 0 || len(serialized) > verifier.config.MaxTokenBytes {
		return Identity{}, ErrInvalidToken
	}

	claims := &jwt.RegisteredClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods(allowedAlgorithms),
		jwt.WithIssuer(verifier.config.Issuer),
		jwt.WithAudience(verifier.config.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(verifier.config.ClockSkew),
	)

	token, err := parser.ParseWithClaims(serialized, claims, func(token *jwt.Token) (any, error) {
		if critical, present := token.Header["crit"]; present {
			values, ok := critical.([]any)
			if !ok || len(values) != 0 {
				return nil, ErrInvalidToken
			}
		}
		algorithm := token.Method.Alg()
		keyID, ok := token.Header["kid"].(string)
		if !ok || keyID == "" || len(keyID) > maxKeyIDLength {
			return nil, ErrInvalidToken
		}
		return verifier.keyFor(ctx, keyID, algorithm)
	})
	if err != nil || token == nil || !token.Valid {
		return Identity{}, ErrInvalidToken
	}
	if claims.Subject == "" {
		return Identity{}, ErrInvalidToken
	}
	parsedSubject, err := uuid.Parse(claims.Subject)
	if err != nil || parsedSubject == uuid.Nil {
		return Identity{}, ErrInvalidToken
	}

	return Identity{Subject: parsedSubject.String()}, nil
}

func (verifier *RemoteVerifier) keyFor(ctx context.Context, keyID, algorithm string) (any, error) {
	now := verifier.now()
	if key, ok := verifier.cachedKey(keyID, algorithm, now); ok {
		return key, nil
	}

	if verifier.isNegative(keyID, now) {
		return nil, ErrInvalidToken
	}

	if err := verifier.refresh(ctx); err != nil {
		verifier.rememberNegative(keyID, now)
		return nil, ErrInvalidToken
	}
	if key, ok := verifier.cachedKey(keyID, algorithm, verifier.now()); ok {
		return key, nil
	}

	verifier.rememberNegative(keyID, verifier.now())
	return nil, ErrInvalidToken
}

func (verifier *RemoteVerifier) cachedKey(keyID, algorithm string, now time.Time) (any, bool) {
	verifier.mu.RLock()
	defer verifier.mu.RUnlock()
	if !now.Before(verifier.expiresAt) {
		return nil, false
	}
	key, ok := verifier.keys[keyID]
	if !ok || key.algorithm != algorithm {
		return nil, false
	}
	return key.publicKey, true
}

func (verifier *RemoteVerifier) isNegative(keyID string, now time.Time) bool {
	verifier.mu.RLock()
	defer verifier.mu.RUnlock()
	expiresAt, ok := verifier.negativeKids[keyID]
	return ok && now.Before(expiresAt)
}

func (verifier *RemoteVerifier) rememberNegative(keyID string, now time.Time) {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	if len(verifier.negativeKids) >= maxNegativeKids {
		verifier.negativeKids = make(map[string]time.Time)
	}
	verifier.negativeKids[keyID] = now.Add(verifier.config.RefreshCooldown)
}

func (verifier *RemoteVerifier) refresh(ctx context.Context) error {
	_, err, _ := verifier.refreshGroup.Do("jwks", func() (any, error) {
		now := verifier.now()
		verifier.mu.Lock()
		if now.Before(verifier.nextRefreshAllowed) {
			verifier.mu.Unlock()
			return nil, ErrInvalidToken
		}
		verifier.nextRefreshAllowed = now.Add(verifier.config.RefreshCooldown)
		verifier.mu.Unlock()

		keys, err := verifier.fetchKeys(ctx)
		if err != nil {
			return nil, err
		}

		verifier.mu.Lock()
		verifier.keys = keys
		verifier.expiresAt = verifier.now().Add(verifier.config.CacheTTL)
		verifier.negativeKids = make(map[string]time.Time)
		verifier.mu.Unlock()
		return nil, nil
	})
	return err
}

func (verifier *RemoteVerifier) fetchKeys(ctx context.Context) (map[string]verificationKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, verifier.config.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")

	response, err := verifier.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned status %d", response.StatusCode)
	}

	limited := io.LimitReader(response.Body, verifier.config.MaxJWKSBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil || int64(len(body)) > verifier.config.MaxJWKSBytes {
		return nil, errors.New("invalid JWKS response size")
	}

	var document jwksDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, errors.New("invalid JWKS document")
	}
	if len(document.Keys) == 0 || len(document.Keys) > maxCachedKeys {
		return nil, errors.New("invalid JWKS key count")
	}

	keys := make(map[string]verificationKey)
	for _, candidate := range document.Keys {
		key, supported, err := parsePublicJWK(candidate)
		if err != nil {
			return nil, err
		}
		if !supported {
			continue
		}
		if _, duplicate := keys[candidate.KeyID]; duplicate {
			return nil, errors.New("duplicate JWKS kid")
		}
		keys[candidate.KeyID] = key
	}
	if len(keys) == 0 {
		return nil, errors.New("JWKS has no allowed verification keys")
	}
	return keys, nil
}

func parsePublicJWK(candidate jsonWebKey) (verificationKey, bool, error) {
	if candidate.SymmetricK != "" || candidate.PrivateD != "" || candidate.PrimeP != "" || candidate.PrimeQ != "" ||
		candidate.ExponentDP != "" || candidate.ExponentDQ != "" || candidate.Coefficient != "" ||
		(len(candidate.OtherPrimes) != 0 && string(candidate.OtherPrimes) != "null") {
		return verificationKey{}, false, errors.New("JWKS contains private key parameters")
	}
	if candidate.KeyID == "" || len(candidate.KeyID) > maxKeyIDLength || candidate.Use != "sig" {
		return verificationKey{}, false, nil
	}
	if len(candidate.KeyOps) > 0 && !contains(candidate.KeyOps, "verify") {
		return verificationKey{}, false, nil
	}

	switch candidate.Algorithm {
	case "RS256":
		if candidate.KeyType != "RSA" || candidate.Modulus == "" || candidate.Exponent == "" {
			return verificationKey{}, false, nil
		}
		modulusBytes, err := base64.RawURLEncoding.DecodeString(candidate.Modulus)
		if err != nil {
			return verificationKey{}, false, errors.New("invalid RSA modulus")
		}
		exponentBytes, err := base64.RawURLEncoding.DecodeString(candidate.Exponent)
		if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
			return verificationKey{}, false, errors.New("invalid RSA exponent")
		}
		exponent := 0
		for _, value := range exponentBytes {
			exponent = exponent<<8 | int(value)
		}
		modulus := new(big.Int).SetBytes(modulusBytes)
		if modulus.BitLen() < 2048 || exponent < 3 || exponent%2 == 0 {
			return verificationKey{}, false, errors.New("invalid RSA exponent")
		}
		return verificationKey{
			algorithm: "RS256",
			publicKey: &rsa.PublicKey{N: modulus, E: exponent},
		}, true, nil
	case "ES256":
		if candidate.KeyType != "EC" || candidate.Curve != "P-256" || candidate.X == "" || candidate.Y == "" {
			return verificationKey{}, false, nil
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(candidate.X)
		if err != nil {
			return verificationKey{}, false, errors.New("invalid EC x coordinate")
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(candidate.Y)
		if err != nil {
			return verificationKey{}, false, errors.New("invalid EC y coordinate")
		}
		if len(xBytes) != 32 || len(yBytes) != 32 {
			return verificationKey{}, false, errors.New("invalid EC coordinate size")
		}
		x := new(big.Int).SetBytes(xBytes)
		y := new(big.Int).SetBytes(yBytes)
		if !elliptic.P256().IsOnCurve(x, y) {
			return verificationKey{}, false, errors.New("invalid EC public key")
		}
		return verificationKey{
			algorithm: "ES256",
			publicKey: &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y},
		}, true, nil
	default:
		return verificationKey{}, false, nil
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(value, expected) {
			return true
		}
	}
	return false
}
