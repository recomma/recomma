package storage

import (
	"log"

	"github.com/dgraph-io/badger/v4"
)

type Storage struct {
	db   *badger.DB
	seen map[string]struct{}
}

func NewStorage(path string) (*Storage, error) {
	db, err := badger.Open(badger.DefaultOptions("badger"))
	if err != nil {
		return nil, err
	}

	return &Storage{
		db:   db,
		seen: make(map[string]struct{}),
	}, nil
}

func (st *Storage) Close() error {
	return st.db.Close()
}

func (st *Storage) Size() int {
	return len(st.seen)
}

func (st *Storage) Add(key string, data []byte) error {
	return st.db.Update(func(txn *badger.Txn) error {
		e := badger.NewEntry([]byte(key), data)
		return txn.SetEntry(e)
	})
}

func (st *Storage) LoadSeenKeys(prefix []byte) error {
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
