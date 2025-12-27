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
	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		println("config error:", err.Error())
		println("hint: set ZAI_TOKEN env variable or check configs/config.yaml")
		os.Exit(1)
	}

	logger.Init(cfg.Server.Debug)

	authSvc := auth.NewService()
	sigGen := crypto.NewSignatureGenerator()
	tokenizer := utils.NewTokenizer()

	client := zlm.NewClient(cfg, authSvc, sigGen)
	srv := server.New(cfg, client, tokenizer)

	if err := srv.Start(); err != nil {
		logger.Fatal().Err(err).Msg("server failed")
		os.Exit(1)
	}
}
