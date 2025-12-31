package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/zarazaex69/mo/internal/config"
	"github.com/zarazaex69/mo/internal/pkg/logger"
	"github.com/zarazaex69/mo/internal/pkg/tokenstore"
	"github.com/zarazaex69/mo/internal/pkg/utils"
	"github.com/zarazaex69/mo/internal/provider/zlm"
	"github.com/zarazaex69/mo/internal/service/auth"
)

type Server struct {
	cfg        *config.Config
	router     *chi.Mux
	client     *zlm.Client
	tokenizer  utils.Tokener
	tokenStore *tokenstore.Store
}

func New(cfg *config.Config, client *zlm.Client, tokenizer utils.Tokener) (*Server, error) {
	// data path from env or default to ~/.config/traw/data
	dataPath := os.Getenv("MO_DATA_PATH")
	if dataPath == "" {
		home, _ := os.UserHomeDir()
		dataPath = filepath.Join(home, ".config", "traw", "data")
	}

	store, err := tokenstore.New(filepath.Join(dataPath, "tokens"))
	if err != nil {
		return nil, fmt.Errorf("init token store: %w", err)
	}

	// connect auth service to token store
	auth.GetService().SetTokenStore(store)

	s := &Server{
		cfg:        cfg,
		router:     chi.NewRouter(),
		client:     client,
		tokenizer:  tokenizer,
		tokenStore: store,
	}
	s.routes()
	return s, nil
}

func (s *Server) Close() {
	if s.tokenStore != nil {
		s.tokenStore.Close()
	}
}

func (s *Server) routes() {
	s.router.Use(middleware.Logger)
	s.router.Use(middleware.Recoverer)
	s.router.Use(middleware.RealIP)
	s.router.Use(middleware.RequestID)

	s.router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	s.router.Get("/v1/models", ListModels(s.cfg))
	s.router.Post("/v1/chat/completions", ChatCompletions(s.cfg, s.client, s.tokenizer))

	// token management
	s.router.Post("/auth/register", RegisterAccount(s.tokenStore))
	s.router.Get("/auth/tokens", ListTokens(s.tokenStore))
	s.router.Delete("/auth/tokens/{id}", RemoveToken(s.tokenStore))
	s.router.Post("/auth/tokens/{id}/activate", ActivateToken(s.tokenStore))
	s.router.Get("/auth/tokens/{id}/validate", ValidateToken(s.tokenStore))
}

func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	logger.Info().Msgf("listening on %s", addr)
	return http.ListenAndServe(addr, s.router)
}
