package httpserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"finance/backend/internal/auth"
	"finance/backend/internal/config"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 120 * time.Second
	writeTimeout      = 120 * time.Second
	idleTimeout       = 60 * time.Second
	maxHeaderBytes    = 1 << 20
	databaseTimeout   = 2 * time.Second
)

type DatabaseChecker interface {
	PingContext(context.Context) error
}

type application struct {
	database        DatabaseChecker
	frontendOrigins map[string]struct{}
	verifier        auth.Verifier
	households      HouseholdService
	finance         FinanceService
	previewImport   PreviewImportService
	confirmImport   ConfirmImportService
	importLimiter   *ImportLimiter
	apiLimiter      *APILimiter
	requireOrigin   bool
	logger          *slog.Logger
}

// Option adds an optional HTTP dependency without changing the production
// constructor contract used by earlier stages.
type Option func(*application)

// WithBackupV5Imports enables the backup-v5 import routes. A nil service keeps
// its route registered but fail-closed. A nil limiter selects the bounded
// process-local default.
func WithBackupV5Imports(preview PreviewImportService, confirm ConfirmImportService, limiter *ImportLimiter) Option {
	return func(app *application) {
		app.previewImport = preview
		app.confirmImport = confirm
		if limiter == nil {
			limiter = newDefaultImportLimiter()
		}
		app.importLimiter = limiter
	}
}

// WithAPILimiter replaces the bounded process-local default for deterministic
// tests. A nil limiter remains fail-closed through the middleware.
func WithAPILimiter(limiter *APILimiter) Option {
	return func(app *application) {
		app.apiLimiter = limiter
	}
}

type contextKey string

const requestIDKey contextKey = "request_id"

const identityKey contextKey = "identity"

func New(
	config config.Config,
	database DatabaseChecker,
	verifier auth.Verifier,
	households HouseholdService,
	financeService FinanceService,
	logger *slog.Logger,
	options ...Option,
) *http.Server {
	origins := make(map[string]struct{}, len(config.FrontendOrigins))
	for _, origin := range config.FrontendOrigins {
		origins[origin] = struct{}{}
	}
	app := &application{
		database: database, frontendOrigins: origins, verifier: verifier,
		households: households, finance: financeService, logger: logger,
		apiLimiter: newDefaultAPILimiter(), requireOrigin: config.AppEnv == "production",
	}
	for _, option := range options {
		if option != nil {
			option(app)
		}
	}

	handler := app.requestID(app.logging(app.recoverPanic(app.cors(app.apiPerimeter(app.routes())))))

	return &http.Server{
		Addr:              config.Address(),
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", app.health)
	mux.Handle("/api/v1", app.authenticate(http.HandlerFunc(app.apiV1)))
	mux.Handle("/api/v1/", app.authenticate(http.HandlerFunc(app.apiV1)))
	mux.HandleFunc("/", app.notFound)
	return mux
}

func (app *application) health(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		app.writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "Метод не поддерживается")
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), databaseTimeout)
	defer cancel()

	databaseStatus := "available"
	status := "ok"
	httpStatus := http.StatusOK

	if err := app.database.PingContext(ctx); err != nil {
		databaseStatus = "unavailable"
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}

	app.writeJSON(writer, httpStatus, map[string]any{
		"status": status,
		"api": map[string]string{
			"status": "available",
		},
		"database": map[string]string{
			"status": databaseStatus,
		},
	})
}

func (app *application) notFound(writer http.ResponseWriter, _ *http.Request) {
	app.writeError(writer, http.StatusNotFound, "not_found", "Маршрут не найден")
}

