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

// Config holds all application configuration
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Upstream UpstreamConfig `yaml:"upstream"`
	Model    ModelConfig    `yaml:"model"`
	Headers  HeadersConfig  `yaml:"headers"`
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Port    int    `yaml:"port"`
	Host    string `yaml:"host"`
	Debug   bool   `yaml:"debug"`
	Version string `yaml:"version"`
}

// UpstreamConfig holds Z.AI API configuration
type UpstreamConfig struct {
	Protocol string `yaml:"protocol"`
	Host     string `yaml:"host"`
	Token    string `yaml:"token"`
	// Anonymous removed as we strictly require token
}

// ModelConfig holds AI model configuration
type ModelConfig struct {
	Default   string `yaml:"default"`
	ThinkMode string `yaml:"think_mode"` // reasoning, think, strip, details
}

// HeadersConfig holds HTTP headers for upstream requests
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

// Load loads configuration from file and environment variables
func Load(configPath string) (*Config, error) {
	var err error
	once.Do(func() {
		cfg, err = loadConfig(configPath)
	})
	return cfg, err
}

// Get returns the singleton config instance
func Get() *Config {
	if cfg == nil {
		// Fallback to default config if not loaded
		cfg, _ = loadConfig("")
	}
	return cfg
}

func loadConfig(configPath string) (*Config, error) {
	// Load .env file first (ignore error if not exists)
	_ = godotenv.Load()

	// Initialize with default configuration first
	c := defaultConfig()

	// Try to load from YAML file if provided
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		// Unmarshal YAML on top of default configuration
		if err := yaml.Unmarshal(data, c); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	// Override with environment variables
	c.applyEnvOverrides()

	// Validate configuration
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
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
	// Server overrides
	if port := getEnvInt("PORT", 0); port != 0 {
		c.Server.Port = port
	}
	if host := getEnv("HOST", ""); host != "" {
		c.Server.Host = host
	}
	if debug := getEnvBool("DEBUG", false); debug {
		c.Server.Debug = debug
	}

	// Upstream overrides
	if token := getEnv("ZAI_TOKEN", ""); token != "" {
		c.Upstream.Token = strings.TrimSpace(token)
	}

	// Model overrides
	if model := getEnv("MODEL", ""); model != "" {
		c.Model.Default = model
	}
	if thinkMode := getEnv("THINK_MODE", ""); thinkMode != "" {
		c.Model.ThinkMode = thinkMode
	}
}

func (c *Config) validate() error {
	// Validate server port
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d (must be 1-65535)", c.Server.Port)
	}

	// Validate think mode
	validThinkModes := []string{"reasoning", "think", "strip", "details"}
	valid := false
	for _, mode := range validThinkModes {
		if c.Model.ThinkMode == mode {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid think mode: %s (must be one of: %v)", c.Model.ThinkMode, validThinkModes)
	}

	// Enforce strict token presence to prevent unauthorized access.
	// Unlike development modes, the production configuration explicitly disables anonymous access
	// to ensure all requests are actionable, billable, and traceable to a valid user account.
	if c.Upstream.Token == "" {
		return fmt.Errorf("upstream token (ZAI_TOKEN) is required in this strict mode")
	}

	return nil
}

// GetUpstreamHeaders returns headers for upstream Z.AI requests
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

// Helper functions

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return strings.ToLower(value) == "true" || value == "1"
	}
	return defaultValue
}
