package zlm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/zarazaex69/mo/internal/config"
	"github.com/zarazaex69/mo/internal/domain"
	"github.com/zarazaex69/mo/internal/pkg/logger"
	"github.com/zarazaex69/mo/internal/pkg/utils"
	"github.com/zarazaex69/mo/internal/service/auth"
)

func FormatRequest(req *domain.ChatRequest, cfg *config.Config) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	model := req.Model
	if model == "" {
		model = cfg.Model.Default
	}

	var msgs []map[string]interface{}
	chatID := utils.GenerateRequestID()

	for _, msg := range req.Messages {
		newMsg := map[string]interface{}{"role": msg.Role}

		if s, ok := msg.Content.(string); ok {
			newMsg["content"] = s
			msgs = append(msgs, newMsg)
			continue
		}

		// multimodal array
		if arr, ok := msg.Content.([]interface{}); ok {
			var content interface{} = ""

			for _, item := range arr {
				m, ok := item.(map[string]interface{})
				if !ok {
					continue
				}

				itemType, _ := m["type"].(string)

				if itemType == "text" {
					if t, ok := m["text"].(string); ok {
						content = t
					}
					continue
				}

				if itemType == "image_url" {
					mediaURL := ""
					if imgURL, ok := m["image_url"].(map[string]interface{}); ok {
						if u, ok := imgURL["url"].(string); ok {
							mediaURL = u
						}
					}

					if mediaURL == "" {
						continue
					}

					// upload if base64
					uploaded, err := UploadImage(mediaURL, chatID, cfg)
					if err != nil {
						logger.Warn().Err(err).Msg("image upload failed")
						continue
					}
					if uploaded != "" {
						mediaURL = uploaded
					}

					// convert to array if needed
					if s, ok := content.(string); ok {
						content = []map[string]interface{}{
							{"type": "text", "text": s},
						}
					}
					if slice, ok := content.([]map[string]interface{}); ok {
						slice = append(slice, map[string]interface{}{
							"type":      "image_url",
							"image_url": map[string]interface{}{"url": mediaURL},
						})
						content = slice
					}
				}
			}

			newMsg["content"] = content
			msgs = append(msgs, newMsg)
		}
	}

	result["model"] = model
	result["messages"] = msgs
	result["stream"] = true
	result["params"] = map[string]interface{}{}

	if len(req.Tools) > 0 {
		tools := make([]map[string]interface{}, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]interface{}{
				"name":         t.Function.Name,
				"description":  t.Function.Description,
				"input_schema": t.Function.Parameters,
			}
		}
		result["tools"] = tools
	}

	features := map[string]interface{}{
		"image_generation": false,
		"web_search":       false,
		"auto_web_search":  false,
	}

	if req.Thinking != nil {
		features["thinking"] = *req.Thinking
	}

	result["features"] = features

	return result, nil
}

func UploadImage(dataURL, chatID string, cfg *config.Config) (string, error) {
	if !strings.HasPrefix(dataURL, "data:") {
		return "", nil
	}

	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid data url")
	}

	imgData, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}

	filename := utils.GenerateID()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create form: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(imgData)); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	writer.Close()

	authSvc := auth.NewService()
	user, err := authSvc.GetUser(cfg)
	if err != nil {
		return "", fmt.Errorf("get user: %w", err)
	}

	uploadURL := fmt.Sprintf("%s//%s/api/v1/files/", cfg.Upstream.Protocol, cfg.Upstream.Host)
	req, err := http.NewRequest("POST", uploadURL, body)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	for k, v := range cfg.GetUpstreamHeaders() {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", "Bearer "+user.Token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Referer", fmt.Sprintf("%s//%s/c/%s", cfg.Upstream.Protocol, cfg.Upstream.Host, chatID))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload failed %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return fmt.Sprintf("%s_%s", result.ID, result.Filename), nil
}
