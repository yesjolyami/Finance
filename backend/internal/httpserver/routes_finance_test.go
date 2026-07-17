package httpserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	financecore "finance/backend/internal/finance"
)

const financeTestHousehold = "22000000-0000-4000-8000-000000000001"
const financeTestObject = "33000000-0000-4000-8000-000000000001"

type financeServiceStub struct {
	err                  error
	replayed             bool
	lastSubject          string
	lastHousehold        string
	lastObject           string
	lastKey              string
	lastVersion          string
	lastRequestID        string
	lastAccountPatch     financecore.AccountPatchInput
	lastTransactionPatch financecore.TransactionPatchInput
}

func (s *financeServiceStub) remember(subject, household string) {
	s.lastSubject = subject
	s.lastHousehold = household
}
func (s *financeServiceStub) ListAccounts(_ context.Context, subject, household string, _ financecore.AccountListInput) (financecore.AccountPage, error) {
	s.remember(subject, household)
	return financecore.AccountPage{Accounts: []financecore.Account{}}, s.err
}
func (s *financeServiceStub) CreateAccount(_ context.Context, subject, household, key, requestID string, _ financecore.CreateAccountInput) (financecore.CreateResult[financecore.Account], error) {
	s.remember(subject, household)
	s.lastKey = key
	s.lastRequestID = requestID
	return financecore.CreateResult[financecore.Account]{Value: stubAccount(), Replayed: s.replayed}, s.err
}
func (s *financeServiceStub) UpdateAccount(_ context.Context, subject, household, id, version, requestID string, input financecore.AccountPatchInput) (financecore.Account, error) {
	s.remember(subject, household)
	s.lastObject = id
	s.lastVersion = version
	s.lastRequestID = requestID
	s.lastAccountPatch = input
	return stubAccount(), s.err
}
func (s *financeServiceStub) SetAccountArchived(_ context.Context, subject, household, id, version, requestID string, _ bool) (financecore.Account, error) {
	s.remember(subject, household)
	s.lastObject = id
	s.lastVersion = version
	s.lastRequestID = requestID
	return stubAccount(), s.err
}
func (s *financeServiceStub) ListCategories(_ context.Context, subject, household string, _ financecore.CategoryListInput) (financecore.CategoryPage, error) {
	s.remember(subject, household)
	return financecore.CategoryPage{Categories: []financecore.Category{}}, s.err
}
func (s *financeServiceStub) CreateCategory(_ context.Context, subject, household, key, requestID string, _ financecore.CreateCategoryInput) (financecore.CreateResult[financecore.Category], error) {
	s.remember(subject, household)
	s.lastKey = key
	s.lastRequestID = requestID
	return financecore.CreateResult[financecore.Category]{Value: stubCategory(), Replayed: s.replayed}, s.err
}
func (s *financeServiceStub) UpdateCategory(_ context.Context, subject, household, id, version, requestID string, _ financecore.CategoryPatchInput) (financecore.Category, error) {
	s.remember(subject, household)
	s.lastObject = id
	s.lastVersion = version
	s.lastRequestID = requestID
	return stubCategory(), s.err
}
func (s *financeServiceStub) SetCategoryArchived(_ context.Context, subject, household, id, version, requestID string, _ bool) (financecore.Category, error) {
	s.remember(subject, household)
	s.lastObject = id
	s.lastVersion = version
	s.lastRequestID = requestID
	return stubCategory(), s.err
}
func (s *financeServiceStub) ListTransactions(_ context.Context, subject, household string, _ financecore.TransactionListInput) (financecore.TransactionPage, error) {
	s.remember(subject, household)
	return financecore.TransactionPage{Transactions: []financecore.Transaction{}}, s.err
}
func (s *financeServiceStub) CreateTransaction(_ context.Context, subject, household, key, requestID string, _ financecore.CreateTransactionInput) (financecore.CreateResult[financecore.Transaction], error) {
	s.remember(subject, household)
	s.lastKey = key
	s.lastRequestID = requestID
	return financecore.CreateResult[financecore.Transaction]{Value: stubTransaction(), Replayed: s.replayed}, s.err
}
func (s *financeServiceStub) UpdateTransaction(_ context.Context, subject, household, id, version, requestID string, input financecore.TransactionPatchInput) (financecore.Transaction, error) {
	s.remember(subject, household)
	s.lastObject = id
	s.lastVersion = version
	s.lastRequestID = requestID
	s.lastTransactionPatch = input
	return stubTransaction(), s.err
}
func (s *financeServiceStub) DeleteTransaction(_ context.Context, subject, household, id, version, requestID, _ string) (financecore.Transaction, error) {
	s.remember(subject, household)
	s.lastObject = id
	s.lastVersion = version
	s.lastRequestID = requestID
	return stubTransaction(), s.err
}
func (s *financeServiceStub) RestoreTransaction(_ context.Context, subject, household, id, version, requestID string) (financecore.Transaction, error) {
	s.remember(subject, household)
	s.lastObject = id
	s.lastVersion = version
	s.lastRequestID = requestID
	return stubTransaction(), s.err
}
func (s *financeServiceStub) GetSummary(_ context.Context, subject, household string, _ financecore.SummaryRangeInput) (financecore.Summary, error) {
	s.remember(subject, household)
	return financecore.Summary{From: "2026-07-01", To: "2026-07-31", HouseholdTotalCents: "0", CashFlow: financecore.CashFlow{IncomeCents: "0", ExpenseCents: "0"}}, s.err
}
func (s *financeServiceStub) ListAccountBalances(_ context.Context, subject, household string, _ financecore.AccountBalanceListInput) (financecore.AccountBalancePage, error) {
	s.remember(subject, household)
	return financecore.AccountBalancePage{AccountBalances: []financecore.AccountBalance{}}, s.err
}
func (s *financeServiceStub) ListCategoryExpenses(_ context.Context, subject, household string, _ financecore.CategoryExpenseListInput) (financecore.CategoryExpensePage, error) {
	s.remember(subject, household)
	return financecore.CategoryExpensePage{ExpenseByCategory: []financecore.CategoryExpense{}}, s.err
}

