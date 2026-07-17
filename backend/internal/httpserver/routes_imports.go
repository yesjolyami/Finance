package httpserver

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"finance/backend/internal/backupv5"

	"github.com/google/uuid"
)

const (
	importBudgetMonthHeader  = "Import-Budget-Month"
	importPreviewTokenHeader = "Import-Preview-Token"
)

type PreviewImportService interface {
	Preview(context.Context, string, uuid.UUID, string, io.Reader) (backupv5.PreviewResponse, error)
}

type ConfirmImportService interface {
	Confirm(context.Context, backupv5.ConfirmInput) (backupv5.ConfirmResult, error)
}

func isBackupV5ImportPath(path string) bool {
	segments := splitAPIPath(path)
	return len(segments) == 5 && segments[0] == "households" && segments[2] == "imports" &&
		segments[3] == "backup-v5" && (segments[4] == "preview" || segments[4] == "confirm")
}

func (app *application) backupV5ImportAPI(
	writer http.ResponseWriter,
	request *http.Request,
	subject, householdIDRaw, action string,
) {
	defer request.Body.Close()
	if request.Method != http.MethodPost {
		app.methodNotAllowed(writer, http.MethodPost)
		return
	}
	if request.URL.RawQuery != "" || request.URL.Fragment != "" {
		app.importBadRequest(writer)
		return
	}
	householdID, err := uuid.Parse(householdIDRaw)
	if err != nil || householdID == uuid.Nil || householdID.String() != householdIDRaw {
		app.writeError(writer, http.StatusNotFound, "not_found", "Ресурс не найден")
		return
	}
	if !app.validateImportCommonHeaders(writer, request) {
		return
	}
	budgetMonth, ok := exactHeader(request, importBudgetMonthHeader)
	if !ok || !validBudgetMonthHeader(budgetMonth) {
		app.importBadRequest(writer)
		return
	}
	if request.ContentLength > backupv5.MaxRawBytes {
		app.writeError(writer, http.StatusRequestEntityTooLarge, "body_too_large", "Тело запроса слишком большое")
		return
	}

	switch action {
	case "preview":
		app.backupV5Preview(writer, request, subject, householdID, budgetMonth)
	case "confirm":
		app.backupV5Confirm(writer, request, subject, householdID, budgetMonth)
	default:
		app.notFound(writer, request)
	}
}

func (app *application) backupV5Preview(
	writer http.ResponseWriter,
	request *http.Request,
	subject string,
	householdID uuid.UUID,
	budgetMonth string,
) {
	if len(request.Header.Values(importPreviewTokenHeader)) != 0 || len(request.Header.Values("Idempotency-Key")) != 0 {
		app.importBadRequest(writer)
		return
	}
	if app.previewImport == nil || app.importLimiter == nil {
		app.importUnavailable(writer)
		return
	}
	lease, err := app.importLimiter.acquirePreview(subject, householdID.String())
	if err != nil {
		app.importRateLimited(writer, request)
		return
	}
	defer lease.release(false)

	result, err := app.previewImport.Preview(
		request.Context(), subject, householdID, budgetMonth,
		io.LimitReader(request.Body, backupv5.MaxRawBytes+1),
	)
	app.writeImportResult(writer, request, http.StatusOK, result, err)
}

func (app *application) backupV5Confirm(
	writer http.ResponseWriter,
	request *http.Request,
	subject string,
	householdID uuid.UUID,
	budgetMonth string,
) {
	previewToken, ok := exactOpaqueHeader(request, importPreviewTokenHeader, 128)
	if !ok {
		app.importBadRequest(writer)
		return
	}
	idempotencyKey, ok := exactOpaqueHeader(request, "Idempotency-Key", 255)
	if !ok {
		app.importBadRequest(writer)
		return
	}
	if app.confirmImport == nil || app.importLimiter == nil {
		app.importUnavailable(writer)
		return
	}
	lease, err := app.importLimiter.acquireConfirm(subject, householdID.String())
	if err != nil {
		app.importRateLimited(writer, request)
		return
	}
	replayed := false
	defer func() { lease.release(replayed) }()

	result, err := app.confirmImport.Confirm(request.Context(), backupv5.ConfirmInput{
		Subject: subject, HouseholdID: householdID, BudgetMonth: budgetMonth,
		RawJSON:      io.LimitReader(request.Body, backupv5.MaxRawBytes+1),
		PreviewToken: previewToken, IdempotencyKey: idempotencyKey,
		ServerRequestID: requestIDFromContext(request.Context()),
	})
	if err != nil {
		app.writeImportResult(writer, request, http.StatusCreated, nil, err)
		return
	}
	replayed = result.Replayed
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
		writer.Header().Set("Idempotency-Replayed", "true")
	}
	app.writeImportResult(writer, request, status, result.Response, nil)
}

