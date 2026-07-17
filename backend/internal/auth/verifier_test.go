package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://project.test/auth/v1"
	testAudience = "authenticated"
	testSubject  = "a0000000-0000-4000-8000-000000000001"
)

type mutableJWKS struct {
	mu       sync.RWMutex
	keys     []jsonWebKey
	status   int
	delay    time.Duration
	extra    string
	requests atomic.Int64
}

func (state *mutableJWKS) handler(writer http.ResponseWriter, request *http.Request) {
	state.requests.Add(1)
	state.mu.RLock()
	keys := append([]jsonWebKey(nil), state.keys...)
	status := state.status
	delay := state.delay
	extra := state.extra
	state.mu.RUnlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-request.Context().Done():
			return
		}
	}
	if status == 0 {
		status = http.StatusOK
	}
	writer.WriteHeader(status)
	if extra != "" {
		_, _ = writer.Write([]byte(extra))
		return
	}
	_ = json.NewEncoder(writer).Encode(jwksDocument{Keys: keys})
}

func (state *mutableJWKS) setKeys(keys ...jsonWebKey) {
	state.mu.Lock()
	state.keys = append([]jsonWebKey(nil), keys...)
	state.mu.Unlock()
}

func newTestVerifier(t *testing.T, state *mutableJWKS) (*RemoteVerifier, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(state.handler))
	t.Cleanup(server.Close)
	verifier, err := NewRemoteVerifier(VerifierConfig{
		Issuer:          testIssuer,
		Audience:        testAudience,
		JWKSURL:         server.URL,
		CacheTTL:        time.Minute,
		RefreshCooldown: 10 * time.Second,
		ClockSkew:       5 * time.Second,
		HTTPTimeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("NewRemoteVerifier() error: %v", err)
	}
	return verifier, server
}

func rsaJWK(keyID string, publicKey *rsa.PublicKey) jsonWebKey {
	return jsonWebKey{
		KeyID:     keyID,
		Algorithm: "RS256",
		KeyType:   "RSA",
		Use:       "sig",
		KeyOps:    []string{"verify"},
		Modulus:   base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
		Exponent:  base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
	}
}

func ecJWK(keyID string, publicKey *ecdsa.PublicKey) jsonWebKey {
	return jsonWebKey{
		KeyID:     keyID,
		Algorithm: "ES256",
		KeyType:   "EC",
		Use:       "sig",
		KeyOps:    []string{"verify"},
		Curve:     "P-256",
		X:         base64.RawURLEncoding.EncodeToString(publicKey.X.FillBytes(make([]byte, 32))),
		Y:         base64.RawURLEncoding.EncodeToString(publicKey.Y.FillBytes(make([]byte, 32))),
	}
}

func validClaims(now time.Time) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{
		Issuer:    testIssuer,
		Subject:   testSubject,
		Audience:  jwt.ClaimStrings{testAudience},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		IssuedAt:  jwt.NewNumericDate(now),
	}
}

func signToken(t *testing.T, method jwt.SigningMethod, keyID string, claims jwt.RegisteredClaims, key any) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	if keyID != "" {
		token.Header["kid"] = keyID
	}
	serialized, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("SignedString() error: %v", err)
	}
	return serialized
}

func TestVerifyAcceptsValidRS256AndES256(t *testing.T) {
	now := time.Now()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	state := &mutableJWKS{keys: []jsonWebKey{rsaJWK("rsa", &rsaKey.PublicKey), ecJWK("ec", &ecKey.PublicKey)}}
	verifier, _ := newTestVerifier(t, state)
	if _, err := verifier.fetchKeys(context.Background()); err != nil {
		t.Fatalf("valid JWKS rejected: %v", err)
	}
	if _, err := verifier.keyFor(context.Background(), "rsa", "RS256"); err != nil {
		t.Fatalf("valid RSA JWK rejected before token parsing: %v", err)
	}

	for _, test := range []struct {
		name   string
		method jwt.SigningMethod
		keyID  string
		key    any
	}{
		{name: "RS256", method: jwt.SigningMethodRS256, keyID: "rsa", key: rsaKey},
		{name: "ES256", method: jwt.SigningMethodES256, keyID: "ec", key: ecKey},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity, err := verifier.Verify(context.Background(), signToken(t, test.method, test.keyID, validClaims(now), test.key))
			if err != nil || identity.Subject != testSubject {
				t.Fatalf("valid token rejected: identity=%#v err=%v", identity, err)
			}
		})
	}
}

