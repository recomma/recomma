package storage

import (
	"fmt"
	"sync"
	"testing"

	"github.com/dgraph-io/badger/v4"
)

// newTestStorage creates a fresh Storage instance backed by an in‑memory Badger
// database stored in a temporary directory. The directory is removed once the
// test finishes.
func newTestStorage(t *testing.T) (*Storage, func()) {
	t.Helper()

	st, err := newStorage(badger.DefaultOptions("").WithInMemory(true).WithLogger(nil))
	if err != nil {
		t.Fatalf("unable to create storage: %v", err)
	}

	cleanup := func() {
		st.Close()
	}

	return st, cleanup
}

func TestSeenKey(t *testing.T) {
	tests := []struct {
		name          string
		keysToAdd     []string // keys that will be added
		keyToCheck    string   // key whose presence we will query
		expectedExist bool     // whether the key should be reported as seen
	}{
		{
			name:          "key exists",
			keysToAdd:     []string{"alpha", "beta"},
			keyToCheck:    "alpha",
			expectedExist: true,
		},
		{
			name:          "key does not exist",
			keysToAdd:     []string{"gamma"},
			keyToCheck:    "delta",
			expectedExist: false,
		},
		{
			name:          "duplicate add",
			keysToAdd:     []string{"dup", "dup"},
			keyToCheck:    "dup",
			expectedExist: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, cleanup := newTestStorage(t)
			defer cleanup()

			// add the keys
			for _, k := range tt.keysToAdd {
				if err := st.Add(k, []byte("value")); err != nil {
					t.Fatalf("Add failed for %s: %v", k, err)
				}
			}

			got := st.SeenKey([]byte(tt.keyToCheck))
			if got != tt.expectedExist {
				t.Fatalf("SeenKey(%q) = %v; want %v", tt.keyToCheck, got, tt.expectedExist)
			}
		})
	}
}

func TestSize(t *testing.T) {
	tests := []struct {
		name        string
		keysToAdd   []string
		expectedLen int
	}{
		{
			name:        "empty storage",
			keysToAdd:   nil,
			expectedLen: 0,
		},
		{
			name:        "single key",
			keysToAdd:   []string{"one"},
			expectedLen: 1,
		},
		{
			name:        "multiple keys",
			keysToAdd:   []string{"a", "b", "c", "d"},
			expectedLen: 4,
		},
		{
			name:        "duplicate keys",
			keysToAdd:   []string{"dup", "dup"},
			expectedLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, cleanup := newTestStorage(t)
			defer cleanup()

			for _, k := range tt.keysToAdd {
				if err := st.Add(k, []byte("v")); err != nil {
					t.Fatalf("Add failed for %s: %v", k, err)
				}
			}

			if got := st.Size(); got != tt.expectedLen {
				t.Fatalf("Size() = %d; want %d", got, tt.expectedLen)
			}
		})
	}
}

func TestConcurrentAddSize(t *testing.T) {
	st, cleanup := newTestStorage(t)
	defer cleanup()

	const (
		goroutines   = 10
		perGoroutine = 100
	)

	var wg sync.WaitGroup
	wg.Add(goroutines)

	// each goroutine adds `perGoroutine` unique keys
	for i := 0; i < goroutines; i++ {
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				key := fmt.Sprintf("g%d_k%d", gid, j)
				if err := st.Add(key, []byte("v")); err != nil {
					t.Errorf("Add failed for %s: %v", key, err)
				}
			}
		}(i)
	}

	wg.Wait()

	expected := goroutines * perGoroutine
	if got := st.Size(); got != expected {
		t.Fatalf("Size() = %d; want %d", got, expected)
	}
}