func stubAccount() financecore.Account {
	return financecore.Account{ID: financeTestObject, Name: "Основной", Color: "#112233", Version: "1", CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC()}
}
func stubCategory() financecore.Category {
	return financecore.Category{ID: financeTestObject, Type: "expense", Name: "Еда", Color: "#112233", Version: "1", CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC()}
}
func stubTransaction() financecore.Transaction {
	return financecore.Transaction{ID: financeTestObject, Type: "expense", AccountID: financeTestObject, AmountCents: "123", EventDate: "2026-07-15", Version: "1", CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC()}
}

func newFinanceHTTPServer(logOutput io.Writer, service *financeServiceStub) *http.Server {
	verifier := &verifierStub{}
	return New(testConfig(), databaseStub{}, verifier, &householdServiceStub{}, service, testLogger(logOutput))
}
func financeRequest(method, path, body string) *http.Request {
	request := authenticatedRequest(method, path, body)
	return request
}
func financeBase(path string) string {
	return "/api/v1/households/" + financeTestHousehold + "/finance" + path
}

func TestFinanceRouteTableAndAuthentication(t *testing.T) {
	service := &financeServiceStub{}
	server := newFinanceHTTPServer(io.Discard, service)
	routes := []struct {
		method, path, body string
		headers            map[string]string
		want               int
	}{
		{http.MethodGet, "/accounts", "", nil, 200}, {http.MethodPost, "/accounts", `{"name":"A","color":"#112233"}`, map[string]string{"Idempotency-Key": "k"}, 201}, {http.MethodPatch, "/accounts/" + financeTestObject, `{"name":"B"}`, map[string]string{"If-Match": `"v1"`}, 200}, {http.MethodPost, "/accounts/" + financeTestObject + "/archive", `{}`, map[string]string{"If-Match": `"v1"`}, 200}, {http.MethodPost, "/accounts/" + financeTestObject + "/restore", `{}`, map[string]string{"If-Match": `"v1"`}, 200},
		{http.MethodGet, "/categories?type=expense", "", nil, 200}, {http.MethodPost, "/categories", `{"type":"expense","name":"A","color":"#112233"}`, map[string]string{"Idempotency-Key": "k"}, 201}, {http.MethodPatch, "/categories/" + financeTestObject, `{"name":"B"}`, map[string]string{"If-Match": `"v1"`}, 200}, {http.MethodPost, "/categories/" + financeTestObject + "/archive", `{}`, map[string]string{"If-Match": `"v1"`}, 200}, {http.MethodPost, "/categories/" + financeTestObject + "/restore", `{}`, map[string]string{"If-Match": `"v1"`}, 200},
		{http.MethodGet, "/transactions", "", nil, 200}, {http.MethodPost, "/transactions", `{"type":"expense","accountId":"` + financeTestObject + `","categoryId":"` + financeTestObject + `","amountCents":"1","eventDate":"2026-07-15"}`, map[string]string{"Idempotency-Key": "k"}, 201}, {http.MethodPatch, "/transactions/" + financeTestObject, `{"note":"x"}`, map[string]string{"If-Match": `"v1"`}, 200}, {http.MethodPost, "/transactions/" + financeTestObject + "/delete", `{"reason":"x"}`, map[string]string{"If-Match": `"v1"`}, 200}, {http.MethodPost, "/transactions/" + financeTestObject + "/restore", `{}`, map[string]string{"If-Match": `"v1"`}, 200},
		{http.MethodGet, "/summary?from=2026-07-01&to=2026-07-31", "", nil, 200}, {http.MethodGet, "/summary/account-balances?to=2026-07-31", "", nil, 200}, {http.MethodGet, "/summary/expense-by-category?from=2026-07-01&to=2026-07-31", "", nil, 200},
	}
	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			unauth := httptest.NewRecorder()
			server.Handler.ServeHTTP(unauth, httptest.NewRequest(route.method, financeBase(route.path), nil))
			if unauth.Code != http.StatusUnauthorized {
				t.Fatalf("unauth status=%d", unauth.Code)
			}
			request := financeRequest(route.method, financeBase(route.path), route.body)
			for key, value := range route.headers {
				request.Header.Set(key, value)
			}
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, request)
			if recorder.Code != route.want {
				t.Fatalf("status=%d want=%d body=%q", recorder.Code, route.want, recorder.Body.String())
			}
		})
	}
}

