package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"finance/backend/internal/auth"
	"finance/backend/internal/households"
)

const maxJSONBodyBytes = 64 << 10

type HouseholdService interface {
	Bootstrap(context.Context, string, string) (households.BootstrapResult, error)
	GetMe(context.Context, string) (households.User, error)
	UpdateProfile(context.Context, string, households.UpdateProfileInput) (households.User, error)
	ListHouseholds(context.Context, string) ([]households.Household, error)
	CreateHousehold(context.Context, string, string, string) (households.Household, error)
	GetHousehold(context.Context, string, string) (households.Household, error)
	UpdateHousehold(context.Context, string, string, string) (households.Household, error)
	ListMembers(context.Context, string, string) ([]households.Member, error)
	UpdateMember(context.Context, string, string, string, households.UpdateMemberInput) (households.Member, error)
	CreateInvitation(context.Context, string, string, string, int, string) (households.Invitation, error)
	AcceptInvitation(context.Context, string, string) (households.Household, error)
	RevokeInvitation(context.Context, string, string, string) error
}

func (app *application) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		values := request.Header.Values("Authorization")
		if len(values) != 1 {
			app.securityFailure(request, "authorization_ambiguous")
			app.unauthorized(writer)
			return
		}
		parts := strings.Fields(values[0])
		if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
			app.securityFailure(request, "authorization_malformed")
			app.unauthorized(writer)
			return
		}
		identity, err := app.verifier.Verify(request.Context(), parts[1])
		if err != nil {
			app.securityFailure(request, "authentication_rejected")
			app.unauthorized(writer)
			return
		}
		lease, retryAfter, err := app.apiLimiter.acquireSubject(identity.Subject)
		if err != nil {
			app.rateLimited(writer, request, retryAfter, "subject_limited")
			return
		}
		defer lease.release()
		ctx := context.WithValue(request.Context(), identityKey, identity)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (app *application) unauthorized(writer http.ResponseWriter) {
	writer.Header().Set("WWW-Authenticate", "Bearer")
	app.writeError(writer, http.StatusUnauthorized, "unauthorized", "Требуется действительная авторизация")
}

func identityFromContext(ctx context.Context) (auth.Identity, bool) {
	identity, ok := ctx.Value(identityKey).(auth.Identity)
	return identity, ok
}

func splitAPIPath(path string) []string {
	trimmed := strings.TrimPrefix(path, "/api/v1")
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return nil
	}
	segments := strings.Split(trimmed, "/")
	for _, segment := range segments {
		if segment == "" {
			return nil
		}
	}
	return segments
}

func (app *application) apiV1(writer http.ResponseWriter, request *http.Request) {
	identity, ok := identityFromContext(request.Context())
	if !ok {
		app.unauthorized(writer)
		return
	}
	segments := splitAPIPath(request.URL.Path)
	if len(segments) == 5 && segments[0] == "households" && segments[2] == "imports" && segments[3] == "backup-v5" &&
		(segments[4] == "preview" || segments[4] == "confirm") {
		app.backupV5ImportAPI(writer, request, identity.Subject, segments[1], segments[4])
		return
	}
	if len(segments) >= 3 && segments[0] == "households" && segments[2] == "finance" {
		app.financeAPI(writer, request, identity.Subject, segments[1], segments[3:])
		return
	}
	if request.URL.RawQuery != "" {
		app.writeError(writer, http.StatusBadRequest, "invalid_request", "Query параметры не поддерживаются")
		return
	}
	switch {
	case len(segments) == 2 && segments[0] == "session" && segments[1] == "bootstrap":
		app.requireMethod(writer, request, http.MethodPost, func() { app.bootstrap(writer, request, identity.Subject) })
	case len(segments) == 1 && segments[0] == "me":
		if request.Method == http.MethodGet {
			app.me(writer, request, identity.Subject)
		} else if request.Method == http.MethodPatch {
			app.updateMe(writer, request, identity.Subject)
		} else {
			app.methodNotAllowed(writer, "GET, PATCH")
		}
	case len(segments) == 1 && segments[0] == "households":
		if request.Method == http.MethodGet {
			app.listHouseholds(writer, request, identity.Subject)
		} else if request.Method == http.MethodPost {
			app.createHousehold(writer, request, identity.Subject)
		} else {
			app.methodNotAllowed(writer, "GET, POST")
		}
	case len(segments) == 2 && segments[0] == "households":
		if request.Method == http.MethodGet {
			app.getHousehold(writer, request, identity.Subject, segments[1])
		} else if request.Method == http.MethodPatch {
			app.updateHousehold(writer, request, identity.Subject, segments[1])
		} else {
			app.methodNotAllowed(writer, "GET, PATCH")
		}
	case len(segments) == 3 && segments[0] == "households" && segments[2] == "members":
		app.requireMethod(writer, request, http.MethodGet, func() {
			app.listMembers(writer, request, identity.Subject, segments[1])
		})
	case len(segments) == 4 && segments[0] == "households" && segments[2] == "members":
		app.requireMethod(writer, request, http.MethodPatch, func() {
			app.updateMember(writer, request, identity.Subject, segments[1], segments[3])
		})
	case len(segments) == 3 && segments[0] == "households" && segments[2] == "invitations":
		app.requireMethod(writer, request, http.MethodPost, func() {
			app.createInvitation(writer, request, identity.Subject, segments[1])
		})
	case len(segments) == 2 && segments[0] == "invitations" && segments[1] == "accept":
		app.requireMethod(writer, request, http.MethodPost, func() {
			app.acceptInvitation(writer, request, identity.Subject)
		})
	case len(segments) == 5 && segments[0] == "households" && segments[2] == "invitations" && segments[4] == "revoke":
		app.requireMethod(writer, request, http.MethodPost, func() {
			app.revokeInvitation(writer, request, identity.Subject, segments[1], segments[3])
		})
	default:
		app.notFound(writer, request)
	}
}

