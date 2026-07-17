package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"finance/backend/internal/auth"
	"finance/backend/internal/config"
	"finance/backend/internal/households"
)

const testSubject = "11000000-0000-4000-8000-000000000001"

type databaseStub struct {
	err error
}

func (database databaseStub) PingContext(context.Context) error {
	return database.err
}

type verifierStub struct {
	err       error
	calls     int
	lastToken string
}

func (verifier *verifierStub) Verify(_ context.Context, token string) (auth.Identity, error) {
	verifier.calls++
	verifier.lastToken = token
	if verifier.err != nil {
		return auth.Identity{}, verifier.err
	}
	return auth.Identity{Subject: testSubject}, nil
}

type householdServiceStub struct {
	err              error
	lastSubject      string
	lastHouseholdID  string
	lastUserID       string
	lastInvitationID string
	lastName         string
	lastRole         string
	lastStatus       string
	lastTTL          int
	lastKey          string
	lastToken        string
}

func (service *householdServiceStub) remember(subject string) {
	service.lastSubject = subject
}

func (service *householdServiceStub) Bootstrap(_ context.Context, subject, displayName string) (households.BootstrapResult, error) {
	service.remember(subject)
	service.lastName = displayName
	return households.BootstrapResult{User: households.User{ID: subject, DisplayName: "Пользователь"}, Households: []households.Household{}}, service.err
}

func (service *householdServiceStub) GetMe(_ context.Context, subject string) (households.User, error) {
	service.remember(subject)
	return households.User{ID: subject, DisplayName: "Пользователь"}, service.err
}

func (service *householdServiceStub) ListHouseholds(_ context.Context, subject string) ([]households.Household, error) {
	service.remember(subject)
	return []households.Household{}, service.err
}

func (service *householdServiceStub) CreateHousehold(_ context.Context, subject, name, key string) (households.Household, error) {
	service.remember(subject)
	service.lastName = name
	service.lastKey = key
	return households.Household{ID: "22000000-0000-4000-8000-000000000001", Name: name, Role: "owner"}, service.err
}

func (service *householdServiceStub) GetHousehold(_ context.Context, subject, householdID string) (households.Household, error) {
	service.remember(subject)
	service.lastHouseholdID = householdID
	return households.Household{ID: householdID, Name: "Дом", Role: "owner"}, service.err
}

func (service *householdServiceStub) UpdateHousehold(_ context.Context, subject, householdID, name string) (households.Household, error) {
	service.remember(subject)
	service.lastHouseholdID = householdID
	service.lastName = name
	return households.Household{ID: householdID, Name: name, Role: "owner"}, service.err
}

func (service *householdServiceStub) ListMembers(_ context.Context, subject, householdID string) ([]households.Member, error) {
	service.remember(subject)
	service.lastHouseholdID = householdID
	return []households.Member{}, service.err
}

func (service *householdServiceStub) UpdateMember(
	_ context.Context,
	subject, householdID, userID string,
	input households.UpdateMemberInput,
) (households.Member, error) {
	service.remember(subject)
	service.lastHouseholdID = householdID
	service.lastUserID = userID
	if input.Role != nil {
		service.lastRole = *input.Role
	}
	if input.Status != nil {
		service.lastStatus = *input.Status
	}
	return households.Member{UserID: userID, Role: service.lastRole, Status: service.lastStatus}, service.err
}

func (service *householdServiceStub) CreateInvitation(
	_ context.Context,
	subject, householdID, role string,
	ttl int,
	key string,
) (households.Invitation, error) {
	service.remember(subject)
	service.lastHouseholdID = householdID
	service.lastRole = role
	service.lastTTL = ttl
	service.lastKey = key
	return households.Invitation{ID: "33000000-0000-4000-8000-000000000001", HouseholdID: householdID, Role: role, Token: "raw-once"}, service.err
}

func (service *householdServiceStub) AcceptInvitation(_ context.Context, subject, token string) (households.Household, error) {
	service.remember(subject)
	service.lastToken = token
	return households.Household{ID: "22000000-0000-4000-8000-000000000001", Role: "member"}, service.err
}

func (service *householdServiceStub) RevokeInvitation(_ context.Context, subject, householdID, invitationID string) error {
	service.remember(subject)
	service.lastHouseholdID = householdID
	service.lastInvitationID = invitationID
	return service.err
}

func testConfig() config.Config {
	return config.Config{
		AppEnv:          "test",
		HTTPHost:        "127.0.0.1",
		HTTPPort:        8080,
		DatabaseURL:     "postgres://unused",
		FrontendOrigins: []string{"http://127.0.0.1:5173", "https://app.test"},
	}
}

func testLogger(output io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, nil))
}

