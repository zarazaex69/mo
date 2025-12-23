package zlm

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// Client handles communication with Z.AI API
type Client struct {
	cfg          *config.Config
	authService  auth.AuthServicer
	signatureGen crypto.SignatureGenerator
}

// NewClient creates a new Z.AI API client
func NewClient(cfg *config.Config, authSvc auth.AuthServicer, sigGen crypto.SignatureGenerator) *Client {
	return &Client{
		cfg:          cfg,
		authService:  authSvc,
		signatureGen: sigGen,
	}
}

// SendChatRequest initiates a synchronous HTTP request to the upstream AI provider to obtain chat completions.
// It acts as a facade, handling authentication, request signing, and protocol adaptation to ensure
// seamless integration with the Z.AI API while abstracting these complexities from the caller.
func (c *Client) SendChatRequest(req *domain.ChatRequest, chatID string) (*http.Response, error) {
	timestamp := time.Now().UnixMilli()
	requestID := utils.GenerateRequestID()

	// Retrieve authenticated user credentials to ensure upstream requests are authorized and traceable.
	// This step is mandatory as anonymous access is strictly prohibited in this configuration.
	user, err := c.authService.GetUser(c.cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}

	// Construct necessary query parameters to satisfy the upstream API versioning and tracking requirements.
	// Accurate timestamps and request IDs are critical for replay attack prevention and debugging.
	params := url.Values{}
	params.Set("timestamp", fmt.Sprintf("%d", timestamp))
	params.Set("requestId", requestID)
	params.Set("version", "0.0.1")
	params.Set("platform", "web")
	params.Set("token", user.Token)

	// Populate HTTP headers to comply with the upstream contract, including content negotiation
	// and origin falsification (if necessary for proxying).
	headers := c.cfg.GetUpstreamHeaders()
	headers["Authorization"] = "Bearer " + user.Token
	headers["Content-Type"] = "application/json"
	headers["Referer"] = fmt.Sprintf("%s//%s/c/%s", c.cfg.Upstream.Protocol, c.cfg.Upstream.Host, chatID)

	// Transform the internal domain request model into the specific payload format required by Z.AI.
	// This ensures that any discrepancies between our internal API and the upstream API are bridged here.
	requestBody, err := FormatRequest(req, c.cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to format request: %w", err)
	}

	// Append metadata fields that are expected by the upstream backend but not present in the standard OpenAI schema.
	requestBody["chat_id"] = chatID
	requestBody["id"] = utils.GenerateRequestID()

	// Inject cryptographic signatures for legitimate user authentication.
	// This verifies the integrity of the request and binds it to a specific user context.
	params.Set("user_id", user.ID)

	// Extract the most recent user prompt to serve as the message payload for signature generation.
	lastUserMsg := extractLastUserMessage(req.Messages)

	// Generate and attach the HMAC signature to verify request authenticity and prevent tampering.
	sigParams := map[string]string{
		"requestId": requestID,
		"timestamp": fmt.Sprintf("%d", timestamp),
		"user_id":   user.ID,
	}
	sigResult, err := c.signatureGen.GenerateSignature(sigParams, lastUserMsg)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to generate signature, continuing without it")
	} else {
		headers["x-signature"] = sigResult.Signature
		params.Set("signature_timestamp", fmt.Sprintf("%d", sigResult.Timestamp))
		requestBody["signature_prompt"] = lastUserMsg
	}

	// Compose the full upstream URL.
	apiURL := fmt.Sprintf("%s//%s/api/v2/chat/completions?%s",
		c.cfg.Upstream.Protocol, c.cfg.Upstream.Host, params.Encode())

	// Serialize the request body.
	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	logger.Debug().
		Str("url", apiURL).
		Str("chat_id", chatID).
		Str("model", req.Model).
		Msg("Sending chat request to Z.AI")

	// Create the HTTP request object.
	httpReq, err := http.NewRequest("POST", apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Apply the prepared headers to the request.
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	// Execute the request using a zero-timeout client to support potential long-lived streaming responses.
	// A timeout here would prematurely cut off the stream from the AI provider.
	client := httpclient.New(0)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, domain.NewUpstreamError(resp.StatusCode, "Z.AI API error")
	}

	return resp, nil
}

// extractLastUserMessage extracts the last user message content for signature
func extractLastUserMessage(messages []domain.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			// Handle string content
			if contentStr, ok := messages[i].Content.(string); ok {
				return contentStr
			}

			// Handle array content (multimodal)
			if contentArr, ok := messages[i].Content.([]interface{}); ok {
				var texts []string
				for _, item := range contentArr {
					if itemMap, ok := item.(map[string]interface{}); ok {
						if itemMap["type"] == "text" {
							if text, ok := itemMap["text"].(string); ok {
								texts = append(texts, text)
							}
						}
					}
				}
				return strings.Join(texts, " ")
			}
		}
	}
	return ""
}