func TestFinanceStrictQueryBodyAndHeaders(t *testing.T) {
	server := newFinanceHTTPServer(io.Discard, &financeServiceStub{})
	tests := []struct {
		name, method, path, body string
		headers                  map[string][]string
		want                     int
	}{
		{"unknown query", http.MethodGet, "/accounts?unknown=1", "", nil, 400}, {"duplicate query", http.MethodGet, "/accounts?state=active&state=all", "", nil, 400}, {"blank query", http.MethodGet, "/transactions?type=", "", nil, 400}, {"required type", http.MethodGet, "/categories", "", nil, 400}, {"required summary date", http.MethodGet, "/summary?from=2026-07-01", "", nil, 400}, {"cursor too long", http.MethodGet, "/accounts?cursor=" + strings.Repeat("a", 513), "", nil, 400}, {"non get query", http.MethodPost, "/accounts?x=1", `{}`, nil, 400},
		{"duplicate JSON", http.MethodPost, "/accounts", `{"name":"A","name":"B","color":"#112233"}`, map[string][]string{"Idempotency-Key": {"k"}}, 400}, {"trailing JSON", http.MethodPost, "/accounts", `{} {}`, map[string][]string{"Idempotency-Key": {"k"}}, 400}, {"unknown JSON", http.MethodPost, "/accounts", `{"unknown":1}`, map[string][]string{"Idempotency-Key": {"k"}}, 400}, {"action extra", http.MethodPost, "/accounts/" + financeTestObject + "/archive", `{"extra":true}`, map[string][]string{"If-Match": {`"v1"`}}, 400}, {"action missing body", http.MethodPost, "/accounts/" + financeTestObject + "/archive", "", map[string][]string{"If-Match": {`"v1"`}}, 400}, {"patch null nonnullable", http.MethodPatch, "/accounts/" + financeTestObject, `{"name":null}`, map[string][]string{"If-Match": {`"v1"`}}, 422}, {"empty patch", http.MethodPatch, "/accounts/" + financeTestObject, `{}`, map[string][]string{"If-Match": {`"v1"`}}, 422}, {"delete missing reason", http.MethodPost, "/transactions/" + financeTestObject + "/delete", `{}`, map[string][]string{"If-Match": {`"v1"`}}, 422}, {"missing key", http.MethodPost, "/accounts", `{"name":"A","color":"#112233"}`, nil, 400}, {"padded key", http.MethodPost, "/accounts", `{"name":"A","color":"#112233"}`, map[string][]string{"Idempotency-Key": {" key "}}, 400}, {"oversized key", http.MethodPost, "/accounts", `{"name":"A","color":"#112233"}`, map[string][]string{"Idempotency-Key": {strings.Repeat("k", 256)}}, 400}, {"duplicate key", http.MethodPost, "/accounts", `{"name":"A","color":"#112233"}`, map[string][]string{"Idempotency-Key": {"one", "two"}}, 400}, {"missing if match", http.MethodPatch, "/accounts/" + financeTestObject, `{"name":"A"}`, nil, 400}, {"weak if match", http.MethodPatch, "/accounts/" + financeTestObject, `{"name":"A"}`, map[string][]string{"If-Match": {"W/\"v1\""}}, 400}, {"duplicate if match", http.MethodPatch, "/accounts/" + financeTestObject, `{"name":"A"}`, map[string][]string{"If-Match": {`"v1"`, `"v2"`}}, 400},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := financeRequest(test.method, financeBase(test.path), test.body)
			for key, values := range test.headers {
				request.Header.Del(key)
				for _, value := range values {
					request.Header.Add(key, value)
				}
			}
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%q", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestFinancePatchPresenceETagReplayAndRequestID(t *testing.T) {
	service := &financeServiceStub{}
	server := newFinanceHTTPServer(io.Discard, service)
	request := financeRequest(http.MethodPatch, financeBase("/accounts/"+financeTestObject), `{"bankLabel":"","ownerUserId":null}`)
	request.Header.Set("If-Match", `"v1"`)
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, request)
	if recorder.Code != 200 || !service.lastAccountPatch.BankLabel.Present || !service.lastAccountPatch.OwnerUserID.Present || service.lastAccountPatch.OwnerUserID.Value != nil || service.lastVersion != "1" || service.lastRequestID == "" || recorder.Header().Get("ETag") != `"v1"` {
		t.Fatalf("patch lost presence/header: status=%d service=%#v headers=%v", recorder.Code, service, recorder.Header())
	}
	service.replayed = true
	create := financeRequest(http.MethodPost, financeBase("/transactions"), `{"type":"expense","accountId":"`+financeTestObject+`","categoryId":"`+financeTestObject+`","amountCents":"123","eventDate":"2026-07-15"}`)
	create.Header.Set("Idempotency-Key", "same")
	replay := httptest.NewRecorder()
	server.Handler.ServeHTTP(replay, create)
	if replay.Code != 200 || replay.Header().Get("Idempotency-Replayed") != "true" || replay.Header().Get("ETag") != `"v1"` || !strings.Contains(replay.Body.String(), `"amountCents":"123"`) || !strings.Contains(replay.Body.String(), `"version":"1"`) {
		t.Fatalf("replay response status=%d headers=%v body=%s", replay.Code, replay.Header(), replay.Body.String())
	}
}