func newTestServer(logOutput io.Writer) (*http.Server, *verifierStub, *householdServiceStub) {
	verifier := &verifierStub{}
	service := &householdServiceStub{}
	server := New(testConfig(), databaseStub{}, verifier, service, nil, testLogger(logOutput))
	return server, verifier, service
}

func authenticatedRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer valid-token")
	if body != "" {
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	return request
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v; body=%q", err, recorder.Body.String())
	}
	return body
}

func TestHealthIsPublicAndSafe(t *testing.T) {
	server, verifier, _ := newTestServer(io.Discard)
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || verifier.calls != 0 {
		t.Fatalf("public health failed: status=%d verifier_calls=%d", recorder.Code, verifier.calls)
	}
	if recorder.Header().Get("X-Request-ID") == "" {
		t.Fatal("health response has no request ID")
	}
	if !strings.Contains(recorder.Header().Get("Vary"), "Origin") {
		t.Fatal("response without Origin must still vary by Origin")
	}
}

func TestHealthReportsUnavailableDatabaseWithoutDetails(t *testing.T) {
	secret := "postgres://secret-user:secret-password@private-host/database"
	server := New(testConfig(), databaseStub{err: errors.New(secret)}, &verifierStub{}, &householdServiceStub{}, nil, testLogger(io.Discard))
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if recorder.Code != http.StatusServiceUnavailable || bytes.Contains(recorder.Body.Bytes(), []byte(secret)) {
		t.Fatalf("unsafe degraded health: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestEveryAPIV1RouteRequiresBearer(t *testing.T) {
	server, _, _ := newTestServer(io.Discard)
	routes := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/session/bootstrap"},
		{http.MethodGet, "/api/v1/me"},
		{http.MethodGet, "/api/v1/households"},
		{http.MethodPost, "/api/v1/households"},
		{http.MethodGet, "/api/v1/households/22000000-0000-4000-8000-000000000001"},
		{http.MethodPatch, "/api/v1/households/22000000-0000-4000-8000-000000000001"},
		{http.MethodGet, "/api/v1/households/22000000-0000-4000-8000-000000000001/members"},
		{http.MethodPatch, "/api/v1/households/22000000-0000-4000-8000-000000000001/members/11000000-0000-4000-8000-000000000002"},
		{http.MethodPost, "/api/v1/households/22000000-0000-4000-8000-000000000001/invitations"},
		{http.MethodPost, "/api/v1/invitations/accept"},
		{http.MethodPost, "/api/v1/households/22000000-0000-4000-8000-000000000001/invitations/33000000-0000-4000-8000-000000000001/revoke"},
	}
	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, nil))
			if recorder.Code != http.StatusUnauthorized || recorder.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("route not protected: status=%d", recorder.Code)
			}
			_ = decodeBody(t, recorder)
		})
	}
}

func TestBearerParserRejectsMalformedDuplicateAndInvalidTokens(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		invalid bool
	}{
		{name: "missing"},
		{name: "basic", headers: []string{"Basic abc"}},
		{name: "case sensitive scheme", headers: []string{"bearer abc"}},
		{name: "empty", headers: []string{"Bearer"}},
		{name: "extra parts", headers: []string{"Bearer abc def"}},
		{name: "duplicate", headers: []string{"Bearer one", "Bearer two"}},
		{name: "invalid signature", headers: []string{"Bearer invalid"}, invalid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, verifier, _ := newTestServer(io.Discard)
			if test.invalid {
				verifier.err = auth.ErrInvalidToken
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
			for _, value := range test.headers {
				request.Header.Add("Authorization", value)
			}
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized || recorder.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("invalid bearer accepted: status=%d", recorder.Code)
			}
		})
	}
}

