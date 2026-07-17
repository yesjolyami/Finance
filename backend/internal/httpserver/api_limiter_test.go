package httpserver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"finance/backend/internal/auth"
	"finance/backend/internal/config"
)

type apiLimiterClockStub struct{ now time.Time }

func (clock *apiLimiterClockStub) Now() time.Time { return clock.now }

func testAPILimiter(t *testing.T, clock *apiLimiterClockStub, config APILimiterConfig) *APILimiter {
	t.Helper()
	limiter, err := NewAPILimiter(config, clock)
	if err != nil {
		t.Fatalf("new API limiter: %v", err)
	}
	return limiter
}

func TestAPILimiterAttemptWindowRetryAfterAndCleanup(t *testing.T) {
	clock := &apiLimiterClockStub{now: time.Unix(100, 0)}
	limiter := testAPILimiter(t, clock, APILimiterConfig{
		Window: time.Minute, PerimeterAttempts: 2, SubjectAttempts: 1,
		PerimeterConcurrency: 2, SubjectConcurrency: 1, MaximumSubjects: 1,
	})

	for attempt := 0; attempt < 2; attempt++ {
		lease, retry, err := limiter.acquirePerimeter()
		if err != nil || retry != 0 {
			t.Fatalf("perimeter attempt %d: retry=%s error=%v", attempt+1, retry, err)
		}
		lease.release()
	}
	if _, retry, err := limiter.acquirePerimeter(); err != ErrAPILimited || retry != time.Minute {
		t.Fatalf("perimeter limit: retry=%s error=%v", retry, err)
	}

	subject, _, err := limiter.acquireSubject("subject-a")
	if err != nil {
		t.Fatal(err)
	}
	subject.release()
	if _, retry, err := limiter.acquireSubject("subject-a"); err != ErrAPILimited || retry != time.Minute {
		t.Fatalf("subject limit: retry=%s error=%v", retry, err)
	}
	if _, _, err := limiter.acquireSubject("subject-b"); err != ErrAPILimited {
		t.Fatalf("bounded subject map accepted another key: %v", err)
	}

	clock.now = clock.now.Add(time.Minute)
	lease, _, err := limiter.acquireSubject("subject-b")
	if err != nil {
		t.Fatalf("expired inactive subject was not cleaned: %v", err)
	}
	lease.release()
	if len(limiter.subjects) != 1 {
		t.Fatalf("tracked subjects=%d", len(limiter.subjects))
	}
}

func TestAPILimiterConcurrencyAndIdempotentRelease(t *testing.T) {
	clock := &apiLimiterClockStub{now: time.Unix(200, 0)}
	limiter := testAPILimiter(t, clock, APILimiterConfig{
		Window: time.Minute, PerimeterAttempts: 10, SubjectAttempts: 10,
		PerimeterConcurrency: 1, SubjectConcurrency: 1, MaximumSubjects: 4,
	})
	perimeter, _, err := limiter.acquirePerimeter()
	if err != nil {
		t.Fatal(err)
	}
	if _, retry, err := limiter.acquirePerimeter(); err != ErrAPILimited || retry < time.Second {
		t.Fatalf("perimeter concurrency error=%v retry=%s", err, retry)
	}
	perimeter.release()
	perimeter.release()

	subject, _, err := limiter.acquireSubject("subject")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := limiter.acquireSubject("subject"); err != ErrAPILimited {
		t.Fatalf("subject concurrency error=%v", err)
	}
	other, _, err := limiter.acquireSubject("other")
	if err != nil {
		t.Fatalf("isolated subject rejected: %v", err)
	}
	other.release()
	subject.release()
	if limiter.perimeterActive != 0 || limiter.subjects["subject"].active != 0 {
		t.Fatalf("leases were not released: perimeter=%d subject=%d",
			limiter.perimeterActive, limiter.subjects["subject"].active)
	}
}

