package qwen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/zarazaex69/mo/internal/domain"
	"github.com/zarazaex69/mo/internal/pkg/httpclient"
	"github.com/zarazaex69/mo/internal/pkg/logger"
	"github.com/zarazaex69/mo/internal/pkg/tokenstore"
)

const (
	BaseURL = "https://portal.qwen.ai/v1"
)

var supportedModels = []string{
	"coder-model",
	"vision-model",
}

type Client struct {
	store *tokenstore.Store
}

func NewClient(store *tokenstore.Store) *Client {
	return &Client{store: store}
}

func (c *Client) Name() string {
	return "qwen"
}

func (c *Client) SupportsModel(model string) bool {
	for _, m := range supportedModels {
		if m == model {
			return true
		}
	}
	return false
}

func (c *Client) SendChatRequest(req *domain.ChatRequest, chatID string) (*http.Response, error) {
	token, err := c.getValidToken()
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	body := c.formatRequest(req)
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	apiURL := BaseURL + "/chat/completions"

	logger.Debug().
		Str("url", apiURL).
		Str("model", req.Model).
		Msg("qwen request")

	httpReq, err := http.NewRequest("POST", apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	client := httpclient.New(0)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		logger.Info().Msg("token expired, refreshing...")

		if err := c.refreshActiveToken(); err != nil {
			return nil, fmt.Errorf("refresh token: %w", err)
		}

		return c.SendChatRequest(req, chatID)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if strings.Contains(string(body), "invalid access token") || strings.Contains(string(body), "token expired") {
			logger.Info().Msg("token invalid, refreshing...")

			if err := c.refreshActiveToken(); err != nil {
				return nil, fmt.Errorf("refresh token: %w", err)
			}

			return c.SendChatRequest(req, chatID)
		}

		logger.Error().
			Int("status", resp.StatusCode).
			Str("body", string(body)).
			Msg("qwen error")

		return nil, domain.NewUpstreamError(resp.StatusCode, "qwen error")
	}

	return resp, nil
}

func (c *Client) getValidToken() (string, error) {
	active, err := c.store.GetActiveByProvider("qwen")
	if err != nil {
		return "", err
	}
	if active == nil {
		return "", fmt.Errorf("no active qwen token")
	}

	if IsTokenExpired(active.ExpiryDate) {
		logger.Info().Msg("token expired, refreshing...")
		if err := c.refreshActiveToken(); err != nil {
			return "", err
		}
		active, _ = c.store.GetActiveByProvider("qwen")
	}

	return active.Token, nil
}

func (c *Client) refreshActiveToken() error {
	active, err := c.store.GetActiveByProvider("qwen")
	if err != nil || active == nil {
		return fmt.Errorf("no active qwen token")
	}

	newToken, err := RefreshToken(active.RefreshToken)
	if err != nil {
		return err
	}

	active.Token = newToken.AccessToken
	active.RefreshToken = newToken.RefreshToken
	active.ExpiryDate = newToken.ExpiryDate

	return c.store.Update(active)
}

func (c *Client) formatRequest(req *domain.ChatRequest) map[string]any {
	result := map[string]any{
		"model":    req.Model,
		"messages": c.formatMessages(req.Messages),
		"stream":   req.Stream,
	}

	if req.Temperature != nil {
		result["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		result["max_tokens"] = *req.MaxTokens
	}
	if req.TopP != nil {
		result["top_p"] = *req.TopP
	}

	if len(req.Tools) > 0 && isToolsSupported(req.Model) {
		result["tools"] = req.Tools
	}

	return result
}

func (c *Client) formatMessages(msgs []domain.Message) []map[string]any {
	var result []map[string]any

	for _, msg := range msgs {
		m := map[string]any{"role": msg.Role}

		if s, ok := msg.Content.(string); ok {
			m["content"] = s
		} else if arr, ok := msg.Content.([]any); ok {
			m["content"] = arr
		}

		if msg.ToolCallID != "" {
			m["tool_call_id"] = msg.ToolCallID
		}
		if len(msg.ToolCalls) > 0 {
			m["tool_calls"] = msg.ToolCalls
		}

		result = append(result, m)
	}

	return result
}

func isToolsSupported(model string) bool {
	return model == "coder-model" || model == "vision-model"
}
