package store

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"fmt"

	"github.com/boltdb/bolt"
)

func New(path string) (*Store, error) {
	// Open the my.db data file in your current directory.
	// It will be created if it doesn't exist.
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("store: couldn't open bold db %s: %w", path, err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte("db")); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("store: couldn't update db: %w", err)
	}
	return &Store{db: db}, nil
}

type Store struct {
	db *bolt.DB
}

func (s *Store) Close() {
	s.db.Close()
}

func (s *Store) Keys() ([]string, error) {
	var keys []string
	if err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("db"))
		return b.ForEach(func(k, v []byte) error {
			keys = append(keys, string(k))
			return nil
		})
	}); err != nil {
		return nil, fmt.Errorf("store: couldn't get keys: %w", err)
	}
	return keys, nil
}

func (s *Store) Get(key string, val interface{}) error {
	if err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("db"))
		if v := b.Get([]byte(key)); len(v) > 0 {
			dec := gob.NewDecoder(bytes.NewReader(v))
			if err := dec.Decode(val); err != nil {
				return fmt.Errorf("couldn't decode gob: %w", err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("store: couldn't get %s: %w", key, err)
	}
	return nil
}

func (s *Store) Put(key string, val interface{}) error {
	if err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("db"))
		var buf bytes.Buffer
		enc := gob.NewEncoder(bufio.NewWriter(&buf))
		if err := enc.Encode(val); err != nil {
			return fmt.Errorf("couldn't encode gob: %w", err)
		}
		return b.Put([]byte(key), buf.Bytes())
	}); err != nil {
		return fmt.Errorf("store: couldn't put %s: %w", key, err)
	}
	return nil
}

func (s *Store) Delete(key string) error {
	if err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("db"))
		return b.Delete([]byte(key))
	}); err != nil {
		return fmt.Errorf("store: couldn't delete %s: %w", key, err)
	}
	return nil
}
