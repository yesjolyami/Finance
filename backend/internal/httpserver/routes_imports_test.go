package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"finance/backend/internal/backupv5"

	"github.com/google/uuid"
)

const importTestHousehold = "22000000-0000-4000-8000-000000000001"

type previewImportStub struct {
	calls         int
	err           error
	panicValue    any
	lastSubject   string
	lastHousehold uuid.UUID
	lastMonth     string
	raw           []byte
	started       chan struct{}
	release       chan struct{}
}

func (stub *previewImportStub) Preview(
	_ context.Context,
	subject string,
	householdID uuid.UUID,
	budgetMonth string,
	raw io.Reader,
) (backupv5.PreviewResponse, error) {
	stub.calls++
	stub.lastSubject = subject
	stub.lastHousehold = householdID
	stub.lastMonth = budgetMonth
	stub.raw, _ = io.ReadAll(raw)
	if stub.started != nil {
		close(stub.started)
		<-stub.release
	}
	if stub.panicValue != nil {
		value := stub.panicValue
		stub.panicValue = nil
		panic(value)
	}
	return backupv5.PreviewResponse{
		BackupDigest: "sha256:" + strings.Repeat("a", 64), ExpiresAt: time.Unix(100, 0).UTC(),
		ConfirmationToken: strings.Repeat("A", 43), BudgetMonth: budgetMonth,
		Counts: backupv5.PreviewCounts{}, Totals: backupv5.PreviewTotals{
			IncomeCents: "0", ExpenseCents: "0", TransferCents: "0", HouseholdBalanceCents: "0",
		}, Warnings: []backupv5.PreviewWarning{},
	}, stub.err
}

type confirmImportStub struct {
	calls      int
	err        error
	replayed   bool
	panicValue any
	lastInput  backupv5.ConfirmInput
	raw        []byte
}

func (stub *confirmImportStub) Confirm(_ context.Context, input backupv5.ConfirmInput) (backupv5.ConfirmResult, error) {
	stub.calls++
	stub.lastInput = input
	stub.raw, _ = io.ReadAll(input.RawJSON)
	if stub.panicValue != nil {
		value := stub.panicValue
		stub.panicValue = nil
		panic(value)
	}
	return backupv5.ConfirmResult{Replayed: stub.replayed, Response: backupv5.ConfirmResponse{
		ImportRunID: "44000000-0000-4000-8000-000000000001", Status: "completed",
		PolicyVersion: backupv5.PolicyVersion, CompletedAt: time.Unix(200, 0).UTC(),
		Counts: backupv5.PreviewCounts{}, WarningCounts: backupv5.ConfirmWarningCounts{},
	}}, stub.err
}

func newImportHTTPServer(logOutput io.Writer, preview PreviewImportService, confirm ConfirmImportService, limiter *ImportLimiter) *http.Server {
	return New(
		testConfig(), databaseStub{}, &verifierStub{}, &householdServiceStub{}, nil, testLogger(logOutput),
		WithBackupV5Imports(preview, confirm, limiter),
	)
}

func importPath(action string) string {
	return "/api/v1/households/" + importTestHousehold + "/imports/backup-v5/" + action
}

func importRequest(action, body string) *http.Request {
	request := authenticatedRequest(http.MethodPost, importPath(action), body)
	request.Header.Set(importBudgetMonthHeader, "2026-07-01")
	if action == "confirm" {
		request.Header.Set(importPreviewTokenHeader, strings.Repeat("A", 43))
		request.Header.Set("Idempotency-Key", "import-once")
	}
	return request
}

type observedBody struct {
	data   *strings.Reader
	reads  int
	closed bool
}

func newObservedBody(value string) *observedBody {
	return &observedBody{data: strings.NewReader(value)}
}
func (body *observedBody) Read(output []byte) (int, error) {
	body.reads++
	return body.data.Read(output)
}
func (body *observedBody) Close() error { body.closed = true; return nil }