func (app *application) validateImportCommonHeaders(writer http.ResponseWriter, request *http.Request) bool {
	if len(request.Header.Values("X-Request-ID")) != 0 || len(request.Header.Values("Content-Encoding")) != 0 {
		app.importBadRequest(writer)
		return false
	}
	values := request.Header.Values("Content-Type")
	if len(values) != 1 {
		app.importBadRequest(writer)
		return false
	}
	// ParseMediaType may collapse duplicate parameters into its result map.
	// The import wire contract allows at most one parameter, so reject a
	// second delimiter before normalization as well.
	if len(strings.Split(values[0], ";")) > 2 {
		app.importBadRequest(writer)
		return false
	}
	mediaType, parameters, err := mime.ParseMediaType(values[0])
	if err != nil || mediaType != "application/json" || len(parameters) > 1 {
		app.importBadRequest(writer)
		return false
	}
	if len(parameters) == 1 {
		charset, exists := parameters["charset"]
		if !exists || !strings.EqualFold(charset, "utf-8") {
			app.importBadRequest(writer)
			return false
		}
	}
	return true
}

func exactHeader(request *http.Request, name string) (string, bool) {
	values := request.Header.Values(name)
	if len(values) != 1 || values[0] == "" {
		return "", false
	}
	return values[0], true
}

func exactOpaqueHeader(request *http.Request, name string, maximumBytes int) (string, bool) {
	value, ok := exactHeader(request, name)
	if !ok || len(value) > maximumBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return "", false
	}
	for _, character := range []byte(value) {
		if character <= 0x1f || character == 0x7f {
			return "", false
		}
	}
	return value, true
}

func validBudgetMonthHeader(value string) bool {
	if len(value) != len("2006-01-02") {
		return false
	}
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Year() >= 1 && parsed.Format("2006-01-02") == value && parsed.Day() == 1
}

func (app *application) writeImportResult(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	result any,
	err error,
) {
	if err == nil {
		app.writeJSON(writer, status, result)
		return
	}
	switch {
	case errors.Is(err, backupv5.ErrInvalidJSON), errors.Is(err, backupv5.ErrSchema),
		errors.Is(err, backupv5.ErrIdempotencyKey):
		app.importBadRequest(writer)
	case errors.Is(err, backupv5.ErrLimit):
		app.writeError(writer, http.StatusRequestEntityTooLarge, "body_too_large", "Тело запроса слишком большое")
	case errors.Is(err, backupv5.ErrValue), errors.Is(err, backupv5.ErrReference),
		errors.Is(err, backupv5.ErrReconciliation):
		app.writeError(writer, http.StatusUnprocessableEntity, "import_validation_failed", "Резервная копия не прошла проверку")
	case errors.Is(err, backupv5.ErrForbidden):
		app.writeError(writer, http.StatusForbidden, "forbidden", "Недостаточно прав")
	case errors.Is(err, backupv5.ErrNotFound):
		app.writeError(writer, http.StatusNotFound, "not_found", "Ресурс не найден")
	case errors.Is(err, backupv5.ErrHouseholdNotEmpty):
		app.writeError(writer, http.StatusConflict, "household_not_empty", "Импорт доступен только для пустого пространства")
	case errors.Is(err, backupv5.ErrIdempotencyConflict):
		app.writeError(writer, http.StatusConflict, "idempotency_conflict", "Idempotency-Key уже использован с другими данными")
	case errors.Is(err, backupv5.ErrImportStateConflict):
		app.writeError(writer, http.StatusConflict, "import_state_conflict", "Состояние импорта конфликтует с запросом")
	case errors.Is(err, backupv5.ErrPreviewTokenInvalid):
		app.writeError(writer, http.StatusGone, "preview_token_invalid", "Предпросмотр импорта недоступен")
	default:
		app.logger.ErrorContext(request.Context(), "backup import request failed", "request_id", requestIDFromContext(request.Context()))
		app.writeError(writer, http.StatusInternalServerError, "internal_error", "Внутренняя ошибка сервера")
	}
}

func (app *application) importBadRequest(writer http.ResponseWriter) {
	app.writeError(writer, http.StatusBadRequest, "invalid_import_request", "Некорректный запрос импорта")
}

func (app *application) importRateLimited(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Retry-After", strconv.Itoa(int(defaultImportWindow/time.Second)))
	app.securityFailure(request, "backup_import_limited")
	app.writeError(writer, http.StatusTooManyRequests, "import_rate_limited", "Слишком много запросов импорта")
}

func (app *application) importUnavailable(writer http.ResponseWriter) {
	app.writeError(writer, http.StatusServiceUnavailable, "import_unavailable", "Импорт временно недоступен")
}
