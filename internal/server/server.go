package server

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/zarazaex69/mo/internal/config"
	"github.com/zarazaex69/mo/internal/pkg/logger"
	"github.com/zarazaex69/mo/internal/pkg/utils"
	"github.com/zarazaex69/mo/internal/provider/zlm"
)

type Server struct {
	cfg       *config.Config
	router    *chi.Mux
	aiClient  *zlm.Client
	tokenizer utils.Tokener
}

func New(cfg *config.Config, aiClient *zlm.Client, tokenizer utils.Tokener) *Server {
	s := &Server{
		cfg:       cfg,
		router:    chi.NewRouter(),
		aiClient:  aiClient,
		tokenizer: tokenizer,
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	s.router.Use(middleware.Logger)
	s.router.Use(middleware.Recoverer)
	s.router.Use(middleware.RealIP)
	s.router.Use(middleware.RequestID)

	s.router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	s.router.Get("/v1/models", ListModels(s.cfg))
	s.router.Post("/v1/chat/completions", ChatCompletions(s.cfg, s.aiClient, s.tokenizer))
}

func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	logger.Info().Msgf("listening on %s", addr)
	return http.ListenAndServe(addr, s.router)
}
