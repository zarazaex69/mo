package zlm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
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
	var files []domain.FileAttachment
	chatID := utils.GenerateRequestID()
	userMsgID := utils.GenerateRequestID()

	for _, msg := range req.Messages {
		newMsg := map[string]interface{}{"role": msg.Role}

		if s, ok := msg.Content.(string); ok {
			newMsg["content"] = s
			msgs = append(msgs, newMsg)
			continue
		}

		// multimodal array
		if arr, ok := msg.Content.([]interface{}); ok {
			var textContent string

			for _, item := range arr {
				m, ok := item.(map[string]interface{})
				if !ok {
					continue
				}

				itemType, _ := m["type"].(string)

				if itemType == "text" {
					if t, ok := m["text"].(string); ok {
						textContent = t
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

					// upload if base64 and get full metadata
					uploaded, err := UploadImageFull(mediaURL, chatID, cfg)
					if err != nil {
						logger.Warn().Err(err).Msg("image upload failed")
						continue
					}

					if uploaded != nil {
						// build file attachment in z.ai format
						attachment := domain.FileAttachment{
							Type:   "image",
							File:   uploaded,
							ID:     uploaded.ID,
							URL:    fmt.Sprintf("/api/v1/files/%s/content", uploaded.ID),
							Name:   uploaded.Filename,
							Status: "uploaded",
							Size:   uploaded.Meta.Size,
							Error:  "",
							ItemID: utils.GenerateRequestID(),
							Media:  "image",
						}
						files = append(files, attachment)
					}
				}
			}

			newMsg["content"] = textContent
			msgs = append(msgs, newMsg)
		}
	}

	result["model"] = model
	result["messages"] = msgs
	result["stream"] = true
	result["params"] = map[string]interface{}{}

	// add files if any
	if len(files) > 0 {
		// add ref to user message
		filesWithRef := make([]map[string]interface{}, len(files))
		for i, f := range files {
			filesWithRef[i] = map[string]interface{}{
				"type":   f.Type,
				"file":   f.File,
				"id":     f.ID,
				"url":    f.URL,
				"name":   f.Name,
				"status": f.Status,
				"size":   f.Size,
				"error":  f.Error,
				"itemId": f.ItemID,
				"media":  f.Media,
				// link to user message
				"ref_user_msg_id": userMsgID,
			}
		}
		result["files"] = filesWithRef
		result["current_user_message_id"] = userMsgID
	}

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

// UploadImageFull uploads image and returns full file metadata
func UploadImageFull(dataURL, chatID string, cfg *config.Config) (*domain.UploadedFile, error) {
	if !strings.HasPrefix(dataURL, "data:") {
		return nil, nil
	}

	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid data url")
	}

	// extract content type from data url
	contentType := "image/png"
	ext := "png"
	if strings.Contains(parts[0], "image/jpeg") {
		contentType = "image/jpeg"
		ext = "jpg"
	} else if strings.Contains(parts[0], "image/gif") {
		contentType = "image/gif"
		ext = "gif"
	} else if strings.Contains(parts[0], "image/webp") {
		contentType = "image/webp"
		ext = "webp"
	}

	imgData, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}

	filename := fmt.Sprintf("%s.%s", utils.GenerateID(), ext)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// create form file with proper content type
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	h.Set("Content-Type", contentType)

	part, err := writer.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("create form: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(imgData)); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}
	writer.Close()

	authSvc := auth.NewService()
	user, err := authSvc.GetUser(cfg)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	uploadURL := fmt.Sprintf("%s//%s/api/v1/files/", cfg.Upstream.Protocol, cfg.Upstream.Host)
	req, err := http.NewRequest("POST", uploadURL, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
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
		return nil, fmt.Errorf("upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upload failed %d: %s", resp.StatusCode, string(b))
	}

	var result domain.UploadedFile
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	logger.Debug().
		Str("id", result.ID).
		Str("filename", result.Filename).
		Str("cdn_url", result.Meta.CdnURL).
		Msg("image uploaded")

	return &result, nil
}

// UploadImage legacy wrapper for backward compat
func UploadImage(dataURL, chatID string, cfg *config.Config) (string, error) {
	file, err := UploadImageFull(dataURL, chatID, cfg)
	if err != nil {
		return "", err
	}
	if file == nil {
		return "", nil
	}
	return fmt.Sprintf("%s_%s", file.ID, file.Filename), nil
}
