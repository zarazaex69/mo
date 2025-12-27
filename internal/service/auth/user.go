package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/zarazaex69/mo/internal/config"
	"github.com/zarazaex69/mo/internal/domain"
	"github.com/zarazaex69/mo/internal/pkg/httpclient"
	"github.com/zarazaex69/mo/internal/pkg/logger"
)

type AuthServicer interface {
	GetUser(cfg *config.Config) (*domain.User, error)
}

type Service struct {
	cache map[string]*cachedUser
	mu    sync.RWMutex
}

type cachedUser struct {
	user     *domain.User
	cachedAt time.Time
}

func NewService() *Service {
	return &Service{
		cache: make(map[string]*cachedUser),
	}
}

func (s *Service) GetUser(cfg *config.Config) (*domain.User, error) {
	token := cfg.Upstream.Token
	if token == "" {
		return nil, fmt.Errorf("token required")
	}

	// check cache
	s.mu.RLock()
	cached, ok := s.cache[token]
	s.mu.RUnlock()

	// 30 min ttl
	if ok && time.Since(cached.cachedAt) < 30*time.Minute {
		return cached.user, nil
	}

	// fetch from api
	url := fmt.Sprintf("%s//%s/api/v1/auths/", cfg.Upstream.Protocol, cfg.Upstream.Host)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	for k, v := range cfg.GetUpstreamHeaders() {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := httpclient.New(10 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth api returned %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	userID := getString(result, "id")
	userName := getString(result, "name")

	user := &domain.User{
		ID:    userID,
		Token: token,
	}

	// cache it
	if userID != "" {
		s.mu.Lock()
		s.cache[token] = &cachedUser{user: user, cachedAt: time.Now()}
		s.mu.Unlock()
		logger.Info().Str("user_id", userID).Str("name", userName).Msg("user authenticated")
	}

	return user, nil
}

func (s *Service) ClearCache() {
	s.mu.Lock()
	s.cache = make(map[string]*cachedUser)
	s.mu.Unlock()
	logger.Info().Msg("cache cleared")
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