func TestBackupV5RoutesUnavailableAndProtected(t *testing.T) {
	server, verifier, _ := newTestServer(io.Discard)
	for _, action := range []string{"preview", "confirm"} {
		unauthorized := httptest.NewRecorder()
		server.Handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, importPath(action), nil))
		if unauthorized.Code != http.StatusUnauthorized {
			t.Fatalf("%s unauth status=%d", action, unauthorized.Code)
		}
		request := importRequest(action, `{}`)
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusServiceUnavailable || verifier.lastToken != "valid-token" {
			t.Fatalf("%s unavailable status=%d body=%q", action, recorder.Code, recorder.Body.String())
		}
		assertImportError(t, recorder, "import_unavailable")
	}
}

func TestBackupV5PreviewAndConfirmDelegateExactInputs(t *testing.T) {
	preview := &previewImportStub{}
	confirm := &confirmImportStub{}
	server := newImportHTTPServer(io.Discard, preview, confirm, nil)

	previewRequest := importRequest("preview", `{"version":5}`)
	previewRecorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(previewRecorder, previewRequest)
	if previewRecorder.Code != http.StatusOK || preview.calls != 1 || preview.lastSubject != testSubject ||
		preview.lastHousehold.String() != importTestHousehold || preview.lastMonth != "2026-07-01" ||
		string(preview.raw) != `{"version":5}` {
		t.Fatalf("preview delegation failed: status=%d calls=%d raw=%q", previewRecorder.Code, preview.calls, preview.raw)
	}
	var previewJSON map[string]any
	if err := json.Unmarshal(previewRecorder.Body.Bytes(), &previewJSON); err != nil {
		t.Fatal(err)
	}
	assertExactKeys(t, previewJSON, "backupDigest", "expiresAt", "confirmationToken", "budgetMonth", "counts", "totals", "warnings")

	confirmRequest := importRequest("confirm", `{"version":5}`)
	confirmRecorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(confirmRecorder, confirmRequest)
	if confirmRecorder.Code != http.StatusCreated || confirm.calls != 1 || confirm.lastInput.Subject != testSubject ||
		confirm.lastInput.HouseholdID.String() != importTestHousehold || confirm.lastInput.BudgetMonth != "2026-07-01" ||
		confirm.lastInput.PreviewToken != strings.Repeat("A", 43) || confirm.lastInput.IdempotencyKey != "import-once" ||
		confirm.lastInput.ServerRequestID == "" || confirm.lastInput.ServerRequestID != confirmRecorder.Header().Get("X-Request-ID") ||
		string(confirm.raw) != `{"version":5}` ||
		confirmRecorder.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("confirm delegation failed: status=%d input=%#v raw=%q", confirmRecorder.Code, confirm.lastInput, confirm.raw)
	}
	var confirmJSON map[string]any
	if err := json.Unmarshal(confirmRecorder.Body.Bytes(), &confirmJSON); err != nil {
		t.Fatal(err)
	}
	assertExactKeys(t, confirmJSON, "importRunId", "status", "policyVersion", "completedAt", "counts", "warningCounts")

	confirm.replayed = true
	replayRecorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(replayRecorder, importRequest("confirm", `{}`))
	if replayRecorder.Code != http.StatusOK || replayRecorder.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay response status=%d headers=%v", replayRecorder.Code, replayRecorder.Header())
	}
}

type oversizedStream struct {
	remaining int64
	reads     int
}

func (stream *oversizedStream) Read(output []byte) (int, error) {
	stream.reads++
	if stream.remaining == 0 {
		return 0, io.EOF
	}
	count := int64(len(output))
	if count > stream.remaining {
		count = stream.remaining
	}
	for index := int64(0); index < count; index++ {
		output[index] = 'x'
	}
	stream.remaining -= count
	return int(count), nil
}

type boundedPreviewImportStub struct {
	seen int64
}

func (stub *boundedPreviewImportStub) Preview(_ context.Context, _ string, _ uuid.UUID, _ string, raw io.Reader) (backupv5.PreviewResponse, error) {
	buffer := make([]byte, 32*1024)
	for {
		count, err := raw.Read(buffer)
		stub.seen += int64(count)
		if err == io.EOF {
			break
		}
		if err != nil {
			return backupv5.PreviewResponse{}, err
		}
	}
	if stub.seen > backupv5.MaxRawBytes {
		return backupv5.PreviewResponse{}, backupv5.ErrLimit
	}
	return backupv5.PreviewResponse{}, nil
}