func TestFinanceTypedErrorsSafeLogsAndCORS(t *testing.T) {
	mapping := []struct {
		err    error
		status int
		code   string
	}{{financecore.ErrInvalidQuery, 400, "invalid_query"}, {financecore.ErrValidation, 422, "validation_failed"}, {financecore.ErrForbidden, 403, "forbidden"}, {financecore.ErrNotFound, 404, "not_found"}, {financecore.ErrIdempotency, 409, "idempotency_conflict"}, {financecore.ErrVersionConflict, 409, "version_conflict"}, {financecore.ErrVersionExhausted, 409, "version_exhausted"}, {financecore.ErrSystemImmutable, 409, "system_resource_immutable"}, {financecore.ErrConflict, 409, "state_conflict"}, {errors.New("sql secret"), 500, "internal_error"}}
	for _, test := range mapping {
		service := &financeServiceStub{err: test.err}
		server := newFinanceHTTPServer(io.Discard, service)
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, financeRequest(http.MethodGet, financeBase("/accounts"), ""))
		if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), `"code":"`+test.code+`"`) || strings.Contains(recorder.Body.String(), "sql secret") {
			t.Fatalf("mapping %v status=%d body=%s", test.err, recorder.Code, recorder.Body.String())
		}
	}
	var logs strings.Builder
	server := newFinanceHTTPServer(&logs, &financeServiceStub{})
	secretID := "44000000-0000-4000-8000-000000000099"
	request := financeRequest(http.MethodGet, "/api/v1/households/"+secretID+"/finance/transactions?cursor=secret-cursor", "")
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, request)
	if strings.Contains(logs.String(), secretID) || strings.Contains(logs.String(), "secret-cursor") || !strings.Contains(logs.String(), "/api/v1/households/{id}/finance/transactions") {
		t.Fatalf("unsafe log: %s", logs.String())
	}
	preflight := httptest.NewRequest(http.MethodOptions, financeBase("/accounts"), nil)
	preflight.Header.Set("Origin", "https://app.test")
	preflight.Header.Set("Access-Control-Request-Method", "PATCH")
	preflight.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type, Idempotency-Key, If-Match")
	cors := httptest.NewRecorder()
	server.Handler.ServeHTTP(cors, preflight)
	if cors.Code != 204 || !strings.Contains(cors.Header().Get("Access-Control-Allow-Headers"), "If-Match") || !strings.Contains(cors.Header().Get("Access-Control-Expose-Headers"), "ETag") || !strings.Contains(cors.Header().Get("Access-Control-Expose-Headers"), "Idempotency-Replayed") {
		t.Fatalf("finance CORS headers=%v status=%d", cors.Header(), cors.Code)
	}
}