func (app *application) requireMethod(
	writer http.ResponseWriter,
	request *http.Request,
	method string,
	handler func(),
) {
	if request.Method != method {
		app.methodNotAllowed(writer, method)
		return
	}
	handler()
}

func (app *application) methodNotAllowed(writer http.ResponseWriter, allow string) {
	writer.Header().Set("Allow", allow)
	app.writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "Метод не поддерживается")
}

func (app *application) bootstrap(writer http.ResponseWriter, request *http.Request, subject string) {
	var input struct {
		DisplayName string `json:"displayName"`
	}
	if !app.decodeBody(writer, request, &input) {
		return
	}
	result, err := app.households.Bootstrap(request.Context(), subject, input.DisplayName)
	app.writeServiceResult(writer, http.StatusOK, result, err)
}

func (app *application) me(writer http.ResponseWriter, request *http.Request, subject string) {
	result, err := app.households.GetMe(request.Context(), subject)
	app.writeServiceResult(writer, http.StatusOK, result, err)
}

func (app *application) updateMe(writer http.ResponseWriter, request *http.Request, subject string) {
	var input struct {
		DisplayName         *string `json:"displayName"`
		UsageMode           *string `json:"usageMode"`
		OnboardingCompleted *bool   `json:"onboardingCompleted"`
		PrimaryCurrencyCode *string `json:"primaryCurrencyCode"`
	}
	if !app.decodeBody(writer, request, &input) {
		return
	}
	result, err := app.households.UpdateProfile(request.Context(), subject, households.UpdateProfileInput{
		DisplayName: input.DisplayName, UsageMode: input.UsageMode,
		OnboardingCompleted: input.OnboardingCompleted, PrimaryCurrencyCode: input.PrimaryCurrencyCode,
	})
	app.writeServiceResult(writer, http.StatusOK, result, err)
}

func (app *application) listHouseholds(writer http.ResponseWriter, request *http.Request, subject string) {
	result, err := app.households.ListHouseholds(request.Context(), subject)
	app.writeServiceResult(writer, http.StatusOK, map[string]any{"households": result}, err)
}

func (app *application) createHousehold(writer http.ResponseWriter, request *http.Request, subject string) {
	var input struct {
		Name string `json:"name"`
	}
	if !app.decodeBody(writer, request, &input) {
		return
	}
	idempotencyKey, ok := app.idempotencyKey(writer, request)
	if !ok {
		return
	}
	result, err := app.households.CreateHousehold(request.Context(), subject, input.Name, idempotencyKey)
	app.writeServiceResult(writer, http.StatusCreated, result, err)
}

func (app *application) getHousehold(writer http.ResponseWriter, request *http.Request, subject, householdID string) {
	result, err := app.households.GetHousehold(request.Context(), subject, householdID)
	app.writeServiceResult(writer, http.StatusOK, result, err)
}

func (app *application) updateHousehold(writer http.ResponseWriter, request *http.Request, subject, householdID string) {
	var input struct {
		Name string `json:"name"`
	}
	if !app.decodeBody(writer, request, &input) {
		return
	}
	result, err := app.households.UpdateHousehold(request.Context(), subject, householdID, input.Name)
	app.writeServiceResult(writer, http.StatusOK, result, err)
}

