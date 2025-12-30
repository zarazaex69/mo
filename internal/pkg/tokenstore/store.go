package tokenstore

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/google/uuid"
)

type Token struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
	IsActive  bool      `json:"is_active"`
}

type Store struct {
	db *badger.DB
}

func New(path string) (*Store, error) {
	opts := badger.DefaultOptions(path).
		WithLoggingLevel(badger.ERROR)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open badger: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Add(email, token string) (*Token, error) {
	t := &Token{
		ID:        uuid.New().String()[:8],
		Email:     email,
		Token:     token,
		CreatedAt: time.Now(),
		IsActive:  false,
	}

	// if this is first token, make it active
	tokens, _ := s.List()
	if len(tokens) == 0 {
		t.IsActive = true
	}

	if err := s.save(t); err != nil {
		return nil, err
	}

	return t, nil
}

func (s *Store) Remove(id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte("token:" + id))
	})
}

func (s *Store) SetActive(id string) error {
	tokens, err := s.List()
	if err != nil {
		return err
	}

	// deactivate all, activate target
	for _, t := range tokens {
		t.IsActive = (t.ID == id)
		if err := s.save(t); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) GetActive() (*Token, error) {
	tokens, err := s.List()
	if err != nil {
		return nil, err
	}

	for _, t := range tokens {
		if t.IsActive {
			return t, nil
		}
	}

	return nil, nil
}

func (s *Store) List() ([]*Token, error) {
	var tokens []*Token

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte("token:")
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var t Token
				if err := json.Unmarshal(val, &t); err != nil {
					return err
				}
				tokens = append(tokens, &t)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	return tokens, err
}

func (s *Store) save(t *Token) error {
	data, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}

	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte("token:"+t.ID), data)
	})
}

// ValidateToken checks if token is valid by calling Z.ai API
func ValidateToken(token string) bool {
	req, err := http.NewRequest("GET", "https://chat.z.ai/api/v1/folders/", nil)
	if err != nil {
		return false
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:146.0) Gecko/20100101 Firefox/146.0")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Referer", "https://chat.z.ai/")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// 200 = valid, 401/403 = invalid
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
