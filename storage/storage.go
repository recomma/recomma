package storage

import (
	"log"
	"sync"

	"github.com/dgraph-io/badger/v4"
)

type Storage struct {
	db   *badger.DB
	seen map[string]struct{}
	mu   sync.RWMutex
}

func New(path string) (*Storage, error) {
	return newStorage(badger.DefaultOptions(path))
}

func newStorage(opt badger.Options) (*Storage, error) {
	db, err := badger.Open(opt)
	if err != nil {
		return nil, err
	}

	return &Storage{
		db:   db,
		seen: make(map[string]struct{}),
	}, nil
}

func (st *Storage) Close() error {
	st.mu.Lock()
	st.seen = make(map[string]struct{})
	st.mu.Unlock()
	return st.db.Close()
}

func (st *Storage) Size() int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return len(st.seen)
}

func (st *Storage) Add(key string, data []byte) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.seen[key] = struct{}{}
	return st.db.Update(func(txn *badger.Txn) error {
		e := badger.NewEntry([]byte(key), data)
		return txn.SetEntry(e)
	})
}

func (st *Storage) LoadSeenKeys(prefix []byte) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	err := st.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = prefix

		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			key := it.Item().KeyCopy(nil)
			st.seen[string(key)] = struct{}{}
		}
		return nil
	})
	return err
}

func (st *Storage) SeenKey(key []byte) bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	// first check our cache
	if _, ok := st.seen[string(key)]; ok {
		return true
	}

	err := st.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get(key)
		return err
	})

	switch err {
	case badger.ErrKeyNotFound:
		return false

	case nil:
		return true

	default:
		log.Printf("Unexpected error for key %s: %s", key, err)
	}

	return false
}
