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
		return nil, fmt.Errorf("store: couldn't view db: %w", err)
	}
	return keys, nil
}

func (s *Store) Get(key string, val interface{}) error {
	if err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("db"))
		if v := b.Get([]byte(key)); v != nil {
			dec := gob.NewDecoder(bytes.NewReader(v))
			if err := dec.Decode(val); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("store: couldn't view db: %w", err)
	}
	return nil
}

func (s *Store) Put(key string, val interface{}) error {
	if err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("db"))
		var buf bytes.Buffer
		enc := gob.NewEncoder(bufio.NewWriter(&buf))
		if err := enc.Encode(val); err != nil {
			return err
		}
		return b.Put([]byte(key), buf.Bytes())
	}); err != nil {
		return fmt.Errorf("store: couldn't update db: %w", err)
	}
	return nil
}