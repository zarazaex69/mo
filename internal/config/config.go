package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Upstream UpstreamConfig `yaml:"upstream"`
	Model    ModelConfig    `yaml:"model"`
	Headers  HeadersConfig  `yaml:"headers"`
}

type ServerConfig struct {
	Port    int    `yaml:"port"`
	Host    string `yaml:"host"`
	Debug   bool   `yaml:"debug"`
	Version string `yaml:"version"`
}

type UpstreamConfig struct {
	Protocol string `yaml:"protocol"`
	Host     string `yaml:"host"`
	Token    string `yaml:"token"`
}

type ModelConfig struct {
	Default   string `yaml:"default"`
	ThinkMode string `yaml:"think_mode"`
}

type HeadersConfig struct {
	Accept          string `yaml:"accept"`
	AcceptLanguage  string `yaml:"accept_language"`
	UserAgent       string `yaml:"user_agent"`
	SecChUa         string `yaml:"sec_ch_ua"`
	SecChUaMobile   string `yaml:"sec_ch_ua_mobile"`
	SecChUaPlatform string `yaml:"sec_ch_ua_platform"`
	XFEVersion      string `yaml:"x_fe_version"`
}

var (
	cfg  *Config
	once sync.Once
)

func Load(configPath string) (*Config, error) {
	var err error
	once.Do(func() {
		cfg, err = loadConfig(configPath)
	})
	return cfg, err
}

func Get() *Config {
	if cfg == nil {
		cfg, _ = loadConfig("")
	}
	return cfg
}

func loadConfig(configPath string) (*Config, error) {
	_ = godotenv.Load()

	c := defaultConfig()

	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := yaml.Unmarshal(data, c); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	c.applyEnvOverrides()

	if err := c.validate(); err != nil {
		return nil, err
	}

	return c, nil
}

func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:    8080,
			Host:    "0.0.0.0",
			Debug:   false,
			Version: "0.1.0",
		},
		Upstream: UpstreamConfig{
			Protocol: "https:",
			Host:     "chat.z.ai",
			Token:    "",
		},
		Model: ModelConfig{
			Default:   "GLM-4-6-API-V1",
			ThinkMode: "reasoning",
		},
		Headers: HeadersConfig{
			Accept:          "*/*",
			AcceptLanguage:  "en-US",
			UserAgent:       "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
			SecChUa:         `"Chromium";v="141", "Not?A_Brand";v="8"`,
			SecChUaMobile:   "?0",
			SecChUaPlatform: "Linux",
			XFEVersion:      "prod-fe-1.0.117",
		},
	}
}

func (c *Config) applyEnvOverrides() {
	if port := getEnvInt("PORT", 0); port != 0 {
		c.Server.Port = port
	}
	if host := getEnv("HOST", ""); host != "" {
		c.Server.Host = host
	}
	if debug := getEnvBool("DEBUG", false); debug {
		c.Server.Debug = debug
	}

	if token := getEnv("ZAI_TOKEN", ""); token != "" {
		c.Upstream.Token = strings.TrimSpace(token)
	}

	if model := getEnv("MODEL", ""); model != "" {
		c.Model.Default = model
	}
	if thinkMode := getEnv("THINK_MODE", ""); thinkMode != "" {
		c.Model.ThinkMode = thinkMode
	}
}

func (c *Config) validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Server.Port)
	}

	validModes := []string{"reasoning", "think", "strip", "details"}
	valid := false
	for _, m := range validModes {
		if c.Model.ThinkMode == m {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid think_mode: %s", c.Model.ThinkMode)
	}

	// token is required in strict mode
	if c.Upstream.Token == "" {
		return fmt.Errorf("ZAI_TOKEN is required")
	}

	return nil
}

func (c *Config) GetUpstreamHeaders() map[string]string {
	return map[string]string{
		"Accept":             c.Headers.Accept,
		"Accept-Language":    c.Headers.AcceptLanguage,
		"Cache-Control":      "no-cache",
		"Connection":         "keep-alive",
		"Pragma":             "no-cache",
		"Sec-Ch-Ua":          c.Headers.SecChUa,
		"Sec-Ch-Ua-Mobile":   c.Headers.SecChUaMobile,
		"Sec-Ch-Ua-Platform": c.Headers.SecChUaPlatform,
		"Sec-Fetch-Dest":     "empty",
		"Sec-Fetch-Mode":     "cors",
		"Sec-Fetch-Site":     "same-origin",
		"User-Agent":         c.Headers.UserAgent,
		"X-FE-Version":       c.Headers.XFEVersion,
		"Origin":             c.Upstream.Protocol + "//" + c.Upstream.Host,
		"Referer":            c.Upstream.Protocol + "//" + c.Upstream.Host + "/",
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		return strings.ToLower(v) == "true" || v == "1"
	}
	return def
}
