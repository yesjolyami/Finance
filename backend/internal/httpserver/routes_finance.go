package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	financecore "finance/backend/internal/finance"
)

type FinanceService interface {
	ListAccounts(context.Context, string, string, financecore.AccountListInput) (financecore.AccountPage, error)
	CreateAccount(context.Context, string, string, string, string, financecore.CreateAccountInput) (financecore.CreateResult[financecore.Account], error)
	UpdateAccount(context.Context, string, string, string, string, string, financecore.AccountPatchInput) (financecore.Account, error)
	SetAccountArchived(context.Context, string, string, string, string, string, bool) (financecore.Account, error)
	ListCategories(context.Context, string, string, financecore.CategoryListInput) (financecore.CategoryPage, error)
	CreateCategory(context.Context, string, string, string, string, financecore.CreateCategoryInput) (financecore.CreateResult[financecore.Category], error)
	UpdateCategory(context.Context, string, string, string, string, string, financecore.CategoryPatchInput) (financecore.Category, error)
	SetCategoryArchived(context.Context, string, string, string, string, string, bool) (financecore.Category, error)
	ListTransactions(context.Context, string, string, financecore.TransactionListInput) (financecore.TransactionPage, error)
	CreateTransaction(context.Context, string, string, string, string, financecore.CreateTransactionInput) (financecore.CreateResult[financecore.Transaction], error)
	UpdateTransaction(context.Context, string, string, string, string, string, financecore.TransactionPatchInput) (financecore.Transaction, error)
	DeleteTransaction(context.Context, string, string, string, string, string, string) (financecore.Transaction, error)
	RestoreTransaction(context.Context, string, string, string, string, string) (financecore.Transaction, error)
	GetSummary(context.Context, string, string, financecore.SummaryRangeInput) (financecore.Summary, error)
	ListAccountBalances(context.Context, string, string, financecore.AccountBalanceListInput) (financecore.AccountBalancePage, error)
	ListCategoryExpenses(context.Context, string, string, financecore.CategoryExpenseListInput) (financecore.CategoryExpensePage, error)
}

var ifMatchPattern = regexp.MustCompile(`^"v([1-9][0-9]*)"$`)

func (app *application) financeAPI(writer http.ResponseWriter, request *http.Request, subject, householdID string, path []string) {
	if request.Method != http.MethodGet && request.URL.RawQuery != "" {
		app.writeError(writer, http.StatusBadRequest, "invalid_query", "Query параметры не поддерживаются")
		return
	}
	switch {
	case len(path) == 1 && path[0] == "accounts":
		if request.Method == http.MethodGet {
			app.financeListAccounts(writer, request, subject, householdID)
			return
		}
		if request.Method == http.MethodPost {
			app.financeCreateAccount(writer, request, subject, householdID)
			return
		}
		app.methodNotAllowed(writer, "GET, POST")
	case len(path) == 2 && path[0] == "accounts":
		app.requireMethod(writer, request, http.MethodPatch, func() { app.financeUpdateAccount(writer, request, subject, householdID, path[1]) })
	case len(path) == 3 && path[0] == "accounts" && (path[2] == "archive" || path[2] == "restore"):
		app.requireMethod(writer, request, http.MethodPost, func() { app.financeAccountState(writer, request, subject, householdID, path[1], path[2] == "archive") })
	case len(path) == 1 && path[0] == "categories":
		if request.Method == http.MethodGet {
			app.financeListCategories(writer, request, subject, householdID)
			return
		}
		if request.Method == http.MethodPost {
			app.financeCreateCategory(writer, request, subject, householdID)
			return
		}
		app.methodNotAllowed(writer, "GET, POST")
	case len(path) == 2 && path[0] == "categories":
		app.requireMethod(writer, request, http.MethodPatch, func() { app.financeUpdateCategory(writer, request, subject, householdID, path[1]) })
	case len(path) == 3 && path[0] == "categories" && (path[2] == "archive" || path[2] == "restore"):
		app.requireMethod(writer, request, http.MethodPost, func() { app.financeCategoryState(writer, request, subject, householdID, path[1], path[2] == "archive") })
	case len(path) == 1 && path[0] == "transactions":
		if request.Method == http.MethodGet {
			app.financeListTransactions(writer, request, subject, householdID)
			return
		}
		if request.Method == http.MethodPost {
			app.financeCreateTransaction(writer, request, subject, householdID)
			return
		}
		app.methodNotAllowed(writer, "GET, POST")
	case len(path) == 2 && path[0] == "transactions":
		app.requireMethod(writer, request, http.MethodPatch, func() { app.financeUpdateTransaction(writer, request, subject, householdID, path[1]) })
	case len(path) == 3 && path[0] == "transactions" && path[2] == "delete":
		app.requireMethod(writer, request, http.MethodPost, func() { app.financeDeleteTransaction(writer, request, subject, householdID, path[1]) })
	case len(path) == 3 && path[0] == "transactions" && path[2] == "restore":
		app.requireMethod(writer, request, http.MethodPost, func() { app.financeRestoreTransaction(writer, request, subject, householdID, path[1]) })
	case len(path) == 1 && path[0] == "summary":
		app.requireMethod(writer, request, http.MethodGet, func() { app.financeSummary(writer, request, subject, householdID) })
	case len(path) == 2 && path[0] == "summary" && path[1] == "account-balances":
		app.requireMethod(writer, request, http.MethodGet, func() { app.financeAccountBalances(writer, request, subject, householdID) })
	case len(path) == 2 && path[0] == "summary" && path[1] == "expense-by-category":
		app.requireMethod(writer, request, http.MethodGet, func() { app.financeCategoryExpenses(writer, request, subject, householdID) })
	default:
		app.notFound(writer, request)
	}
}