func TestVerifyRejectsInvalidAlgorithmsSignatureAndClaims(t *testing.T) {
	now := time.Now()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	state := &mutableJWKS{keys: []jsonWebKey{rsaJWK("rsa", &key.PublicKey)}}
	verifier, _ := newTestVerifier(t, state)

	missingExp := validClaims(now)
	missingExp.ExpiresAt = nil
	missingSub := validClaims(now)
	missingSub.Subject = ""
	wrongIssuer := validClaims(now)
	wrongIssuer.Issuer = "https://other.test/auth/v1"
	wrongAudience := validClaims(now)
	wrongAudience.Audience = jwt.ClaimStrings{"anon"}
	expired := validClaims(now)
	expired.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Minute))
	futureNBF := validClaims(now)
	futureNBF.NotBefore = jwt.NewNumericDate(now.Add(time.Minute))

	noneToken := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims(now))
	noneToken.Header["kid"] = "rsa"
	noneSerialized, err := noneToken.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	criticalToken := jwt.NewWithClaims(jwt.SigningMethodRS256, validClaims(now))
	criticalToken.Header["kid"] = "rsa"
	criticalToken.Header["crit"] = []string{"unsupported"}
	criticalSerialized, err := criticalToken.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		token string
	}{
		{name: "alg none", token: noneSerialized},
		{name: "unknown critical header", token: criticalSerialized},
		{name: "HS256", token: signToken(t, jwt.SigningMethodHS256, "rsa", validClaims(now), []byte("not-a-real-secret"))},
		{name: "invalid signature", token: signToken(t, jwt.SigningMethodRS256, "rsa", validClaims(now), otherKey)},
		{name: "issuer", token: signToken(t, jwt.SigningMethodRS256, "rsa", wrongIssuer, key)},
		{name: "audience", token: signToken(t, jwt.SigningMethodRS256, "rsa", wrongAudience, key)},
		{name: "expired", token: signToken(t, jwt.SigningMethodRS256, "rsa", expired, key)},
		{name: "future nbf", token: signToken(t, jwt.SigningMethodRS256, "rsa", futureNBF, key)},
		{name: "missing exp", token: signToken(t, jwt.SigningMethodRS256, "rsa", missingExp, key)},
		{name: "missing sub", token: signToken(t, jwt.SigningMethodRS256, "rsa", missingSub, key)},
		{name: "missing kid", token: signToken(t, jwt.SigningMethodRS256, "", validClaims(now), key)},
		{name: "malformed", token: "not.a.jwt"},
		{name: "oversized", token: strings.Repeat("x", defaultTokenSize+1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := verifier.Verify(context.Background(), test.token); err == nil {
				t.Fatal("invalid token was accepted")
			}
		})
	}
}