func TestAPILimiterRejectsUnsafeConfiguration(t *testing.T) {
	clock := &apiLimiterClockStub{now: time.Now()}
	valid := APILimiterConfig{
		Window: time.Minute, PerimeterAttempts: 1, SubjectAttempts: 1,
		PerimeterConcurrency: 1, SubjectConcurrency: 1, MaximumSubjects: 1,
	}
	tests := []APILimiterConfig{
		{},
		{Window: 16 * time.Minute, PerimeterAttempts: 1, SubjectAttempts: 1, PerimeterConcurrency: 1, SubjectConcurrency: 1, MaximumSubjects: 1},
		{Window: time.Minute, PerimeterAttempts: 1, SubjectAttempts: 1, PerimeterConcurrency: 1, SubjectConcurrency: 1, MaximumSubjects: maximumRateLimitTrackedSubjects + 1},
	}
	for _, candidate := range tests {
		if _, err := NewAPILimiter(candidate, clock); err == nil {
			t.Fatal("unsafe limiter configuration accepted")
		}
	}
	if _, err := NewAPILimiter(valid, nil); err == nil {
		t.Fatal("nil limiter clock accepted")
	}
}

func newLimitedServer(
	t *testing.T,
	appConfig config.Config,
	limiter *APILimiter,
	logs io.Writer,
) (*http.Server, *verifierStub, *householdServiceStub) {
	t.Helper()
	verifier := &verifierStub{}
	service := &householdServiceStub{}
	server := New(appConfig, databaseStub{}, verifier, service, nil, testLogger(logs), WithAPILimiter(limiter))
	return server, verifier, service
}

func TestHTTPGeneralLimiterProtectsBeforeAndAfterAuthentication(t *testing.T) {
	clock := &apiLimiterClockStub{now: time.Unix(300, 0)}
	perimeterLimiter := testAPILimiter(t, clock, APILimiterConfig{
		Window: time.Minute, PerimeterAttempts: 1, SubjectAttempts: 10,
		PerimeterConcurrency: 1, SubjectConcurrency: 2, MaximumSubjects: 8,
	})
	server, verifier, _ := newLimitedServer(t, testConfig(), perimeterLimiter, io.Discard)
	first := httptest.NewRecorder()
	firstRequest := authenticatedRequest(http.MethodGet, "/api/v1/me", "")
	firstRequest.Header.Set("X-Forwarded-For", "198.51.100.10")
	server.Handler.ServeHTTP(first, firstRequest)
	second := httptest.NewRecorder()
	body := newObservedBody(`{"financial":"secret"}`)
	secondRequest := authenticatedRequest(http.MethodPost, "/api/v1/session/bootstrap", "")
	secondRequest.Body = body
	secondRequest.ContentLength = int64(body.data.Len())
	secondRequest.Header.Set("Content-Type", "application/json")
	secondRequest.Header.Set("X-Forwarded-For", "203.0.113.20")
	server.Handler.ServeHTTP(second, secondRequest)
	if first.Code != http.StatusOK || second.Code != http.StatusTooManyRequests ||
		second.Header().Get("Retry-After") != "60" || verifier.calls != 1 || body.reads != 0 {
		t.Fatalf("perimeter limiter mismatch: first=%d second=%d retry=%q verifier=%d",
			first.Code, second.Code, second.Header().Get("Retry-After"), verifier.calls)
	}

	subjectLimiter := testAPILimiter(t, clock, APILimiterConfig{
		Window: time.Minute, PerimeterAttempts: 10, SubjectAttempts: 1,
		PerimeterConcurrency: 2, SubjectConcurrency: 1, MaximumSubjects: 8,
	})
	server, verifier, _ = newLimitedServer(t, testConfig(), subjectLimiter, io.Discard)
	first = httptest.NewRecorder()
	server.Handler.ServeHTTP(first, authenticatedRequest(http.MethodGet, "/api/v1/me", ""))
	second = httptest.NewRecorder()
	server.Handler.ServeHTTP(second, authenticatedRequest(http.MethodGet, "/api/v1/me", ""))
	if first.Code != http.StatusOK || second.Code != http.StatusTooManyRequests ||
		second.Header().Get("Retry-After") != "60" || verifier.calls != 2 {
		t.Fatalf("subject limiter mismatch: first=%d second=%d retry=%q verifier=%d",
			first.Code, second.Code, second.Header().Get("Retry-After"), verifier.calls)
	}
}