func (app *application) writeError(writer http.ResponseWriter, status int, code, message string) {
	app.writeJSON(writer, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func (app *application) writeJSON(writer http.ResponseWriter, status int, payload any) {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(payload); err != nil {
		app.logger.Error("failed to encode response", "request_id", writer.Header().Get("X-Request-ID"))
		http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_, _ = writer.Write(body.Bytes())
}

func (app *application) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Add("Vary", "Origin")
		originValues := request.Header.Values("Origin")
		if len(originValues) == 0 {
			if app.requireOrigin && requiresBrowserOrigin(request) {
				app.securityFailure(request, "origin_missing")
				app.writeError(writer, http.StatusForbidden, "origin_not_allowed", "Источник запроса не разрешен")
				return
			}
			next.ServeHTTP(writer, request)
			return
		}
		if len(originValues) != 1 || originValues[0] == "" {
			app.securityFailure(request, "origin_ambiguous")
			app.writeError(writer, http.StatusForbidden, "origin_not_allowed", "Источник запроса не разрешен")
			return
		}
		origin := originValues[0]
		if _, allowed := app.frontendOrigins[origin]; !allowed {
			app.securityFailure(request, "origin_rejected")
			app.writeError(writer, http.StatusForbidden, "origin_not_allowed", "Источник запроса не разрешен")
			return
		}
		writer.Header().Set("Access-Control-Allow-Origin", origin)
		isImport := isBackupV5ImportPath(request.URL.Path)
		if isImport {
			writer.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, Idempotency-Replayed")
		} else {
			writer.Header().Set("Access-Control-Expose-Headers", "ETag, X-Request-ID, Idempotency-Key, If-Match, Idempotency-Replayed")
		}

		if request.Method == http.MethodOptions {
			if isImport && (request.URL.RawQuery != "" || request.URL.Fragment != "") {
				app.importBadRequest(writer)
				return
			}
			methodValues := request.Header.Values("Access-Control-Request-Method")
			if len(methodValues) != 1 || methodValues[0] == "" {
				app.securityFailure(request, "preflight_ambiguous")
				app.writeError(writer, http.StatusForbidden, "preflight_not_allowed", "CORS preflight не разрешен")
				return
			}
			requestedMethod := methodValues[0]
			headerValues := request.Header.Values("Access-Control-Request-Headers")
			if len(headerValues) > 1 {
				app.securityFailure(request, "preflight_ambiguous")
				app.writeError(writer, http.StatusForbidden, "preflight_not_allowed", "CORS preflight не разрешен")
				return
			}
			requestedHeaders := ""
			if len(headerValues) == 1 {
				requestedHeaders = headerValues[0]
			}
			if !corsMethodAllowedForPath(isImport, requestedMethod) || !corsHeadersAllowedForPath(isImport, requestedHeaders) {
				app.securityFailure(request, "preflight_rejected")
				app.writeError(writer, http.StatusForbidden, "preflight_not_allowed", "CORS preflight не разрешен")
				return
			}
			if isImport {
				writer.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
				writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, Import-Preview-Token, Import-Budget-Month")
			} else {
				writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH")
				writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, If-Match")
			}
			writer.Header().Set("Access-Control-Max-Age", "600")
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func requiresBrowserOrigin(request *http.Request) bool {
	if request == nil || !strings.HasPrefix(request.URL.Path, "/api/v1") {
		return false
	}
	switch request.Method {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}

func (app *application) apiPerimeter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodOptions || !strings.HasPrefix(request.URL.Path, "/api/v1") {
			next.ServeHTTP(writer, request)
			return
		}
		lease, retryAfter, err := app.apiLimiter.acquirePerimeter()
		if err != nil {
			app.rateLimited(writer, request, retryAfter, "auth_perimeter_limited")
			return
		}
		defer lease.release()
		next.ServeHTTP(writer, request)
	})
}

func (app *application) rateLimited(
	writer http.ResponseWriter,
	request *http.Request,
	retryAfter time.Duration,
	errorClass string,
) {
	seconds := int64(retryAfter / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	writer.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	app.securityFailure(request, errorClass)
	app.writeError(writer, http.StatusTooManyRequests, "rate_limited", "Слишком много запросов")
}

func (app *application) securityFailure(request *http.Request, errorClass string) {
	if app.logger == nil || request == nil {
		return
	}
	app.logger.WarnContext(request.Context(), "security request rejected",
		"request_id", requestIDFromContext(request.Context()),
		"error_class", errorClass,
	)
}

func corsMethodAllowed(method string) bool {
	return method == http.MethodGet || method == http.MethodPost || method == http.MethodPatch
}

func corsMethodAllowedForPath(importPath bool, method string) bool {
	if importPath {
		return method == http.MethodPost
	}
	return corsMethodAllowed(method)
}

func corsHeadersAllowed(raw string) bool {
	return corsHeadersAllowedForPath(false, raw)
}

func corsHeadersAllowedForPath(importPath bool, raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	allowed := map[string]struct{}{
		"authorization": {}, "content-type": {}, "idempotency-key": {}, "if-match": {},
	}
	if importPath {
		allowed = map[string]struct{}{
			"authorization": {}, "content-type": {}, "idempotency-key": {},
			"import-preview-token": {}, "import-budget-month": {},
		}
	}
	for _, header := range strings.Split(raw, ",") {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(header))]; !ok {
			return false
		}
	}
	return true
}

func (app *application) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := newRequestID()
		writer.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(request.Context(), requestIDKey, requestID)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (app *application) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		response := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(response, request)

		app.logger.Info("request completed",
			"request_id", requestIDFromContext(request.Context()),
			"method", request.Method,
			"path", safeLogPath(request.URL.Path),
			"status", response.status,
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
	})
}

func safeLogPath(path string) string {
	if !strings.HasPrefix(path, "/api/v1") {
		return path
	}
	segments := splitAPIPath(path)
	switch {
	case len(segments) == 2 && segments[0] == "session" && segments[1] == "bootstrap":
		return "/api/v1/session/bootstrap"
	case len(segments) == 1 && segments[0] == "me":
		return "/api/v1/me"
	case len(segments) == 1 && segments[0] == "households":
		return "/api/v1/households"
	case len(segments) == 2 && segments[0] == "households":
		return "/api/v1/households/{id}"
	case len(segments) == 3 && segments[0] == "households" && segments[2] == "members":
		return "/api/v1/households/{id}/members"
	case len(segments) == 4 && segments[0] == "households" && segments[2] == "members":
		return "/api/v1/households/{id}/members/{userId}"
	case len(segments) == 3 && segments[0] == "households" && segments[2] == "invitations":
		return "/api/v1/households/{id}/invitations"
	case len(segments) == 2 && segments[0] == "invitations" && segments[1] == "accept":
		return "/api/v1/invitations/accept"
	case len(segments) == 5 && segments[0] == "households" && segments[2] == "invitations" && segments[4] == "revoke":
		return "/api/v1/households/{id}/invitations/{id}/revoke"
	case len(segments) == 5 && segments[0] == "households" && segments[2] == "imports" && segments[3] == "backup-v5" && (segments[4] == "preview" || segments[4] == "confirm"):
		return "/api/v1/households/{id}/imports/backup-v5/" + segments[4]
	case len(segments) >= 3 && segments[0] == "households" && segments[2] == "finance":
		return safeFinanceLogPath(segments)
	default:
		return "/api/v1/unmatched"
	}
}

func (app *application) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recover() != nil {
				app.logger.Error("panic recovered", "request_id", requestIDFromContext(request.Context()))
				app.writeError(writer, http.StatusInternalServerError, "internal_error", "Внутренняя ошибка сервера")
			}
		}()

		next.ServeHTTP(writer, request)
	})
}

func newRequestID() string {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(random)
}

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey).(string)
	return requestID
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}