type exactSizeStream struct{ remaining int64 }

func (stream *exactSizeStream) Read(output []byte) (int, error) {
	if stream.remaining == 0 {
		return 0, io.EOF
	}
	count := int64(len(output))
	if count > stream.remaining {
		count = stream.remaining
	}
	for index := int64(0); index < count; index++ {
		output[index] = ' '
	}
	stream.remaining -= count
	return int(count), nil
}

type drainingPreviewImportStub struct{ seen int64 }

func (stub *drainingPreviewImportStub) Preview(
	_ context.Context,
	_ string,
	_ uuid.UUID,
	budgetMonth string,
	raw io.Reader,
) (backupv5.PreviewResponse, error) {
	seen, err := io.Copy(io.Discard, raw)
	stub.seen = seen
	if err != nil {
		return backupv5.PreviewResponse{}, err
	}
	return backupv5.PreviewResponse{
		BackupDigest: "sha256:" + strings.Repeat("a", 64), ExpiresAt: time.Unix(100, 0).UTC(),
		ConfirmationToken: strings.Repeat("A", 43), BudgetMonth: budgetMonth,
		Counts: backupv5.PreviewCounts{}, Totals: backupv5.PreviewTotals{
			IncomeCents: "0", ExpenseCents: "0", TransferCents: "0", HouseholdBalanceCents: "0",
		}, Warnings: []backupv5.PreviewWarning{},
	}, nil
}

func TestBackupV5StreamingAndContentLengthLimitsAreBounded(t *testing.T) {
	streamingService := &boundedPreviewImportStub{}
	server := newImportHTTPServer(io.Discard, streamingService, &confirmImportStub{}, nil)
	stream := &oversizedStream{remaining: backupv5.MaxRawBytes + 4096}
	request := importRequest("preview", "")
	request.Body = io.NopCloser(stream)
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge || streamingService.seen != backupv5.MaxRawBytes+1 || stream.remaining != 4095 {
		t.Fatalf("stream was not bounded: status=%d seen=%d remaining=%d reads=%d", recorder.Code, streamingService.seen, stream.remaining, stream.reads)
	}

	earlyBody := newObservedBody(`{"must":"not be read"}`)
	early := importRequest("preview", "")
	early.Body = earlyBody
	early.ContentLength = backupv5.MaxRawBytes + 1
	early.Header.Set("Content-Type", "application/json")
	earlyRecorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(earlyRecorder, early)
	if earlyRecorder.Code != http.StatusRequestEntityTooLarge || earlyBody.reads != 0 || !earlyBody.closed {
		t.Fatalf("content-length rejection touched body: status=%d reads=%d closed=%v", earlyRecorder.Code, earlyBody.reads, earlyBody.closed)
	}
}