type accountBody struct {
	Name             string  `json:"name"`
	Color            string  `json:"color"`
	SortOrder        int     `json:"sortOrder"`
	AccountType      string  `json:"accountType"`
	BankLabel        string  `json:"bankLabel"`
	LegacyOwnerLabel string  `json:"legacyOwnerLabel"`
	OwnerUserID      *string `json:"ownerUserId"`
}
type categoryBody struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	SortOrder int    `json:"sortOrder"`
}
type transactionBody struct {
	Type                string  `json:"type"`
	AccountID           string  `json:"accountId"`
	ToAccountID         *string `json:"toAccountId"`
	CategoryID          *string `json:"categoryId"`
	AmountCents         string  `json:"amountCents"`
	EventDate           string  `json:"eventDate"`
	Note                string  `json:"note"`
	IsBalanceAdjustment bool    `json:"isBalanceAdjustment"`
}

type jsonField[T any] struct {
	Present bool
	Null    bool
	Value   T
}

func (field *jsonField[T]) UnmarshalJSON(data []byte) error {
	field.Present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		field.Null = true
		return nil
	}
	return json.Unmarshal(data, &field.Value)
}

type accountPatchBody struct {
	Name             jsonField[string] `json:"name"`
	Color            jsonField[string] `json:"color"`
	SortOrder        jsonField[int]    `json:"sortOrder"`
	AccountType      jsonField[string] `json:"accountType"`
	BankLabel        jsonField[string] `json:"bankLabel"`
	LegacyOwnerLabel jsonField[string] `json:"legacyOwnerLabel"`
	OwnerUserID      jsonField[string] `json:"ownerUserId"`
}
type categoryPatchBody struct {
	Name      jsonField[string] `json:"name"`
	Color     jsonField[string] `json:"color"`
	SortOrder jsonField[int]    `json:"sortOrder"`
}
type transactionPatchBody struct {
	Type                jsonField[string] `json:"type"`
	AccountID           jsonField[string] `json:"accountId"`
	ToAccountID         jsonField[string] `json:"toAccountId"`
	CategoryID          jsonField[string] `json:"categoryId"`
	AmountCents         jsonField[string] `json:"amountCents"`
	EventDate           jsonField[string] `json:"eventDate"`
	Note                jsonField[string] `json:"note"`
	IsBalanceAdjustment jsonField[bool]   `json:"isBalanceAdjustment"`
}

