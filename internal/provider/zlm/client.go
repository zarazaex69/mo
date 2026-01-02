package zlm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zarazaex69/mo/internal/config"
	"github.com/zarazaex69/mo/internal/domain"
	"github.com/zarazaex69/mo/internal/pkg/crypto"
	"github.com/zarazaex69/mo/internal/pkg/httpclient"
	"github.com/zarazaex69/mo/internal/pkg/logger"
	"github.com/zarazaex69/mo/internal/pkg/utils"
	"github.com/zarazaex69/mo/internal/service/auth"
)

var supportedModels = []string{
	"GLM-4-6-API-V1",
	"GLM-4-Flash",
	"GLM-4-Air",
	"GLM-4-Plus",
}

type Client struct {
	cfg    *config.Config
	auth   auth.AuthServicer
	sigGen crypto.SignatureGenerator
}

func NewClient(cfg *config.Config, authSvc auth.AuthServicer, sigGen crypto.SignatureGenerator) *Client {
	return &Client{
		cfg:    cfg,
		auth:   authSvc,
		sigGen: sigGen,
	}
}

func (c *Client) Name() string {
	return "zlm"
}

func (c *Client) SupportsModel(model string) bool {
	for _, m := range supportedModels {
		if m == model {
			return true
		}
	}
	return !strings.HasPrefix(model, "coder-") && !strings.HasPrefix(model, "vision-")
}

func (c *Client) SendChatRequest(req *domain.ChatRequest, chatID string) (*http.Response, error) {
	ts := time.Now().UnixMilli()
	reqID := utils.GenerateRequestID()

	user, err := c.auth.GetUser(c.cfg)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	params := url.Values{}
	params.Set("timestamp", fmt.Sprintf("%d", ts))
	params.Set("requestId", reqID)
	params.Set("version", "0.0.1")
	params.Set("platform", "web")
	params.Set("token", user.Token)

	headers := c.cfg.GetUpstreamHeaders()
	headers["Authorization"] = "Bearer " + user.Token
	headers["Content-Type"] = "application/json"
	headers["Referer"] = fmt.Sprintf("%s//%s/c/%s", c.cfg.Upstream.Protocol, c.cfg.Upstream.Host, chatID)

	body, err := FormatRequest(req, c.cfg)
	if err != nil {
		return nil, fmt.Errorf("format request: %w", err)
	}

	body["chat_id"] = chatID
	body["id"] = utils.GenerateRequestID()

	params.Set("user_id", user.ID)

	lastMsg := extractLastUserMessage(req.Messages)

	sigParams := map[string]string{
		"requestId": reqID,
		"timestamp": fmt.Sprintf("%d", ts),
		"user_id":   user.ID,
	}
	sig, err := c.sigGen.GenerateSignature(sigParams, lastMsg)
	if err != nil {
		logger.Warn().Err(err).Msg("signature failed, continuing without it")
	} else {
		headers["x-signature"] = sig.Signature
		params.Set("signature_timestamp", fmt.Sprintf("%d", sig.Timestamp))
		body["signature_prompt"] = lastMsg
	}

	apiURL := fmt.Sprintf("%s//%s/api/v2/chat/completions?%s",
		c.cfg.Upstream.Protocol, c.cfg.Upstream.Host, params.Encode())

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	logger.Debug().
		Str("url", apiURL).
		Str("chat_id", chatID).
		Str("model", req.Model).
		RawJSON("body", bodyBytes).
		Msg("sending request")

	httpReq, err := http.NewRequest("POST", apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	client := httpclient.New(0)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		logger.Error().
			Int("status", resp.StatusCode).
			Str("body", string(body)).
			Msg("upstream returned error")

		return nil, domain.NewUpstreamError(resp.StatusCode, "upstream error")
	}

	return resp, nil
}

func extractLastUserMessage(msgs []domain.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}

		if s, ok := msgs[i].Content.(string); ok {
			return s
		}

		if arr, ok := msgs[i].Content.([]interface{}); ok {
			var texts []string
			for _, item := range arr {
				if m, ok := item.(map[string]interface{}); ok {
					if m["type"] == "text" {
						if t, ok := m["text"].(string); ok {
							texts = append(texts, t)
						}
					}
				}
			}
			return strings.Join(texts, " ")
		}
	}
	return ""
}
