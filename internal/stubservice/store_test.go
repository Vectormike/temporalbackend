package stubservice

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoreLoadsAnEmptyFileAsNoResponses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0o600))

	store, err := NewStore(path)
	require.NoError(t, err)

	response, replayed, err := store.GetOrSave("missing", func() StoredResponse {
		return StoredResponse{Status: http.StatusOK}
	})
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, http.StatusOK, response.Status)
}

func TestStoreRejectsACorruptStateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte(`{`), 0o600))

	_, err := NewStore(path)
	require.Error(t, err)
}

func TestStoreDropsAResponseWhenPersistenceFails(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.Chmod(directory, 0o500))
	t.Cleanup(func() {
		_ = os.Chmod(directory, 0o700)
	})

	store, err := NewStore(filepath.Join(directory, "state.json"))
	require.NoError(t, err)

	_, replayed, err := store.GetOrSave("payment-1:authorize", func() StoredResponse {
		return StoredResponse{Status: http.StatusOK}
	})
	require.Error(t, err)
	require.False(t, replayed)

	_, replayed, err = store.GetOrSave("payment-1:authorize", func() StoredResponse {
		return StoredResponse{Status: http.StatusCreated}
	})
	require.Error(t, err)
	require.False(t, replayed)
}

func TestStoreServesOneResponseToConcurrentCallers(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	require.NoError(t, err)

	var created int
	var wait sync.WaitGroup
	type outcome struct {
		replayed bool
		err      error
	}
	results := make(chan outcome, 8)
	wait.Add(8)
	for range 8 {
		go func() {
			defer wait.Done()
			_, replayed, err := store.GetOrSave("payment-1:capture", func() StoredResponse {
				created++
				return StoredResponse{Status: http.StatusOK}
			})
			results <- outcome{replayed: replayed, err: err}
		}()
	}
	wait.Wait()
	close(results)

	var replays int
	for result := range results {
		require.NoError(t, result.err)
		if result.replayed {
			replays++
		}
	}
	require.Equal(t, 1, created)
	require.Equal(t, 7, replays)
}