func TestProductionOriginGateForMutationsAndHealth(t *testing.T) {
	appConfig := testConfig()
	appConfig.AppEnv = "production"
	server, verifier, _ := newLimitedServer(t, appConfig, newDefaultAPILimiter(), io.Discard)

	tests := []struct {
		name    string
		origins []string
		want    int
	}{
		{name: "missing", want: http.StatusForbidden},
		{name: "null", origins: []string{"null"}, want: http.StatusForbidden},
		{name: "cross origin", origins: []string{"https://evil.test"}, want: http.StatusForbidden},
		{name: "duplicate", origins: []string{"https://app.test", "https://app.test"}, want: http.StatusForbidden},
		{name: "allowed", origins: []string{"https://app.test"}, want: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := authenticatedRequest(http.MethodPost, "/api/v1/session/bootstrap", `{}`)
			for _, origin := range test.origins {
				request.Header.Add("Origin", origin)
			}
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%q", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
	if verifier.calls != 1 {
		t.Fatalf("rejected origins reached JWT verifier: calls=%d", verifier.calls)
	}
	patchPath := "/api/v1/households/22000000-0000-4000-8000-000000000001"
	patch := authenticatedRequest(http.MethodPatch, patchPath, `{"name":"Дом"}`)
	patchRecorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(patchRecorder, patch)
	if patchRecorder.Code != http.StatusForbidden {
		t.Fatalf("PATCH without Origin status=%d", patchRecorder.Code)
	}
	patch = authenticatedRequest(http.MethodPatch, patchPath, `{"name":"Дом"}`)
	patch.Header.Set("Origin", "https://app.test")
	patchRecorder = httptest.NewRecorder()
	server.Handler.ServeHTTP(patchRecorder, patch)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("PATCH with allowed Origin status=%d body=%q", patchRecorder.Code, patchRecorder.Body.String())
	}

	health := httptest.NewRecorder()
	server.Handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if health.Code != http.StatusOK || strings.Contains(health.Body.String(), "limit") {
		t.Fatalf("health was changed by security perimeter: status=%d body=%q", health.Code, health.Body.String())
	}
}

func TestProductionOriginGateProtectsBackupImportRoutes(t *testing.T) {
	appConfig := testConfig()
	appConfig.AppEnv = "production"
	server, verifier, _ := newLimitedServer(t, appConfig, newDefaultAPILimiter(), io.Discard)
	for _, action := range []string{"preview", "confirm"} {
		path := "/api/v1/households/22000000-0000-4000-8000-000000000001/imports/backup-v5/" + action
		request := authenticatedRequest(http.MethodPost, path, `{}`)
		request.Header.Set("Import-Budget-Month", "2026-07-01")
		if action == "confirm" {
			request.Header.Set("Import-Preview-Token", strings.Repeat("P", 43))
			request.Header.Set("Idempotency-Key", "confirm-key")
		}
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s without Origin status=%d", action, recorder.Code)
		}

		request = authenticatedRequest(http.MethodPost, path, `{}`)
		request.Header.Set("Origin", "https://app.test")
		request.Header.Set("Import-Budget-Month", "2026-07-01")
		if action == "confirm" {
			request.Header.Set("Import-Preview-Token", strings.Repeat("P", 43))
			request.Header.Set("Idempotency-Key", "confirm-key")
		}
		recorder = httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s with allowed Origin status=%d body=%q", action, recorder.Code, recorder.Body.String())
		}
	}
	if verifier.calls != 2 {
		t.Fatalf("rejected import origins reached verifier: calls=%d", verifier.calls)
	}
}

type cancelingVerifier struct{ started chan struct{} }

