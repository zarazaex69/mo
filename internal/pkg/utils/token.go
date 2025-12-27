package utils

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/pkoukk/tiktoken-go"
	"github.com/zarazaex69/mo/internal/pkg/logger"
)

type Tokenizer struct {
	encoder *tiktoken.Tiktoken
	initErr error
	once    sync.Once
}

func NewTokenizer() *Tokenizer {
	return &Tokenizer{}
}

func (t *Tokenizer) Init() error {
	t.once.Do(func() {
		cacheDir := filepath.Join(".", "tiktoken")
		os.Setenv("TIKTOKEN_CACHE_DIR", cacheDir)

		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			t.initErr = err
			logger.Warn().Err(err).Msg("failed to create tiktoken cache dir")
			return
		}

		var err error
		t.encoder, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			t.initErr = err
			logger.Warn().Err(err).Msg("failed to init tiktoken")
			return
		}

		logger.Info().Msg("tokenizer ready")
	})

	return t.initErr
}

func (t *Tokenizer) Count(text string) int {
	if t.encoder == nil {
		if err := t.Init(); err != nil {
			return 0
		}
	}

	if t.encoder == nil {
		return 0
	}

	return len(t.encoder.Encode(text, nil, nil))
}
