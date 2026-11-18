package stubservice

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHealth(t *testing.T) {
	response := httptest.NewRecorder()
	newTestServer(t, "success", time.Second).Handler().ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/health", nil),
	)

	require.Equal(t, http.StatusNoContent, response.Code)
	require.Empty(t, response.Body.String())
}

func TestIdempotentEndpointsReplayTheStoredResponse(t *testing.T) {
	server := newTestServer(t, "success", time.Second)
	handler := server.Handler()

	first := post(t, handler, "/authorize", "payment-1:authorize", `{"amount":2500}`)
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, "false", first.Header().Get("Idempotency-Replayed"))
	require.JSONEq(t, `{"id":"authorization_`+shortHash("payment-1:authorize")+`"}`, first.Body.String())

	second := post(t, handler, "/authorize", "payment-1:authorize", `{"amount":2500}`)
	require.Equal(t, http.StatusOK, second.Code)
	require.Equal(t, "true", second.Header().Get("Idempotency-Replayed"))
	require.Equal(t, first.Body.String(), second.Body.String())

	other := post(t, handler, "/authorize", "payment-2:authorize", `{"amount":2500}`)
	require.Equal(t, "false", other.Header().Get("Idempotency-Replayed"))
	require.NotEqual(t, first.Body.String(), other.Body.String())

	pending := post(t, handler, "/ledger/pending", "payment-1:ledger-pending", `{}`)
	require.Equal(t, http.StatusOK, pending.Code)
	require.JSONEq(t, `{"id":"ledger_`+shortHash("payment-1:ledger-pending")+`"}`, pending.Body.String())

	for _, path := range []string{"/ledger/completed", "/release"} {
		response := post(t, handler, path, "payment-1"+path, `{}`)
		require.Equal(t, http.StatusNoContent, response.Code, path)
		require.Empty(t, response.Body.String(), path)
		require.Equal(t, "false", response.Header().Get("Idempotency-Replayed"), path)
	}
}

func TestRequestsRequireAnIdempotencyKeyAndJSON(t *testing.T) {
	handler := newTestServer(t, "success", time.Second).Handler()

	missing := post(t, handler, "/authorize", "", `{}`)
	require.Equal(t, http.StatusBadRequest, missing.Code)
	require.Contains(t, missing.Body.String(), "Idempotency-Key")

	blank := post(t, handler, "/authorize", "   ", `{}`)
	require.Equal(t, http.StatusBadRequest, blank.Code)

	invalid := post(t, handler, "/authorize", "payment-1:authorize", `not-json`)
	require.Equal(t, http.StatusBadRequest, invalid.Code)
	require.Contains(t, invalid.Body.String(), "valid JSON")
}

func TestCaptureSuccessIsIdempotent(t *testing.T) {
	handler := newTestServer(t, "success", time.Second).Handler()

	first := post(t, handler, "/capture", "payment-1:capture", `{}`)
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, "false", first.Header().Get("Idempotency-Replayed"))
	require.JSONEq(t, `{"id":"capture_`+shortHash("payment-1:capture")+`"}`, first.Body.String())

	second := post(t, handler, "/capture", "payment-1:capture", `{}`)
	require.Equal(t, "true", second.Header().Get("Idempotency-Replayed"))
	require.Equal(t, first.Body.String(), second.Body.String())
}

func TestCaptureFailureIsNotStored(t *testing.T) {
	handler := newTestServer(t, "fail", time.Second).Handler()

	first := post(t, handler, "/capture", "payment-1:capture", `{}`)
	require.Equal(t, http.StatusServiceUnavailable, first.Code)

	second := post(t, handler, "/capture", "payment-1:capture", `{}`)
	require.Equal(t, http.StatusServiceUnavailable, second.Code)
	require.Empty(t, second.Header().Get("Idempotency-Replayed"))
}

func TestCaptureTimeoutWaitsThenSucceeds(t *testing.T) {
	handler := newTestServer(t, "timeout", 20*time.Millisecond).Handler()

	started := time.Now()
	response := post(t, handler, "/capture", "payment-1:capture", `{}`)

	require.GreaterOrEqual(t, time.Since(started), 20*time.Millisecond)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "false", response.Header().Get("Idempotency-Replayed"))
}

func TestCaptureTimeoutStopsWhenTheRequestIsCanceled(t *testing.T) {
	server := newTestServer(t, "timeout", time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/capture", strings.NewReader(`{}`)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "payment-1:capture")
	response := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		server.Handler().ServeHTTP(response, request)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("capture did not return after the request was canceled")
	}
	require.Empty(t, response.Body.String())
}

func TestCaptureRejectsAnUnknownMode(t *testing.T) {
	response := post(t, newTestServer(t, "sometimes", time.Second).Handler(), "/capture", "payment-1:capture", `{}`)

	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Contains(t, response.Body.String(), "CAPTURE_MODE")
}

func TestReplaySurvivesARestart(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.json")
	logger := log.New(io.Discard, "", 0)

	store, err := NewStore(path)
	require.NoError(t, err)
	first := post(t, NewServer(store, "success", time.Second, logger).Handler(), "/authorize", "payment-1:authorize", `{}`)

	reloaded, err := NewStore(path)
	require.NoError(t, err)
	second := post(t, NewServer(reloaded, "success", time.Second, logger).Handler(), "/authorize", "payment-1:authorize", `{}`)

	require.Equal(t, "true", second.Header().Get("Idempotency-Replayed"))
	require.JSONEq(t, first.Body.String(), second.Body.String())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var persisted map[string]StoredResponse
	require.NoError(t, json.Unmarshal(data, &persisted))
	require.Contains(t, persisted, "payment-1:authorize")
}

func newTestServer(t *testing.T, mode string, delay time.Duration) *Server {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	require.NoError(t, err)
	return NewServer(store, mode, delay, log.New(io.Discard, "", 0))
}

func post(t *testing.T, handler http.Handler, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
