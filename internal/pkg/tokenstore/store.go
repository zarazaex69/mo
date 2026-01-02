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
	ID           string    `json:"id"`
	Provider     string    `json:"provider"`
	Email        string    `json:"email"`
	Token        string    `json:"token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiryDate   int64     `json:"expiry_date,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	IsActive     bool      `json:"is_active"`
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
	return s.AddWithProvider("glm", email, token, "", 0)
}

func (s *Store) AddWithProvider(provider, email, token, refreshToken string, expiryDate int64) (*Token, error) {
	t := &Token{
		ID:           uuid.New().String()[:8],
		Provider:     provider,
		Email:        email,
		Token:        token,
		RefreshToken: refreshToken,
		ExpiryDate:   expiryDate,
		CreatedAt:    time.Now(),
		IsActive:     false,
	}

	tokens, _ := s.ListByProvider(provider)
	if len(tokens) == 0 {
		t.IsActive = true
	}

	if err := s.save(t); err != nil {
		return nil, err
	}

	return t, nil
}

func (s *Store) Update(t *Token) error {
	return s.save(t)
}

func (s *Store) Remove(id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte("token:" + id))
	})
}

func (s *Store) SetActive(id string) error {
	t, err := s.GetByID(id)
	if err != nil || t == nil {
		return fmt.Errorf("token not found")
	}

	tokens, err := s.ListByProvider(t.Provider)
	if err != nil {
		return err
	}

	for _, tok := range tokens {
		tok.IsActive = (tok.ID == id)
		if err := s.save(tok); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) GetActive() (*Token, error) {
	return s.GetActiveByProvider("glm")
}

func (s *Store) GetActiveByProvider(provider string) (*Token, error) {
	tokens, err := s.ListByProvider(provider)
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

func (s *Store) GetByID(id string) (*Token, error) {
	var token *Token

	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("token:" + id))
		if err != nil {
			return err
		}

		return item.Value(func(val []byte) error {
			var t Token
			if err := json.Unmarshal(val, &t); err != nil {
				return err
			}
			token = &t
			return nil
		})
	})

	if err == badger.ErrKeyNotFound {
		return nil, nil
	}

	return token, err
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
				if t.Provider == "" {
					t.Provider = "glm"
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

func (s *Store) ListByProvider(provider string) ([]*Token, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}

	var filtered []*Token
	for _, t := range all {
		if t.Provider == provider {
			filtered = append(filtered, t)
		}
	}

	return filtered, nil
}

func (s *Store) save(t *Token) error {
	if t.Provider == "" {
		t.Provider = "glm"
	}

	data, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}

	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte("token:"+t.ID), data)
	})
}

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

	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