func TestValidIdentityAndRouteInputsReachService(t *testing.T) {
	server, verifier, service := newTestServer(io.Discard)

	bootstrap := authenticatedRequest(http.MethodPost, "/api/v1/session/bootstrap", `{"displayName":"  Имя  "}`)
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, bootstrap)
	if recorder.Code != http.StatusOK || service.lastSubject != testSubject || verifier.lastToken != "valid-token" {
		t.Fatalf("identity was not propagated: status=%d service=%#v", recorder.Code, service)
	}

	create := authenticatedRequest(http.MethodPost, "/api/v1/households", `{"name":"Дом"}`)
	create.Header.Set("Idempotency-Key", "create-1")
	recorder = httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, create)
	if recorder.Code != http.StatusCreated || service.lastName != "Дом" || service.lastKey != "create-1" {
		t.Fatalf("create inputs lost: status=%d service=%#v", recorder.Code, service)
	}

	updateMember := authenticatedRequest(
		http.MethodPatch,
		"/api/v1/households/22000000-0000-4000-8000-000000000001/members/11000000-0000-4000-8000-000000000002",
		`{"role":"admin","status":"active"}`,
	)
	recorder = httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, updateMember)
	if recorder.Code != http.StatusOK || service.lastRole != "admin" || service.lastStatus != "active" {
		t.Fatalf("member inputs lost: status=%d service=%#v", recorder.Code, service)
	}

	invite := authenticatedRequest(http.MethodPost, "/api/v1/households/22000000-0000-4000-8000-000000000001/invitations", `{"role":"member","ttlSeconds":3600}`)
	invite.Header.Set("Idempotency-Key", "invite-1")
	recorder = httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, invite)
	if recorder.Code != http.StatusCreated || service.lastTTL != 3600 || service.lastKey != "invite-1" {
		t.Fatalf("invitation inputs lost: status=%d service=%#v", recorder.Code, service)
	}

	accept := authenticatedRequest(http.MethodPost, "/api/v1/invitations/accept", `{"token":"raw-secret"}`)
	recorder = httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, accept)
	if recorder.Code != http.StatusOK || service.lastToken != "raw-secret" {
		t.Fatalf("accept body was not passed: status=%d", recorder.Code)
	}

	revoke := authenticatedRequest(http.MethodPost, "/api/v1/households/hh-id/invitations/invite-id/revoke", "")
	recorder = httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, revoke)
	if recorder.Code != http.StatusOK || service.lastHouseholdID != "hh-id" || service.lastInvitationID != "invite-id" {
		t.Fatalf("revoke route IDs lost: status=%d service=%#v", recorder.Code, service)
	}
}

