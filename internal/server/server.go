package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/zarazaex69/mo/internal/config"
	"github.com/zarazaex69/mo/internal/pkg/logger"
	"github.com/zarazaex69/mo/internal/pkg/tokenstore"
	"github.com/zarazaex69/mo/internal/pkg/utils"
	"github.com/zarazaex69/mo/internal/provider/qwen"
	"github.com/zarazaex69/mo/internal/provider/zlm"
	"github.com/zarazaex69/mo/internal/service/auth"
)

type Server struct {
	cfg        *config.Config
	router     *chi.Mux
	zlmClient  *zlm.Client
	qwenClient *qwen.Client
	tokenizer  utils.Tokener
	tokenStore *tokenstore.Store
}

func New(cfg *config.Config, client *zlm.Client, tokenizer utils.Tokener) (*Server, error) {
	dataPath := os.Getenv("MO_DATA_PATH")
	if dataPath == "" {
		home, _ := os.UserHomeDir()
		dataPath = filepath.Join(home, ".config", "traw", "data")
	}

	store, err := tokenstore.New(filepath.Join(dataPath, "tokens"))
	if err != nil {
		return nil, fmt.Errorf("init token store: %w", err)
	}

	auth.GetService().SetTokenStore(store)

	qwenClient := qwen.NewClient(store)

	s := &Server{
		cfg:        cfg,
		router:     chi.NewRouter(),
		zlmClient:  client,
		qwenClient: qwenClient,
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

	s.router.Get("/v1/models", ListModels(s.cfg, s.tokenStore))
	s.router.Post("/v1/chat/completions", s.handleChatCompletions())

	s.router.Route("/auth/glm", func(r chi.Router) {
		r.Post("/register", RegisterAccount(s.tokenStore))
		r.Get("/tokens", ListTokensByProvider(s.tokenStore, "glm"))
		r.Delete("/tokens/{id}", RemoveToken(s.tokenStore))
		r.Post("/tokens/{id}/activate", ActivateToken(s.tokenStore))
		r.Get("/tokens/{id}/validate", ValidateTokenByID(s.tokenStore))
	})

	s.router.Route("/auth/qwen", func(r chi.Router) {
		r.Post("/register", RegisterQwenAccount(s.tokenStore))
		r.Get("/tokens", ListTokensByProvider(s.tokenStore, "qwen"))
		r.Delete("/tokens/{id}", RemoveToken(s.tokenStore))
		r.Post("/tokens/{id}/activate", ActivateToken(s.tokenStore))
		r.Get("/tokens/{id}/validate", ValidateTokenByID(s.tokenStore))
	})
}

func (s *Server) handleChatCompletions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))

		var peek struct {
			Model string `json:"model"`
		}
		json.Unmarshal(body, &peek)

		if isQwenModel(peek.Model) {
			r.Body = io.NopCloser(bytes.NewReader(body))
			QwenChatCompletions(s.cfg, s.qwenClient, s.tokenizer)(w, r)
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(body))
		ChatCompletions(s.cfg, s.zlmClient, s.tokenizer)(w, r)
	}
}

func isQwenModel(model string) bool {
	return model == "coder-model" || model == "vision-model"
}

func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	logger.Info().Msgf("listening on %s", addr)
	return http.ListenAndServe(addr, s.router)
}