func TestBackupV5RealServerAcceptsMaximumBodyWithinFiniteTimeouts(t *testing.T) {
	service := &drainingPreviewImportStub{}
	server := newImportHTTPServer(io.Discard, service, &confirmImportStub{}, nil)
	if server.ReadTimeout < backupv5.MaximumConfirmTimeout ||
		server.WriteTimeout < backupv5.MaximumConfirmTimeout ||
		server.ReadTimeout > 120*time.Second || server.WriteTimeout > 120*time.Second {
		t.Fatalf("unsafe import timeouts: read=%s write=%s", server.ReadTimeout, server.WriteTimeout)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
		<-serveDone
	})

	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+listener.Addr().String()+importPath("preview"),
		&exactSizeStream{remaining: backupv5.MaxRawBytes},
	)
	if err != nil {
		t.Fatal(err)
	}
	request.ContentLength = backupv5.MaxRawBytes
	request.Header.Set("Authorization", "Bearer valid-token")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(importBudgetMonthHeader, "2026-07-01")
	client := &http.Client{Timeout: 120 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("maximum import body request failed: %v", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK || service.seen != backupv5.MaxRawBytes {
		t.Fatalf("maximum body status=%d seen=%d", response.StatusCode, service.seen)
	}
}

func TestBackupV5StrictMethodPathHeadersAndLength(t *testing.T) {
	server := newImportHTTPServer(io.Discard, &previewImportStub{}, &confirmImportStub{}, nil)
	tests := []struct {
		name   string
		modify func(*http.Request)
		action string
		want   int
	}{
		{"method", func(r *http.Request) { r.Method = http.MethodGet }, "preview", 405},
		{"query", func(r *http.Request) { r.URL.RawQuery = "token=secret" }, "preview", 400},
		{"fragment", func(r *http.Request) { r.URL.Fragment = "secret" }, "preview", 400},
		{"malformed household", func(r *http.Request) { r.URL.Path = strings.Replace(r.URL.Path, importTestHousehold, "not-a-uuid", 1) }, "preview", 404},
		{"noncanonical household", func(r *http.Request) {
			r.URL.Path = strings.Replace(r.URL.Path, importTestHousehold, "22000000-0000-4000-8000-00000000000A", 1)
		}, "preview", 404},
		{"missing content type", func(r *http.Request) { r.Header.Del("Content-Type") }, "preview", 400},
		{"duplicate content type", func(r *http.Request) { r.Header.Add("Content-Type", "application/json") }, "preview", 400},
		{"wrong content type", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, "preview", 400},
		{"wrong charset", func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=iso-8859-1") }, "preview", 400},
		{"unknown content type parameter", func(r *http.Request) { r.Header.Set("Content-Type", "application/json; foo=bar") }, "preview", 400},
		{"extra content type parameter", func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=utf-8; boundary=x") }, "preview", 400},
		{"duplicate charset parameter", func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=utf-8; charset=utf-8") }, "preview", 400},
		{"content encoding identity", func(r *http.Request) { r.Header.Set("Content-Encoding", "identity") }, "preview", 400},
		{"content encoding empty", func(r *http.Request) { r.Header["Content-Encoding"] = []string{""} }, "preview", 400},
		{"client request id", func(r *http.Request) { r.Header.Set("X-Request-ID", "client") }, "preview", 400},
		{"missing month", func(r *http.Request) { r.Header.Del(importBudgetMonthHeader) }, "preview", 400},
		{"duplicate month", func(r *http.Request) { r.Header.Add(importBudgetMonthHeader, "2026-08-01") }, "preview", 400},
		{"malformed month", func(r *http.Request) { r.Header.Set(importBudgetMonthHeader, "2026-07-02") }, "preview", 400},
		{"year zero month", func(r *http.Request) { r.Header.Set(importBudgetMonthHeader, "0000-01-01") }, "preview", 400},
		{"preview token forbidden", func(r *http.Request) { r.Header.Set(importPreviewTokenHeader, "secret") }, "preview", 400},
		{"preview key forbidden", func(r *http.Request) { r.Header.Set("Idempotency-Key", "key") }, "preview", 400},
		{"missing token", func(r *http.Request) { r.Header.Del(importPreviewTokenHeader) }, "confirm", 400},
		{"duplicate token", func(r *http.Request) { r.Header.Add(importPreviewTokenHeader, "other") }, "confirm", 400},
		{"padded token", func(r *http.Request) { r.Header.Set(importPreviewTokenHeader, " token ") }, "confirm", 400},
		{"missing key", func(r *http.Request) { r.Header.Del("Idempotency-Key") }, "confirm", 400},
		{"duplicate key", func(r *http.Request) { r.Header.Add("Idempotency-Key", "other") }, "confirm", 400},
		{"padded key", func(r *http.Request) { r.Header.Set("Idempotency-Key", " key ") }, "confirm", 400},
		{"oversized key", func(r *http.Request) { r.Header.Set("Idempotency-Key", strings.Repeat("k", 256)) }, "confirm", 400},
		{"cookie is not token", func(r *http.Request) {
			r.Header.Del(importPreviewTokenHeader)
			r.AddCookie(&http.Cookie{Name: "token", Value: "secret"})
		}, "confirm", 400},
		{"content length", func(r *http.Request) { r.ContentLength = backupv5.MaxRawBytes + 1 }, "preview", 413},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := importRequest(test.action, `{}`)
			test.modify(request)
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%q", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}

	accepted := importRequest("preview", `{}`)
	accepted.Header.Set("Content-Type", "application/json; charset=UTF-8")
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, accepted)
	if recorder.Code != 200 {
		t.Fatalf("valid UTF-8 content type rejected: %d", recorder.Code)
	}
}

func TestBackupV5UnknownImportPathsRemainOpaqueNotFound(t *testing.T) {
	var logs bytes.Buffer
	preview := &previewImportStub{}
	confirm := &confirmImportStub{}
	server := newImportHTTPServer(&logs, preview, confirm, nil)
	for _, path := range []string{
		"/api/v1/households/" + importTestHousehold + "/imports/backup-v5/delete",
		importPath("preview") + "/extra-secret-segment",
	} {
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, authenticatedRequest(http.MethodPost, path, `{"secret":"body"}`))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("path=%s status=%d body=%q", path, recorder.Code, recorder.Body.String())
		}
	}
	if preview.calls != 0 || confirm.calls != 0 || !strings.Contains(logs.String(), "/api/v1/unmatched") ||
		strings.Contains(logs.String(), importTestHousehold) || strings.Contains(logs.String(), "extra-secret-segment") {
		t.Fatalf("unknown path was not safely isolated: preview=%d confirm=%d logs=%s", preview.calls, confirm.calls, logs.String())
	}
}

func TestBackupV5ErrorMappingIsSafe(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		action string
		status int
		code   string
	}{
		{"json", backupv5.ErrInvalidJSON, "preview", 400, "invalid_import_request"},
		{"schema", backupv5.ErrSchema, "preview", 400, "invalid_import_request"},
		{"limit", backupv5.ErrLimit, "preview", 413, "body_too_large"},
		{"value", backupv5.ErrValue, "preview", 422, "import_validation_failed"},
		{"reference", backupv5.ErrReference, "preview", 422, "import_validation_failed"},
		{"reconciliation", backupv5.ErrReconciliation, "confirm", 422, "import_validation_failed"},
		{"forbidden", backupv5.ErrForbidden, "preview", 403, "forbidden"},
		{"not found", backupv5.ErrNotFound, "preview", 404, "not_found"},
		{"nonempty", backupv5.ErrHouseholdNotEmpty, "preview", 409, "household_not_empty"},
		{"idempotency header", backupv5.ErrIdempotencyKey, "confirm", 400, "invalid_import_request"},
		{"idempotency conflict", backupv5.ErrIdempotencyConflict, "confirm", 409, "idempotency_conflict"},
		{"state conflict", backupv5.ErrImportStateConflict, "confirm", 409, "import_state_conflict"},
		{"token", backupv5.ErrPreviewTokenInvalid, "confirm", 410, "preview_token_invalid"},
		{"cancel", context.Canceled, "confirm", 500, "internal_error"},
		{"internal", errors.New("sql secret body digest"), "confirm", 500, "internal_error"},
		{"token collision", backupv5.ErrTokenCollision, "preview", 500, "internal_error"},
		{"entropy", backupv5.ErrEntropy, "preview", 500, "internal_error"},
		{"repository", backupv5.ErrRepository, "preview", 500, "internal_error"},
		{"request id", backupv5.ErrRequestID, "confirm", 500, "internal_error"},
		{"referenced key", backupv5.ErrReferencedKeyMissing, "confirm", 500, "internal_error"},
		{"wrapped schema", &backupv5.ValidationError{Kind: backupv5.ErrSchema, Code: "private-code", Path: "accounts.private-name"}, "preview", 400, "invalid_import_request"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preview := &previewImportStub{}
			confirm := &confirmImportStub{}
			if test.action == "preview" {
				preview.err = test.err
			} else {
				confirm.err = test.err
			}
			server := newImportHTTPServer(io.Discard, preview, confirm, nil)
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, importRequest(test.action, `{"name":"private"}`))
			if recorder.Code != test.status || strings.Contains(recorder.Body.String(), "private") || strings.Contains(recorder.Body.String(), "secret") {
				t.Fatalf("status=%d want=%d body=%q", recorder.Code, test.status, recorder.Body.String())
			}
			assertImportError(t, recorder, test.code)
		})
	}
}