func (app *application) listMembers(writer http.ResponseWriter, request *http.Request, subject, householdID string) {
	result, err := app.households.ListMembers(request.Context(), subject, householdID)
	app.writeServiceResult(writer, http.StatusOK, map[string]any{"members": result}, err)
}

func (app *application) updateMember(
	writer http.ResponseWriter,
	request *http.Request,
	subject, householdID, userID string,
) {
	var input struct {
		Role   *string `json:"role"`
		Status *string `json:"status"`
	}
	if !app.decodeBody(writer, request, &input) {
		return
	}
	result, err := app.households.UpdateMember(request.Context(), subject, householdID, userID, households.UpdateMemberInput{
		Role: input.Role, Status: input.Status,
	})
	app.writeServiceResult(writer, http.StatusOK, result, err)
}

func (app *application) createInvitation(
	writer http.ResponseWriter,
	request *http.Request,
	subject, householdID string,
) {
	var input struct {
		Role       string `json:"role"`
		TTLSeconds int    `json:"ttlSeconds"`
	}
	if !app.decodeBody(writer, request, &input) {
		return
	}
	idempotencyKey, ok := app.idempotencyKey(writer, request)
	if !ok {
		return
	}
	result, err := app.households.CreateInvitation(
		request.Context(), subject, householdID, input.Role, input.TTLSeconds, idempotencyKey,
	)
	app.writeServiceResult(writer, http.StatusCreated, result, err)
}

func (app *application) acceptInvitation(writer http.ResponseWriter, request *http.Request, subject string) {
	var input struct {
		Token string `json:"token"`
	}
	if !app.decodeBody(writer, request, &input) {
		return
	}
	result, err := app.households.AcceptInvitation(request.Context(), subject, input.Token)
	app.writeServiceResult(writer, http.StatusOK, result, err)
}

func (app *application) revokeInvitation(
	writer http.ResponseWriter,
	request *http.Request,
	subject, householdID, invitationID string,
) {
	err := app.households.RevokeInvitation(request.Context(), subject, householdID, invitationID)
	app.writeServiceResult(writer, http.StatusOK, map[string]string{"status": "revoked"}, err)
}

func (app *application) idempotencyKey(writer http.ResponseWriter, request *http.Request) (string, bool) {
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		app.writeError(writer, http.StatusBadRequest, "invalid_request", "Требуется один Idempotency-Key")
		return "", false
	}
	return values[0], true
}

func (app *application) decodeBody(writer http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		app.writeError(writer, http.StatusUnsupportedMediaType, "unsupported_media_type", "Требуется application/json")
		return false
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			app.writeError(writer, http.StatusRequestEntityTooLarge, "body_too_large", "Тело запроса слишком большое")
		} else {
			app.writeError(writer, http.StatusBadRequest, "invalid_json", "Некорректный JSON")
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		app.writeError(writer, http.StatusBadRequest, "invalid_json", "Ожидается один JSON-объект")
		return false
	}
	return true
}

func (app *application) writeServiceResult(writer http.ResponseWriter, status int, result any, err error) {
	if err == nil {
		app.writeJSON(writer, status, result)
		return
	}
	switch {
	case errors.Is(err, households.ErrInvalid):
		app.writeError(writer, http.StatusBadRequest, "invalid_request", "Некорректные данные запроса")
	case errors.Is(err, households.ErrNotFound):
		app.writeError(writer, http.StatusNotFound, "not_found", "Ресурс не найден")
	case errors.Is(err, households.ErrForbidden):
		app.writeError(writer, http.StatusForbidden, "forbidden", "Недостаточно прав")
	case errors.Is(err, households.ErrIdempotencyReplay):
		app.writeError(writer, http.StatusConflict, "idempotency_replayed", "Запрос уже был обработан; token повторно не выдается")
	case errors.Is(err, households.ErrInvitationUnavailable):
		app.writeError(writer, http.StatusConflict, "invitation_unavailable", "Приглашение недоступно")
	case errors.Is(err, households.ErrConflict):
		app.writeError(writer, http.StatusConflict, "conflict", "Операция конфликтует с текущим состоянием")
	default:
		app.logger.Error("application request failed", "request_id", writer.Header().Get("X-Request-ID"))
		app.writeError(writer, http.StatusInternalServerError, "internal_error", "Внутренняя ошибка сервера")
	}
}