func TestStrictJSONContentTypeBodyLimitAndIdempotency(t *testing.T) {
	server, _, _ := newTestServer(io.Discard)
	tests := []struct {
		name       string
		request    *http.Request
		wantStatus int
	}{
		{name: "missing content type", request: func() *http.Request {
			request := authenticatedRequest(http.MethodPost, "/api/v1/session/bootstrap", `{"displayName":"Имя"}`)
			request.Header.Del("Content-Type")
			return request
		}(), wantStatus: http.StatusUnsupportedMediaType},
		{name: "unknown field", request: authenticatedRequest(http.MethodPost, "/api/v1/session/bootstrap", `{"unknown":true}`), wantStatus: http.StatusBadRequest},
		{name: "two JSON values", request: authenticatedRequest(http.MethodPost, "/api/v1/session/bootstrap", `{} {}`), wantStatus: http.StatusBadRequest},
		{name: "body too large", request: authenticatedRequest(http.MethodPost, "/api/v1/session/bootstrap", `{"displayName":"`+strings.Repeat("x", maxJSONBodyBytes)+`"}`), wantStatus: http.StatusRequestEntityTooLarge},
		{name: "household idempotency required", request: authenticatedRequest(http.MethodPost, "/api/v1/households", `{"name":"Дом"}`), wantStatus: http.StatusBadRequest},
		{name: "invite idempotency required", request: authenticatedRequest(http.MethodPost, "/api/v1/households/id/invitations", `{"role":"member","ttlSeconds":3600}`), wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, test.request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%q", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestServiceErrorMappingDoesNotExposeDetails(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{households.ErrInvalid, http.StatusBadRequest, "invalid_request"},
		{households.ErrNotFound, http.StatusNotFound, "not_found"},
		{households.ErrForbidden, http.StatusForbidden, "forbidden"},
		{households.ErrConflict, http.StatusConflict, "conflict"},
		{households.ErrIdempotencyReplay, http.StatusConflict, "idempotency_replayed"},
		{households.ErrInvitationUnavailable, http.StatusConflict, "invitation_unavailable"},
		{errors.New("private database detail"), http.StatusInternalServerError, "internal_error"},
	}
	for _, test := range tests {
		t.Run(test.wantCode, func(t *testing.T) {
			server, _, service := newTestServer(io.Discard)
			service.err = test.err
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/me", ""))
			body := decodeBody(t, recorder)
			errorBody := body["error"].(map[string]any)
			if recorder.Code != test.wantStatus || errorBody["code"] != test.wantCode || strings.Contains(recorder.Body.String(), "private database detail") {
				t.Fatalf("mapping failed: status=%d body=%q", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestInternalErrorLogUsesServerRequestIDWithoutDetails(t *testing.T) {
	var logs bytes.Buffer
	server, _, service := newTestServer(&logs)
	secret := "sql-dsn-token-key-id-amount"
	service.err = errors.New(secret)
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/me", ""))
	requestID := recorder.Header().Get("X-Request-ID")
	if recorder.Code != http.StatusInternalServerError || requestID == "" {
		t.Fatalf("internal response mismatch: status=%d request_id=%q", recorder.Code, requestID)
	}
	if !strings.Contains(logs.String(), requestID) || strings.Contains(logs.String(), secret) {
		t.Fatalf("unsafe internal error log: %s", logs.String())
	}
}

func TestMethodNotFoundAndNoFinancialRoutes(t *testing.T) {
	server, _, _ := newTestServer(io.Discard)
	tests := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/v1/session/bootstrap", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/v1/me", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/missing", http.StatusNotFound},
		{http.MethodGet, "/api/v1/transactions", http.StatusNotFound},
		{http.MethodPost, "/api/health", http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		request := authenticatedRequest(test.method, test.path, "")
		server.Handler.ServeHTTP(recorder, request)
		if recorder.Code != test.want {
			t.Fatalf("%s %s: status=%d want=%d", test.method, test.path, recorder.Code, test.want)
		}
	}
}

func TestCORSExactAllowlistAndPreflightBeforeAuth(t *testing.T) {
	server, verifier, _ := newTestServer(io.Discard)
	allowed := httptest.NewRequest(http.MethodOptions, "/api/v1/households", nil)
	allowed.Header.Set("Origin", "https://app.test")
	allowed.Header.Set("Access-Control-Request-Method", "POST")
	allowed.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type, Idempotency-Key")
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, allowed)
	if recorder.Code != http.StatusNoContent || verifier.calls != 0 ||
		recorder.Header().Get("Access-Control-Allow-Origin") != "https://app.test" ||
		!strings.Contains(recorder.Header().Get("Vary"), "Origin") ||
		recorder.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("allowed preflight failed: status=%d headers=%v", recorder.Code, recorder.Header())
	}

	for _, request := range []*http.Request{
		func() *http.Request {
			request := httptest.NewRequest(http.MethodOptions, "/api/v1/households", nil)
			request.Header.Set("Origin", "https://evil.test")
			request.Header.Set("Access-Control-Request-Method", "POST")
			return request
		}(),
		func() *http.Request {
			request := httptest.NewRequest(http.MethodOptions, "/api/v1/households", nil)
			request.Header.Set("Origin", "https://app.test")
			request.Header.Set("Access-Control-Request-Method", "POST")
			request.Header.Set("Access-Control-Request-Headers", "X-Unsafe")
			return request
		}(),
	} {
		recorder = httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden || recorder.Header().Get("Access-Control-Allow-Origin") != "" && request.Header.Get("Origin") != "https://app.test" {
			t.Fatalf("disallowed preflight leaked ACAO: status=%d headers=%v", recorder.Code, recorder.Header())
		}
	}
}

func TestLogsNeverContainAuthorizationInviteTokenOrUnknownPath(t *testing.T) {
	var logs bytes.Buffer
	server, verifier, service := newTestServer(&logs)
	verifier.err = auth.ErrInvalidToken
	authorizationSecret := "authorization-secret-value"
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.Header.Set("Authorization", "Bearer "+authorizationSecret)
	server.Handler.ServeHTTP(httptest.NewRecorder(), request)

	verifier.err = nil
	service.err = errors.New("database failed near raw-invite-secret")
	inviteSecret := "raw-invite-secret"
	request = authenticatedRequest(http.MethodPost, "/api/v1/invitations/accept", `{"token":"`+inviteSecret+`"}`)
	server.Handler.ServeHTTP(httptest.NewRecorder(), request)

	pathSecret := "token-accidentally-put-in-path"
	request = authenticatedRequest(http.MethodGet, "/api/v1/invitations/accept/"+pathSecret, "")
	server.Handler.ServeHTTP(httptest.NewRecorder(), request)

	output := logs.String()
	for _, secret := range []string{authorizationSecret, inviteSecret, pathSecret} {
		if strings.Contains(output, secret) {
			t.Fatalf("secret leaked to logs: %q in %s", secret, output)
		}
	}
	if !strings.Contains(output, "/api/v1/unmatched") {
		t.Fatalf("unknown API path was not sanitized: %s", output)
	}
}

func TestServerSafetyLimitsAndRecovery(t *testing.T) {
	server, _, _ := newTestServer(io.Discard)
	if server.ReadHeaderTimeout != 5*time.Second || server.ReadTimeout != 120*time.Second ||
		server.WriteTimeout != 120*time.Second || server.IdleTimeout != 60*time.Second || server.MaxHeaderBytes != 1<<20 {
		t.Fatal("server safety limits are not configured")
	}
	app := &application{logger: testLogger(io.Discard)}
	handler := app.requestID(app.logging(app.recoverPanic(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	}))))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("panic recovery status=%d", recorder.Code)
	}
}
