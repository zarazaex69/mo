package provider

import (
	"net/http"

	"github.com/zarazaex69/mo/internal/domain"
)

type Provider interface {
	Name() string
	SendChatRequest(req *domain.ChatRequest, chatID string) (*http.Response, error)
	SupportsModel(model string) bool
}