func TestBackupV5ActiveOperationBlocksBeforeBodyRead(t *testing.T) {
	clock := &importLimiterClockStub{now: time.Unix(450, 0)}
	limiter, _ := NewImportLimiter(ImportLimiterConfig{Window: 15 * time.Minute, PreviewAttempts: 5, ConfirmAttempts: 3, MaximumTrackedKeys: 8}, clock)
	preview := &previewImportStub{started: make(chan struct{}), release: make(chan struct{})}
	server := newImportHTTPServer(io.Discard, preview, &confirmImportStub{}, limiter)

	finished := make(chan int, 1)
	go func() {
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, importRequest("preview", `{}`))
		finished <- recorder.Code
	}()
	<-preview.started

	body := newObservedBody(`{"name":"must-not-be-read"}`)
	request := importRequest("confirm", "")
	request.Body = body
	request.ContentLength = int64(body.data.Len())
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, request)
	if recorder.Code != 429 || body.reads != 0 || !body.closed {
		t.Fatalf("active operation did not block safely: status=%d reads=%d closed=%v", recorder.Code, body.reads, body.closed)
	}
	close(preview.release)
	if status := <-finished; status != 200 {
		t.Fatalf("first request status=%d", status)
	}
}

func TestBackupV5LimiterReleasesAfterCancellationAndError(t *testing.T) {
	clock := &importLimiterClockStub{now: time.Unix(475, 0)}
	limiter, _ := NewImportLimiter(ImportLimiterConfig{Window: 15 * time.Minute, PreviewAttempts: 3, ConfirmAttempts: 1, MaximumTrackedKeys: 8}, clock)
	preview := &previewImportStub{err: context.Canceled}
	server := newImportHTTPServer(io.Discard, preview, &confirmImportStub{}, limiter)

	first := httptest.NewRecorder()
	server.Handler.ServeHTTP(first, importRequest("preview", `{}`))
	if first.Code != 500 {
		t.Fatalf("cancel status=%d", first.Code)
	}
	preview.err = errors.New("repository unavailable")
	second := httptest.NewRecorder()
	server.Handler.ServeHTTP(second, importRequest("preview", `{}`))
	if second.Code != 500 {
		t.Fatalf("error status=%d", second.Code)
	}
	preview.err = nil
	third := httptest.NewRecorder()
	server.Handler.ServeHTTP(third, importRequest("preview", `{}`))
	if third.Code != 200 {
		t.Fatalf("slot not released after cancellation/error: %d", third.Code)
	}
}