func (app *application) financeListAccounts(w http.ResponseWriter, r *http.Request, s, h string) {
	q, ok := app.financeQuery(w, r, map[string]bool{"state": false, "limit": false, "cursor": false})
	if !ok {
		return
	}
	limit, ok := queryLimit(w, app, q)
	if !ok {
		return
	}
	result, err := app.finance.ListAccounts(r.Context(), s, h, financecore.AccountListInput{State: first(q, "state"), Limit: limit, Cursor: first(q, "cursor")})
	app.writeFinanceResult(w, r, http.StatusOK, result, err)
}
func (app *application) financeCreateAccount(w http.ResponseWriter, r *http.Request, s, h string) {
	var body accountBody
	if !app.decodeFinanceBody(w, r, &body) {
		return
	}
	key, ok := app.financeIdempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := app.finance.CreateAccount(r.Context(), s, h, key, requestIDFromContext(r.Context()), financecore.CreateAccountInput{Name: body.Name, Color: body.Color, SortOrder: body.SortOrder, AccountType: body.AccountType, BankLabel: body.BankLabel, LegacyOwnerLabel: body.LegacyOwnerLabel, OwnerUserID: body.OwnerUserID})
	if err == nil {
		setEntityHeaders(w, result.Value.Version, result.Replayed)
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	app.writeFinanceResult(w, r, status, result.Value, err)
}
func (app *application) financeUpdateAccount(w http.ResponseWriter, r *http.Request, s, h, id string) {
	var body accountPatchBody
	if !app.decodeFinanceBody(w, r, &body) {
		return
	}
	version, ok := app.ifMatch(w, r)
	if !ok {
		return
	}
	input, ok := accountPatchInput(body)
	if !ok {
		app.financeValidationError(w)
		return
	}
	result, err := app.finance.UpdateAccount(r.Context(), s, h, id, version, requestIDFromContext(r.Context()), input)
	if err == nil {
		setEntityHeaders(w, result.Version, false)
	}
	app.writeFinanceResult(w, r, http.StatusOK, result, err)
}
func (app *application) financeAccountState(w http.ResponseWriter, r *http.Request, s, h, id string, archived bool) {
	if !app.decodeFinanceBody(w, r, &struct{}{}) {
		return
	}
	version, ok := app.ifMatch(w, r)
	if !ok {
		return
	}
	result, err := app.finance.SetAccountArchived(r.Context(), s, h, id, version, requestIDFromContext(r.Context()), archived)
	if err == nil {
		setEntityHeaders(w, result.Version, false)
	}
	app.writeFinanceResult(w, r, http.StatusOK, result, err)
}

func (app *application) financeListCategories(w http.ResponseWriter, r *http.Request, s, h string) {
	q, ok := app.financeQuery(w, r, map[string]bool{"type": true, "state": false, "limit": false, "cursor": false})
	if !ok {
		return
	}
	limit, ok := queryLimit(w, app, q)
	if !ok {
		return
	}
	result, err := app.finance.ListCategories(r.Context(), s, h, financecore.CategoryListInput{Type: first(q, "type"), State: first(q, "state"), Limit: limit, Cursor: first(q, "cursor")})
	app.writeFinanceResult(w, r, http.StatusOK, result, err)
}
func (app *application) financeCreateCategory(w http.ResponseWriter, r *http.Request, s, h string) {
	var body categoryBody
	if !app.decodeFinanceBody(w, r, &body) {
		return
	}
	key, ok := app.financeIdempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := app.finance.CreateCategory(r.Context(), s, h, key, requestIDFromContext(r.Context()), financecore.CreateCategoryInput{Type: body.Type, Name: body.Name, Color: body.Color, SortOrder: body.SortOrder})
	if err == nil {
		setEntityHeaders(w, result.Value.Version, result.Replayed)
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	app.writeFinanceResult(w, r, status, result.Value, err)
}
func (app *application) financeUpdateCategory(w http.ResponseWriter, r *http.Request, s, h, id string) {
	var body categoryPatchBody
	if !app.decodeFinanceBody(w, r, &body) {
		return
	}
	version, ok := app.ifMatch(w, r)
	if !ok {
		return
	}
	input, ok := categoryPatchInput(body)
	if !ok {
		app.financeValidationError(w)
		return
	}
	result, err := app.finance.UpdateCategory(r.Context(), s, h, id, version, requestIDFromContext(r.Context()), input)
	if err == nil {
		setEntityHeaders(w, result.Version, false)
	}
	app.writeFinanceResult(w, r, http.StatusOK, result, err)
}
func (app *application) financeCategoryState(w http.ResponseWriter, r *http.Request, s, h, id string, archived bool) {
	if !app.decodeFinanceBody(w, r, &struct{}{}) {
		return
	}
	version, ok := app.ifMatch(w, r)
	if !ok {
		return
	}
	result, err := app.finance.SetCategoryArchived(r.Context(), s, h, id, version, requestIDFromContext(r.Context()), archived)
	if err == nil {
		setEntityHeaders(w, result.Version, false)
	}
	app.writeFinanceResult(w, r, http.StatusOK, result, err)
}

func (app *application) financeListTransactions(w http.ResponseWriter, r *http.Request, s, h string) {
	q, ok := app.financeQuery(w, r, map[string]bool{"from": false, "to": false, "type": false, "accountId": false, "categoryId": false, "state": false, "limit": false, "cursor": false})
	if !ok {
		return
	}
	limit, ok := queryLimit(w, app, q)
	if !ok {
		return
	}
	result, err := app.finance.ListTransactions(r.Context(), s, h, financecore.TransactionListInput{From: first(q, "from"), To: first(q, "to"), Type: first(q, "type"), AccountID: first(q, "accountId"), CategoryID: first(q, "categoryId"), State: first(q, "state"), Limit: limit, Cursor: first(q, "cursor")})
	app.writeFinanceResult(w, r, http.StatusOK, result, err)
}
func (app *application) financeCreateTransaction(w http.ResponseWriter, r *http.Request, s, h string) {
	var body transactionBody
	if !app.decodeFinanceBody(w, r, &body) {
		return
	}
	key, ok := app.financeIdempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := app.finance.CreateTransaction(r.Context(), s, h, key, requestIDFromContext(r.Context()), financecore.CreateTransactionInput{Type: body.Type, AccountID: body.AccountID, ToAccountID: body.ToAccountID, CategoryID: body.CategoryID, AmountCents: body.AmountCents, EventDate: body.EventDate, Note: body.Note, IsBalanceAdjustment: body.IsBalanceAdjustment})
	if err == nil {
		setEntityHeaders(w, result.Value.Version, result.Replayed)
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	app.writeFinanceResult(w, r, status, result.Value, err)
}
func (app *application) financeUpdateTransaction(w http.ResponseWriter, r *http.Request, s, h, id string) {
	var body transactionPatchBody
	if !app.decodeFinanceBody(w, r, &body) {
		return
	}
	version, ok := app.ifMatch(w, r)
	if !ok {
		return
	}
	input, ok := transactionPatchInput(body)
	if !ok {
		app.financeValidationError(w)
		return
	}
	result, err := app.finance.UpdateTransaction(r.Context(), s, h, id, version, requestIDFromContext(r.Context()), input)
	if err == nil {
		setEntityHeaders(w, result.Version, false)
	}
	app.writeFinanceResult(w, r, http.StatusOK, result, err)
}
func (app *application) financeDeleteTransaction(w http.ResponseWriter, r *http.Request, s, h, id string) {
	var body struct {
		Reason string `json:"reason"`
	}
	if !app.decodeFinanceBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		app.financeValidationError(w)
		return
	}
	version, ok := app.ifMatch(w, r)
	if !ok {
		return
	}
	result, err := app.finance.DeleteTransaction(r.Context(), s, h, id, version, requestIDFromContext(r.Context()), body.Reason)
	if err == nil {
		setEntityHeaders(w, result.Version, false)
	}
	app.writeFinanceResult(w, r, http.StatusOK, result, err)
}
func (app *application) financeRestoreTransaction(w http.ResponseWriter, r *http.Request, s, h, id string) {
	if !app.decodeFinanceBody(w, r, &struct{}{}) {
		return
	}
	version, ok := app.ifMatch(w, r)
	if !ok {
		return
	}
	result, err := app.finance.RestoreTransaction(r.Context(), s, h, id, version, requestIDFromContext(r.Context()))
	if err == nil {
		setEntityHeaders(w, result.Version, false)
	}
	app.writeFinanceResult(w, r, http.StatusOK, result, err)
}

func (app *application) financeSummary(w http.ResponseWriter, r *http.Request, s, h string) {
	q, ok := app.financeQuery(w, r, map[string]bool{"from": true, "to": true})
	if !ok {
		return
	}
	result, err := app.finance.GetSummary(r.Context(), s, h, financecore.SummaryRangeInput{From: first(q, "from"), To: first(q, "to")})
	app.writeFinanceResult(w, r, http.StatusOK, result, err)
}
func (app *application) financeAccountBalances(w http.ResponseWriter, r *http.Request, s, h string) {
	q, ok := app.financeQuery(w, r, map[string]bool{"to": true, "limit": false, "cursor": false})
	if !ok {
		return
	}
	limit, ok := queryLimit(w, app, q)
	if !ok {
		return
	}
	result, err := app.finance.ListAccountBalances(r.Context(), s, h, financecore.AccountBalanceListInput{To: first(q, "to"), Limit: limit, Cursor: first(q, "cursor")})
	app.writeFinanceResult(w, r, http.StatusOK, result, err)
}
func (app *application) financeCategoryExpenses(w http.ResponseWriter, r *http.Request, s, h string) {
	q, ok := app.financeQuery(w, r, map[string]bool{"from": true, "to": true, "limit": false, "cursor": false})
	if !ok {
		return
	}
	limit, ok := queryLimit(w, app, q)
	if !ok {
		return
	}
	result, err := app.finance.ListCategoryExpenses(r.Context(), s, h, financecore.CategoryExpenseListInput{From: first(q, "from"), To: first(q, "to"), Limit: limit, Cursor: first(q, "cursor")})
	app.writeFinanceResult(w, r, http.StatusOK, result, err)
}

func (app *application) financeQuery(w http.ResponseWriter, r *http.Request, allowed map[string]bool) (url.Values, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		app.financeQueryError(w)
		return nil, false
	}
	for key, list := range values {
		required, ok := allowed[key]
		_ = required
		if !ok || len(list) != 1 || list[0] == "" {
			app.financeQueryError(w)
			return nil, false
		}
		if key == "cursor" && len(list[0]) > financecore.MaximumCursorBytes {
			app.financeQueryError(w)
			return nil, false
		}
	}
	for key, required := range allowed {
		if required && len(values[key]) != 1 {
			app.financeQueryError(w)
			return nil, false
		}
	}
	return values, true
}
func queryLimit(w http.ResponseWriter, app *application, q url.Values) (int, bool) {
	raw := first(q, "limit")
	if raw == "" {
		return 0, true
	}
	if len(raw) > 3 || (len(raw) > 1 && raw[0] == '0') {
		app.financeQueryError(w)
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > financecore.MaximumListLimit {
		app.financeQueryError(w)
		return 0, false
	}
	return value, true
}
func first(values url.Values, key string) string {
	if len(values[key]) == 1 {
		return values[key][0]
	}
	return ""
}
func (app *application) financeQueryError(w http.ResponseWriter) {
	app.writeError(w, http.StatusBadRequest, "invalid_query", "Некорректные query параметры")
}
func (app *application) financeValidationError(w http.ResponseWriter) {
	app.writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Данные не соответствуют финансовому контракту")
}

func (app *application) financeIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || values[0] == "" || values[0] != strings.TrimSpace(values[0]) ||
		!utf8.ValidString(values[0]) || utf8.RuneCountInString(values[0]) > 255 {
		app.writeError(w, http.StatusBadRequest, "invalid_header", "Требуется один корректный Idempotency-Key")
		return "", false
	}
	return values[0], true
}
func (app *application) ifMatch(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("If-Match")
	if len(values) != 1 {
		app.writeError(w, http.StatusBadRequest, "invalid_header", "Требуется один корректный If-Match")
		return "", false
	}
	matches := ifMatchPattern.FindStringSubmatch(values[0])
	if len(matches) != 2 {
		app.writeError(w, http.StatusBadRequest, "invalid_header", "Требуется один корректный If-Match")
		return "", false
	}
	if _, err := strconv.ParseInt(matches[1], 10, 64); err != nil {
		app.writeError(w, http.StatusBadRequest, "invalid_header", "Требуется один корректный If-Match")
		return "", false
	}
	return matches[1], true
}
func setEntityHeaders(w http.ResponseWriter, version string, replayed bool) {
	w.Header().Set("ETag", `"v`+version+`"`)
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
}

func (app *application) decodeFinanceBody(w http.ResponseWriter, r *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		app.writeError(w, http.StatusBadRequest, "invalid_json", "Требуется application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			app.writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "Тело запроса слишком большое")
		} else {
			app.writeError(w, http.StatusBadRequest, "invalid_json", "Некорректный JSON")
		}
		return false
	}
	if err := validateJSONObject(data); err != nil {
		app.writeError(w, http.StatusBadRequest, "invalid_json", "Ожидается один JSON-объект")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		app.writeError(w, http.StatusBadRequest, "invalid_json", "Некорректный JSON")
		return false
	}
	return true
}
func validateJSONObject(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("object required")
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok || seen[key] {
			return errors.New("duplicate field")
		}
		seen[key] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return errors.New("object end")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func accountPatchInput(body accountPatchBody) (financecore.AccountPatchInput, bool) {
	if body.Name.Null || body.Color.Null || body.SortOrder.Null || body.AccountType.Null || body.BankLabel.Null || body.LegacyOwnerLabel.Null {
		return financecore.AccountPatchInput{}, false
	}
	if !body.Name.Present && !body.Color.Present && !body.SortOrder.Present && !body.AccountType.Present && !body.BankLabel.Present && !body.LegacyOwnerLabel.Present && !body.OwnerUserID.Present {
		return financecore.AccountPatchInput{}, false
	}
	var owner *string
	if body.OwnerUserID.Present && !body.OwnerUserID.Null {
		owner = &body.OwnerUserID.Value
	}
	return financecore.AccountPatchInput{Name: financecore.Field[string]{Present: body.Name.Present, Value: body.Name.Value}, Color: financecore.Field[string]{Present: body.Color.Present, Value: body.Color.Value}, SortOrder: financecore.Field[int]{Present: body.SortOrder.Present, Value: body.SortOrder.Value}, AccountType: financecore.Field[string]{Present: body.AccountType.Present, Value: body.AccountType.Value}, BankLabel: financecore.Field[string]{Present: body.BankLabel.Present, Value: body.BankLabel.Value}, LegacyOwnerLabel: financecore.Field[string]{Present: body.LegacyOwnerLabel.Present, Value: body.LegacyOwnerLabel.Value}, OwnerUserID: financecore.NullableField[string]{Present: body.OwnerUserID.Present, Value: owner}}, true
}
func categoryPatchInput(body categoryPatchBody) (financecore.CategoryPatchInput, bool) {
	if body.Name.Null || body.Color.Null || body.SortOrder.Null {
		return financecore.CategoryPatchInput{}, false
	}
	if !body.Name.Present && !body.Color.Present && !body.SortOrder.Present {
		return financecore.CategoryPatchInput{}, false
	}
	return financecore.CategoryPatchInput{Name: financecore.Field[string]{Present: body.Name.Present, Value: body.Name.Value}, Color: financecore.Field[string]{Present: body.Color.Present, Value: body.Color.Value}, SortOrder: financecore.Field[int]{Present: body.SortOrder.Present, Value: body.SortOrder.Value}}, true
}
func transactionPatchInput(body transactionPatchBody) (financecore.TransactionPatchInput, bool) {
	if body.Type.Null || body.AccountID.Null || body.AmountCents.Null || body.EventDate.Null || body.Note.Null || body.IsBalanceAdjustment.Null {
		return financecore.TransactionPatchInput{}, false
	}
	if !body.Type.Present && !body.AccountID.Present && !body.ToAccountID.Present && !body.CategoryID.Present && !body.AmountCents.Present && !body.EventDate.Present && !body.Note.Present && !body.IsBalanceAdjustment.Present {
		return financecore.TransactionPatchInput{}, false
	}
	var to, category *string
	if body.ToAccountID.Present && !body.ToAccountID.Null {
		to = &body.ToAccountID.Value
	}
	if body.CategoryID.Present && !body.CategoryID.Null {
		category = &body.CategoryID.Value
	}
	return financecore.TransactionPatchInput{Type: financecore.Field[string]{Present: body.Type.Present, Value: body.Type.Value}, AccountID: financecore.Field[string]{Present: body.AccountID.Present, Value: body.AccountID.Value}, ToAccountID: financecore.NullableField[string]{Present: body.ToAccountID.Present, Value: to}, CategoryID: financecore.NullableField[string]{Present: body.CategoryID.Present, Value: category}, AmountCents: financecore.Field[string]{Present: body.AmountCents.Present, Value: body.AmountCents.Value}, EventDate: financecore.Field[string]{Present: body.EventDate.Present, Value: body.EventDate.Value}, Note: financecore.Field[string]{Present: body.Note.Present, Value: body.Note.Value}, IsBalanceAdjustment: financecore.Field[bool]{Present: body.IsBalanceAdjustment.Present, Value: body.IsBalanceAdjustment.Value}}, true
}

func (app *application) writeFinanceResult(w http.ResponseWriter, r *http.Request, status int, result any, err error) {
	if err == nil {
		app.writeJSON(w, status, result)
		return
	}
	switch {
	case errors.Is(err, financecore.ErrInvalidQuery):
		app.writeError(w, http.StatusBadRequest, "invalid_query", "Некорректные query параметры")
	case errors.Is(err, financecore.ErrValidation):
		app.financeValidationError(w)
	case errors.Is(err, financecore.ErrForbidden):
		app.writeError(w, http.StatusForbidden, "forbidden", "Недостаточно прав")
	case errors.Is(err, financecore.ErrNotFound):
		app.writeError(w, http.StatusNotFound, "not_found", "Ресурс не найден")
	case errors.Is(err, financecore.ErrIdempotency):
		app.writeError(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key уже использован с другими данными")
	case errors.Is(err, financecore.ErrVersionConflict):
		app.writeError(w, http.StatusConflict, "version_conflict", "Ресурс был изменён")
	case errors.Is(err, financecore.ErrVersionExhausted):
		app.writeError(w, http.StatusConflict, "version_exhausted", "Версия ресурса исчерпана")
	case errors.Is(err, financecore.ErrSystemImmutable):
		app.writeError(w, http.StatusConflict, "system_resource_immutable", "Системный ресурс неизменяем")
	case errors.Is(err, financecore.ErrConflict):
		app.writeError(w, http.StatusConflict, "state_conflict", "Операция конфликтует с состоянием ресурса")
	default:
		app.logger.ErrorContext(r.Context(), "finance request failed", "request_id", requestIDFromContext(r.Context()))
		app.writeError(w, http.StatusInternalServerError, "internal_error", "Внутренняя ошибка сервера")
	}
}

func safeFinanceLogPath(segments []string) string {
	base := "/api/v1/households/{id}/finance"
	path := segments[3:]
	switch {
	case len(path) == 1 && (path[0] == "accounts" || path[0] == "categories" || path[0] == "transactions" || path[0] == "summary"):
		return base + "/" + path[0]
	case len(path) == 2 && (path[0] == "accounts" || path[0] == "categories" || path[0] == "transactions"):
		return base + "/" + path[0] + "/{id}"
	case len(path) == 3 && (path[0] == "accounts" || path[0] == "categories" || path[0] == "transactions") && (path[2] == "archive" || path[2] == "restore" || path[2] == "delete"):
		return base + "/" + path[0] + "/{id}/" + path[2]
	case len(path) == 2 && path[0] == "summary" && (path[1] == "account-balances" || path[1] == "expense-by-category"):
		return base + "/summary/" + path[1]
	default:
		return base + "/unmatched"
	}
}
