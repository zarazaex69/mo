package utils

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/pkoukk/tiktoken-go"
	"github.com/zarazaex69/mo/internal/pkg/logger"
)

// Tokenizer handles token counting using tiktoken.
// It implements the utils.Tokener interface.
type Tokenizer struct {
	encoder *tiktoken.Tiktoken
	initErr error
	once    sync.Once
}

// NewTokenizer creates a new Tokenizer instance.
func NewTokenizer() *Tokenizer {
	return &Tokenizer{}
}

// Init initializes the tiktoken encoder for the tokenizer.
// This method is safe to call multiple times.
func (t *Tokenizer) Init() error {
	t.once.Do(func() {
		// Set cache directory to avoid downloading on every run
		cacheDir := filepath.Join(".", "tiktoken")
		os.Setenv("TIKTOKEN_CACHE_DIR", cacheDir)

		// Ensure cache directory exists
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			t.initErr = err
			logger.Warn().Err(err).Msg("Failed to create tiktoken cache directory")
			return
		}

		// Initialize encoder with cl100k_base (used by GPT-4 and compatible models)
		var err error
		t.encoder, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			t.initErr = err
			logger.Warn().Err(err).Msg("Failed to initialize tiktoken encoder")
			return
		}

		logger.Info().Msg("Tokenizer initialized successfully")
	})

	return t.initErr
}

// Count counts the number of tokens in the given text.
// It will attempt to initialize the tokenizer if it hasn't been already.
// Returns 0 if the tokenizer is not properly initialized.
func (t *Tokenizer) Count(text string) int {
	if t.encoder == nil {
		if err := t.Init(); err != nil {
			logger.Debug().Err(err).Msg("Tokenizer not available, returning 0 tokens")
			return 0
		}
	}

	if t.encoder == nil {
		// This should not happen if Init() succeeded, but as a safeguard.
		return 0
	}

	tokens := t.encoder.Encode(text, nil, nil)
	return len(tokens)
}