func TestParsePublicJWKRejectsAlgorithmKeyMismatchAndPrivateParameters(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		key       jsonWebKey
		wantError bool
	}{
		{name: "RSA marked ES256", key: func() jsonWebKey { key := rsaJWK("bad", &rsaKey.PublicKey); key.Algorithm = "ES256"; return key }()},
		{name: "EC marked RS256", key: func() jsonWebKey { key := ecJWK("bad", &ecKey.PublicKey); key.Algorithm = "RS256"; return key }()},
		{name: "wrong EC curve", key: func() jsonWebKey { key := ecJWK("bad", &ecKey.PublicKey); key.Curve = "P-384"; return key }()},
		{name: "wrong use", key: func() jsonWebKey { key := rsaJWK("bad", &rsaKey.PublicKey); key.Use = "enc"; return key }()},
		{name: "wrong key ops", key: func() jsonWebKey { key := rsaJWK("bad", &rsaKey.PublicKey); key.KeyOps = []string{"sign"}; return key }()},
		{name: "private RSA", key: func() jsonWebKey { key := rsaJWK("bad", &rsaKey.PublicKey); key.PrivateD = "secret"; return key }(), wantError: true},
		{name: "private EC", key: func() jsonWebKey { key := ecJWK("bad", &ecKey.PublicKey); key.PrivateD = "secret"; return key }(), wantError: true},
		{name: "symmetric key material", key: func() jsonWebKey { key := rsaJWK("bad", &rsaKey.PublicKey); key.SymmetricK = "secret"; return key }(), wantError: true},
		{name: "weak RSA modulus", key: jsonWebKey{
			KeyID: "weak", Algorithm: "RS256", KeyType: "RSA", Use: "sig",
			Modulus:  base64.RawURLEncoding.EncodeToString(new(big.Int).Lsh(big.NewInt(1), 1023).Bytes()),
			Exponent: base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
		}, wantError: true},
		{name: "even RSA exponent", key: func() jsonWebKey {
			key := rsaJWK("even", &rsaKey.PublicKey)
			key.Exponent = base64.RawURLEncoding.EncodeToString([]byte{4})
			return key
		}(), wantError: true},
		{name: "short EC coordinate", key: func() jsonWebKey {
			key := ecJWK("short", &ecKey.PublicKey)
			key.X = base64.RawURLEncoding.EncodeToString([]byte{1})
			return key
		}(), wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, supported, err := parsePublicJWK(test.key)
			if supported {
				t.Fatal("incompatible JWK was accepted")
			}
			if test.wantError != (err != nil) {
				t.Fatalf("unexpected error state: %v", err)
			}
		})
	}
}

func TestVerifierConfigRejectsUnsafeBounds(t *testing.T) {
	base := VerifierConfig{
		Issuer:          testIssuer,
		Audience:        testAudience,
		JWKSURL:         "https://project.test/auth/v1/.well-known/jwks.json",
		CacheTTL:        10 * time.Minute,
		RefreshCooldown: 30 * time.Second,
		ClockSkew:       30 * time.Second,
		HTTPTimeout:     2 * time.Second,
	}
	tests := []struct {
		name   string
		mutate func(*VerifierConfig)
	}{
		{name: "TTL over ten minutes", mutate: func(config *VerifierConfig) { config.CacheTTL = 11 * time.Minute }},
		{name: "cooldown too short", mutate: func(config *VerifierConfig) { config.RefreshCooldown = time.Millisecond }},
		{name: "cooldown too long", mutate: func(config *VerifierConfig) { config.RefreshCooldown = 2 * time.Minute }},
		{name: "negative token size", mutate: func(config *VerifierConfig) { config.MaxTokenBytes = -1 }},
		{name: "huge token size", mutate: func(config *VerifierConfig) { config.MaxTokenBytes = maxTokenSize + 1 }},
		{name: "negative JWKS size", mutate: func(config *VerifierConfig) { config.MaxJWKSBytes = -1 }},
		{name: "huge JWKS size", mutate: func(config *VerifierConfig) { config.MaxJWKSBytes = maxJWKSSize + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base
			test.mutate(&config)
			if _, err := NewRemoteVerifier(config); err == nil {
				t.Fatal("unsafe verifier configuration was accepted")
			}
		})
	}
}