func (verifier *cancelingVerifier) Verify(ctx context.Context, _ string) (auth.Identity, error) {
	close(verifier.started)
	<-ctx.Done()
	return auth.Identity{}, auth.ErrInvalidToken
}

func TestAPIPerimeterReleasesConcurrencyAfterCancellation(t *testing.T) {
	clock := &apiLimiterClockStub{now: time.Unix(350, 0)}
	limiter := testAPILimiter(t, clock, APILimiterConfig{
		Window: time.Minute, PerimeterAttempts: 5, SubjectAttempts: 5,
		PerimeterConcurrency: 1, SubjectConcurrency: 1, MaximumSubjects: 4,
	})
	verifier := &cancelingVerifier{started: make(chan struct{})}
	server := New(
		testConfig(), databaseStub{}, verifier, &householdServiceStub{}, nil,
		testLogger(io.Discard), WithAPILimiter(limiter),
	)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer valid")
	done := make(chan struct{})
	go func() {
		server.Handler.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	<-verifier.started
	limiter.mu.Lock()
	active := limiter.perimeterActive
	limiter.mu.Unlock()
	if active != 1 {
		t.Fatalf("active perimeter=%d", active)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled request did not finish")
	}
	limiter.mu.Lock()
	active = limiter.perimeterActive
	limiter.mu.Unlock()
	if active != 0 {
		t.Fatalf("cancelled request leaked perimeter slot: %d", active)
	}
}

func TestSecurityFailureLogsAreRedacted(t *testing.T) {
	clock := &apiLimiterClockStub{now: time.Unix(400, 0)}
	limiter := testAPILimiter(t, clock, APILimiterConfig{
		Window: time.Minute, PerimeterAttempts: 1, SubjectAttempts: 1,
		PerimeterConcurrency: 1, SubjectConcurrency: 1, MaximumSubjects: 2,
	})
	appConfig := testConfig()
	appConfig.AppEnv = "production"
	var logs bytes.Buffer
	server, _, _ := newLimitedServer(t, appConfig, limiter, &logs)

	secret := "bearer-token-dsn-key-id-digest-amount"
	originSecret := "https://" + secret + ".invalid"
	originRequest := authenticatedRequest(http.MethodPost, "/api/v1/session/bootstrap", `{}`)
	originRequest.Header.Set("Origin", originSecret)
	server.Handler.ServeHTTP(httptest.NewRecorder(), originRequest)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/session/bootstrap", strings.NewReader(secret))
	request.Header.Add("Origin", "https://app.test")
	request.Header.Add("Authorization", "Bearer "+secret)
	request.Header.Add("Authorization", "Bearer duplicate")
	request.Header.Set("X-Forwarded-For", secret)
	request.Header.Set("Content-Type", "application/json")
	server.Handler.ServeHTTP(httptest.NewRecorder(), request)

	request = authenticatedRequest(http.MethodGet, "/api/v1/me", "")
	server.Handler.ServeHTTP(httptest.NewRecorder(), request)
	request = authenticatedRequest(http.MethodGet, "/api/v1/me", "")
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, request)

	output := logs.String()
	if strings.Contains(output, secret) || strings.Contains(output, originSecret) || strings.Contains(output, "duplicate") {
		t.Fatalf("security logs disclosed request data: %s", output)
	}
	for _, class := range []string{"origin_rejected", "authorization_ambiguous", "auth_perimeter_limited"} {
		if !strings.Contains(output, class) {
			t.Fatalf("safe error class %q missing from logs: %s", class, output)
		}
	}
	if strings.Contains(output, `"request_id":""`) || !strings.Contains(output, `"request_id":"`) {
		t.Fatalf("security failure log has no server request ID: %s", output)
	}
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("limited response status=%d", recorder.Code)
	}
	if _, err := strconv.Atoi(recorder.Header().Get("Retry-After")); err != nil {
		t.Fatalf("invalid Retry-After: %q", recorder.Header().Get("Retry-After"))
	}
}
