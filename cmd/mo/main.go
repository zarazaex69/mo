package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/zarazaex69/mo/internal/config"
	"github.com/zarazaex69/mo/internal/pkg/logger"
	"github.com/zarazaex69/mo/internal/pkg/utils"
	"github.com/zarazaex69/mo/internal/server"
)

func main() {
	var configPath string
	var port int

	flag.StringVar(&configPath, "config", "", "path to config file")
	flag.StringVar(&configPath, "c", "", "path to config file (shorthand)")
	flag.IntVar(&port, "port", 0, "server port (overrides config)")
	flag.IntVar(&port, "p", 0, "server port (shorthand)")
	flag.Parse()

	if configPath == "" {
		candidates := []string{
			"configs/config.yaml",
			filepath.Join(os.Getenv("HOME"), ".config", "traw", "configs", "config.yaml"),
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				configPath = p
				break
			}
		}
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		println("config error:", err.Error())
		println("hint: use --config flag or place config in ~/.config/traw/configs/config.yaml")
		os.Exit(1)
	}

	if port > 0 {
		cfg.Server.Port = port
	}

	logger.Init(cfg.Server.Debug)

	tokenizer := utils.NewTokenizer()

	srv, err := server.New(cfg, tokenizer)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to init server")
		os.Exit(1)
	}
	defer srv.Close()

	if err := srv.Start(); err != nil {
		logger.Fatal().Err(err).Msg("server failed")
		os.Exit(1)
	}
}