func TestJWKSRedirectIsRejected(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var targetRequests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		targetRequests.Add(1)
		_ = json.NewEncoder(writer).Encode(jwksDocument{Keys: []jsonWebKey{rsaJWK("rsa", &key.PublicKey)}})
	}))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	t.Cleanup(redirect.Close)

	verifier, err := NewRemoteVerifier(VerifierConfig{
		Issuer: testIssuer, Audience: testAudience, JWKSURL: redirect.URL,
		CacheTTL: time.Minute, RefreshCooldown: 10 * time.Second,
		ClockSkew: 5 * time.Second, HTTPTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), signToken(t, jwt.SigningMethodRS256, "rsa", validClaims(time.Now()), key)); err == nil {
		t.Fatal("token accepted through redirected JWKS")
	}
	if targetRequests.Load() != 0 {
		t.Fatal("JWKS client followed a redirect")
	}
}

func TestUnknownKidsUseSingleRefreshCooldownAndRotation(t *testing.T) {
	keyA, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	state := &mutableJWKS{keys: []jsonWebKey{rsaJWK("a", &keyA.PublicKey)}}
	verifier, _ := newTestVerifier(t, state)
	clock := time.Now()
	verifier.now = func() time.Time { return clock }

	if _, err := verifier.Verify(context.Background(), signToken(t, jwt.SigningMethodRS256, "a", validClaims(clock), keyA)); err != nil {
		t.Fatalf("initial key rejected: %v", err)
	}
	clock = clock.Add(11 * time.Second)

	var wait sync.WaitGroup
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			kid := "unknown-" + string(rune('a'+index))
			token := signToken(t, jwt.SigningMethodRS256, kid, validClaims(clock), keyA)
			_, _ = verifier.Verify(context.Background(), token)
		}(index)
	}
	wait.Wait()
	if requests := state.requests.Load(); requests != 2 {
		t.Fatalf("unknown kid amplification: got %d JWKS requests, want 2", requests)
	}

	state.setKeys(rsaJWK("a", &keyA.PublicKey), rsaJWK("b", &keyB.PublicKey))
	clock = clock.Add(11 * time.Second)
	if _, err := verifier.Verify(context.Background(), signToken(t, jwt.SigningMethodRS256, "b", validClaims(clock), keyB)); err != nil {
		t.Fatalf("rotated key rejected: %v", err)
	}
	if requests := state.requests.Load(); requests != 3 {
		t.Fatalf("unexpected JWKS requests after rotation: %d", requests)
	}
}

func TestJWKSFailuresAreFailClosed(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("timeout", func(t *testing.T) {
		state := &mutableJWKS{keys: []jsonWebKey{rsaJWK("rsa", &key.PublicKey)}, delay: 100 * time.Millisecond}
		verifier, server := newTestVerifier(t, state)
		verifier.client.Timeout = 10 * time.Millisecond
		verifier.config.JWKSURL = server.URL
		if _, err := verifier.Verify(context.Background(), signToken(t, jwt.SigningMethodRS256, "rsa", validClaims(time.Now()), key)); err == nil {
			t.Fatal("token accepted after JWKS timeout")
		}
	})

	t.Run("oversized response", func(t *testing.T) {
		state := &mutableJWKS{extra: strings.Repeat("x", 2048)}
		verifier, _ := newTestVerifier(t, state)
		verifier.config.MaxJWKSBytes = 1024
		if _, err := verifier.Verify(context.Background(), signToken(t, jwt.SigningMethodRS256, "rsa", validClaims(time.Now()), key)); err == nil {
			t.Fatal("token accepted with oversized JWKS")
		}
	})

	t.Run("expired cache is not stale fallback", func(t *testing.T) {
		state := &mutableJWKS{keys: []jsonWebKey{rsaJWK("rsa", &key.PublicKey)}}
		verifier, _ := newTestVerifier(t, state)
		clock := time.Now()
		verifier.now = func() time.Time { return clock }
		token := signToken(t, jwt.SigningMethodRS256, "rsa", validClaims(clock), key)
		if _, err := verifier.Verify(context.Background(), token); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(2 * time.Minute)
		state.mu.Lock()
		state.status = http.StatusServiceUnavailable
		state.mu.Unlock()
		if _, err := verifier.Verify(context.Background(), token); err == nil {
			t.Fatal("expired cached key was used after refresh failure")
		}
	})
}
