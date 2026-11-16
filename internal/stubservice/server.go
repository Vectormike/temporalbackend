package stubservice

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type Server struct {
	store        *Store
	captureMode  string
	captureDelay time.Duration
	logger       *log.Logger
}

func NewServer(store *Store, captureMode string, captureDelay time.Duration, logger *log.Logger) *Server {
	return &Server{
		store:        store,
		captureMode:  captureMode,
		captureDelay: captureDelay,
		logger:       logger,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /authorize", s.idempotent("authorization", http.StatusOK))
	mux.HandleFunc("POST /ledger/pending", s.idempotent("ledger", http.StatusOK))
	mux.HandleFunc("POST /ledger/completed", s.idempotent("", http.StatusNoContent))
	mux.HandleFunc("POST /release", s.idempotent("", http.StatusNoContent))
	mux.HandleFunc("POST /capture", s.capture)
	return mux
}

func (s *Server) idempotent(resource string, status int) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		key, ok := validateRequest(writer, request)
		if !ok {
			return
		}

		response, replayed, err := s.store.GetOrSave(key, func() StoredResponse {
			var body []byte
			if resource != "" {
				body, _ = json.Marshal(map[string]string{"id": resource + "_" + shortHash(key)})
			}
			return StoredResponse{Status: status, Body: body}
		})
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		s.writeResponse(writer, key, response, replayed)
	}
}

func (s *Server) capture(writer http.ResponseWriter, request *http.Request) {
	key, ok := validateRequest(writer, request)
	if !ok {
		return
	}

	switch s.captureMode {
	case "fail":
		s.logger.Printf("capture failed key=%s", key)
		http.Error(writer, "simulated banking provider failure", http.StatusServiceUnavailable)
		return
	case "timeout":
		timer := time.NewTimer(s.captureDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-request.Context().Done():
			s.logger.Printf("capture canceled while waiting key=%s", key)
			return
		}
	case "success":
	default:
		http.Error(writer, "invalid CAPTURE_MODE", http.StatusInternalServerError)
		return
	}

	response, replayed, err := s.store.GetOrSave(key, func() StoredResponse {
		body, _ := json.Marshal(map[string]string{"id": "capture_" + shortHash(key)})
		return StoredResponse{Status: http.StatusOK, Body: body}
	})
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeResponse(writer, key, response, replayed)
}

func (s *Server) writeResponse(
	writer http.ResponseWriter,
	key string,
	response StoredResponse,
	replayed bool,
) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Idempotency-Replayed", fmt.Sprintf("%t", replayed))
	writer.WriteHeader(response.Status)
	if len(response.Body) > 0 {
		_, _ = writer.Write(response.Body)
	}
	s.logger.Printf("external operation key=%s replayed=%t status=%d", key, replayed, response.Status)
}

func validateRequest(writer http.ResponseWriter, request *http.Request) (string, bool) {
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if key == "" {
		http.Error(writer, "Idempotency-Key header is required", http.StatusBadRequest)
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		http.Error(writer, "could not read request", http.StatusBadRequest)
		return "", false
	}
	if !json.Valid(body) {
		http.Error(writer, "request body must be valid JSON", http.StatusBadRequest)
		return "", false
	}
	return key, true
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
