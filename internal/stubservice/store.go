package stubservice

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type StoredResponse struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

type Store struct {
	mu        sync.Mutex
	path      string
	responses map[string]StoredResponse
}

func NewStore(path string) (*Store, error) {
	store := &Store{
		path:      path,
		responses: make(map[string]StoredResponse),
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &store.responses); err != nil {
			return nil, fmt.Errorf("decode persisted stub state: %w", err)
		}
		return store, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read persisted stub state: %w", err)
	}
	return store, nil
}

func (s *Store) GetOrSave(key string, create func() StoredResponse) (StoredResponse, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if response, ok := s.responses[key]; ok {
		return response, true, nil
	}

	response := create()
	s.responses[key] = response
	if err := s.persist(); err != nil {
		delete(s.responses, key)
		return StoredResponse{}, false, err
	}
	return response, false, nil
}

func (s *Store) persist() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	data, err := json.MarshalIndent(s.responses, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	temporaryPath := s.path + ".tmp"
	if err := os.WriteFile(temporaryPath, data, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}