type cancellationConfirmStub struct {
	started chan struct{}
	block   bool
}

func (stub *cancellationConfirmStub) Confirm(ctx context.Context, _ backupv5.ConfirmInput) (backupv5.ConfirmResult, error) {
	if stub.block {
		close(stub.started)
		<-ctx.Done()
		return backupv5.ConfirmResult{}, ctx.Err()
	}
	return backupv5.ConfirmResult{Response: backupv5.ConfirmResponse{
		ImportRunID: "44000000-0000-4000-8000-000000000001", Status: "completed",
		PolicyVersion: backupv5.PolicyVersion, CompletedAt: time.Unix(700, 0).UTC(),
		Counts: backupv5.PreviewCounts{}, WarningCounts: backupv5.ConfirmWarningCounts{},
	}}, nil
}

func TestBackupV5HTTPCancellationReleasesActorAndHouseholdSlots(t *testing.T) {
	clock := &importLimiterClockStub{now: time.Unix(700, 0)}
	limiter, _ := NewImportLimiter(ImportLimiterConfig{Window: 15 * time.Minute, PreviewAttempts: 1, ConfirmAttempts: 2, MaximumTrackedKeys: 8}, clock)
	confirm := &cancellationConfirmStub{started: make(chan struct{}), block: true}
	server := newImportHTTPServer(io.Discard, &previewImportStub{}, confirm, limiter)

	ctx, cancel := context.WithCancel(context.Background())
	request := importRequest("confirm", `{}`).WithContext(ctx)
	finished := make(chan int, 1)
	go func() {
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, request)
		finished <- recorder.Code
	}()
	<-confirm.started
	cancel()
	if status := <-finished; status != http.StatusInternalServerError {
		t.Fatalf("cancelled request status=%d", status)
	}

	confirm.block = false
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, importRequest("confirm", `{}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("slots remained held after cancellation: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestBackupV5LimiterRejectsBeforeBodyAndReleasesAfterPanic(t *testing.T) {
	clock := &importLimiterClockStub{now: time.Unix(500, 0)}
	limiter, _ := NewImportLimiter(ImportLimiterConfig{Window: 15 * time.Minute, PreviewAttempts: 1, ConfirmAttempts: 1, MaximumTrackedKeys: 8}, clock)
	preview := &previewImportStub{}
	server := newImportHTTPServer(io.Discard, preview, &confirmImportStub{}, limiter)

	first := httptest.NewRecorder()
	server.Handler.ServeHTTP(first, importRequest("preview", `{}`))
	if first.Code != 200 {
		t.Fatal(first.Code)
	}
	body := newObservedBody(`{"legacy":"secret"}`)
	limited := importRequest("preview", "")
	limited.Body = body
	limited.ContentLength = int64(body.data.Len())
	limited.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, limited)
	if recorder.Code != 429 || recorder.Header().Get("Retry-After") != "900" ||
		body.reads != 0 || !body.closed || preview.calls != 1 {
		t.Fatalf("limited request touched body/service: status=%d reads=%d closed=%v calls=%d", recorder.Code, body.reads, body.closed, preview.calls)
	}

	clock.now = clock.now.Add(15 * time.Minute)
	preview.panicValue = "body-token-panic-secret"
	panicRecorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(panicRecorder, importRequest("preview", `{}`))
	if panicRecorder.Code != 500 {
		t.Fatalf("panic status=%d", panicRecorder.Code)
	}
	clock.now = clock.now.Add(15 * time.Minute)
	retry := httptest.NewRecorder()
	server.Handler.ServeHTTP(retry, importRequest("preview", `{}`))
	if retry.Code != 200 {
		t.Fatalf("slot not released after panic: %d", retry.Code)
	}
}

func TestBackupV5ConfirmReplayRefundsQuota(t *testing.T) {
	clock := &importLimiterClockStub{now: time.Unix(600, 0)}
	limiter, _ := NewImportLimiter(ImportLimiterConfig{Window: 15 * time.Minute, PreviewAttempts: 1, ConfirmAttempts: 3, MaximumTrackedKeys: 8}, clock)
	confirm := &confirmImportStub{replayed: true}
	server := newImportHTTPServer(io.Discard, &previewImportStub{}, confirm, limiter)
	for attempt := 0; attempt < 5; attempt++ {
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, importRequest("confirm", `{}`))
		if recorder.Code != 200 {
			t.Fatalf("replay %d status=%d", attempt+1, recorder.Code)
		}
	}
	confirm.replayed = false
	for attempt := 0; attempt < 3; attempt++ {
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, importRequest("confirm", `{}`))
		if recorder.Code != 201 {
			t.Fatalf("first confirm %d status=%d", attempt+1, recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, importRequest("confirm", `{}`))
	if recorder.Code != 429 {
		t.Fatalf("fourth non-replay status=%d", recorder.Code)
	}
}

func TestBackupV5ImportCORSIsRouteSpecificAndExact(t *testing.T) {
	server := newImportHTTPServer(io.Discard, &previewImportStub{}, &confirmImportStub{}, nil)
	preflight := httptest.NewRequest(http.MethodOptions, importPath("confirm"), nil)
	preflight.Header.Set("Origin", "https://app.test")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflight.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type, Idempotency-Key, Import-Preview-Token, Import-Budget-Month")
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, preflight)
	if recorder.Code != 204 || recorder.Header().Get("Access-Control-Allow-Methods") != "POST" ||
		recorder.Header().Get("Access-Control-Allow-Headers") != "Authorization, Content-Type, Idempotency-Key, Import-Preview-Token, Import-Budget-Month" ||
		recorder.Header().Get("Access-Control-Expose-Headers") != "X-Request-ID, Idempotency-Replayed" ||
		recorder.Header().Get("Access-Control-Allow-Credentials") != "" || recorder.Header().Get("Access-Control-Allow-Origin") != "https://app.test" {
		t.Fatalf("import preflight status=%d headers=%v", recorder.Code, recorder.Header())
	}

	tests := []struct {
		name   string
		modify func(*http.Request)
		status int
	}{
		{"get", func(r *http.Request) { r.Header.Set("Access-Control-Request-Method", "GET") }, 403},
		{"x request id", func(r *http.Request) { r.Header.Set("Access-Control-Request-Headers", "X-Request-ID") }, 403},
		{"query", func(r *http.Request) { r.URL.RawQuery = "token=secret" }, 400},
		{"duplicate origin", func(r *http.Request) { r.Header.Add("Origin", "https://app.test") }, 403},
		{"duplicate method", func(r *http.Request) { r.Header.Add("Access-Control-Request-Method", "POST") }, 403},
		{"duplicate requested headers", func(r *http.Request) { r.Header.Add("Access-Control-Request-Headers", "Authorization") }, 403},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := preflight.Clone(context.Background())
			request.Header = preflight.Header.Clone()
			test.modify(request)
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status=%d want=%d body=%q", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}

	ordinary := httptest.NewRequest(http.MethodOptions, "/api/v1/households", nil)
	ordinary.Header.Set("Origin", "https://app.test")
	ordinary.Header.Set("Access-Control-Request-Method", "PATCH")
	ordinary.Header.Set("Access-Control-Request-Headers", "Authorization, If-Match")
	recorder = httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, ordinary)
	if recorder.Code != 204 || !strings.Contains(recorder.Header().Get("Access-Control-Expose-Headers"), "ETag") {
		t.Fatalf("ordinary CORS regressed: status=%d headers=%v", recorder.Code, recorder.Header())
	}
}

func TestBackupV5LogsRedactAllImportSecretsAndPaths(t *testing.T) {
	var logs bytes.Buffer
	preview := &previewImportStub{err: errors.New("database digest-secret")}
	confirm := &confirmImportStub{err: errors.New("token-secret")}
	server := newImportHTTPServer(&logs, preview, confirm, nil)

	authorization := "jwt-secret"
	body := `{"name":"legacy-name-secret","note":"private-note-secret","digest":"digest-secret"}`
	request := importRequest("preview", body)
	request.Header.Set("Authorization", "Bearer "+authorization)
	request.Header.Set(importBudgetMonthHeader, "2026-11-01")
	server.Handler.ServeHTTP(httptest.NewRecorder(), request)

	request = importRequest("confirm", body)
	request.Header.Set("Authorization", "Bearer "+authorization)
	request.Header.Set(importPreviewTokenHeader, "preview-token-secret")
	request.Header.Set("Idempotency-Key", "idempotency-secret")
	server.Handler.ServeHTTP(httptest.NewRecorder(), request)

	confirm.err = nil
	confirm.panicValue = "panic-secret preview-token-secret legacy-name-secret 2026-07-01 " + importTestHousehold
	server.Handler.ServeHTTP(httptest.NewRecorder(), importRequest("confirm", body))

	output := logs.String()
	for _, secret := range []string{authorization, "legacy-name-secret", "private-note-secret", "digest-secret", "token-secret", "preview-token-secret", "idempotency-secret", "panic-secret", "2026-11-01", importTestHousehold} {
		if strings.Contains(output, secret) {
			t.Fatalf("secret leaked to logs: %q in %s", secret, output)
		}
	}
	for _, path := range []string{
		"/api/v1/households/{id}/imports/backup-v5/preview",
		"/api/v1/households/{id}/imports/backup-v5/confirm",
	} {
		if !strings.Contains(output, path) {
			t.Fatalf("sanitized path missing: %s", output)
		}
	}
}

func assertImportError(t *testing.T, recorder *httptest.ResponseRecorder, code string) {
	t.Helper()
	body := decodeBody(t, recorder)
	assertExactKeys(t, body, "error")
	errorBody, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error envelope: %v", body)
	}
	assertExactKeys(t, errorBody, "code", "message")
	if errorBody["code"] != code {
		t.Fatalf("error code=%v want=%s", errorBody["code"], code)
	}
}

func assertExactKeys(t *testing.T, value map[string]any, keys ...string) {
	t.Helper()
	if len(value) != len(keys) {
		t.Fatalf("keys=%v want=%v", value, keys)
	}
	for _, key := range keys {
		if _, ok := value[key]; !ok {
			t.Fatalf("key %q absent in %v", key, value)
		}
	}
}
