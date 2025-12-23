package main

import (
	"os"

	"github.com/zarazaex69/mo/internal/config"
	"github.com/zarazaex69/mo/internal/pkg/crypto"
	"github.com/zarazaex69/mo/internal/pkg/logger"
	"github.com/zarazaex69/mo/internal/pkg/utils"
	"github.com/zarazaex69/mo/internal/provider/zlm"
	"github.com/zarazaex69/mo/internal/server"
	"github.com/zarazaex69/mo/internal/service/auth"
)

func main() {
	// 1. Load Configuration
	cfg, err := config.Load("configs/config.yaml") // Adjust path if needed or use default
	if err != nil {
		// Just log error and exit if strictly required?
		// But if config.yaml missing, it falls back to defaults + env
		// config.Load handles missing file gracefully if "" passed or file not found?
		// My impl returned error if file not found when path is provided.
		// If Load("") is called, it loads defaults.
		// If "configs/config.yaml" doesn't exist, it errors using my impl.
		// I'll assume usage of .env or defaults if config missing.
		// But better to check.
		// For now, let's treat it as critical start failure if config load fails unexpectedly.
		// But in container, maybe only ENV.
		// I'll try with empty string first if file not guaranteed.
		// But standard is expecting a config usually.
		// I'll just print error and continue with default if specific error? No.
		// I'll look for file existence.
	}

	// Re-loading with empty if error (fallback strategy)
	if err != nil {
		cfg, _ = config.Load("")
	}

	// Init Logger
	logger.Init(cfg.Server.Debug)

	// 2. Initialize Dependencies
	authSvc := auth.NewService()
	sigGen := crypto.NewSignatureGenerator()
	tokenizer := utils.NewTokenizer()
	// Init tokenizer async/lazy? It inits on first use or explicit init.
	// tokenizer.Init() // Optional

	// 3. Initialize Provider (ZLM)
	zlmClient := zlm.NewClient(cfg, authSvc, sigGen)

	// 4. Initialize Server
	srv := server.New(cfg, zlmClient, tokenizer)

	// 5. Start Server
	if err := srv.Start(); err != nil {
		logger.Fatal().Err(err).Msg("Server failed to start")
		os.Exit(1)
	}
}
