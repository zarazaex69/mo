package zlm

import (
	"bufio"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/zarazaex69/mo/internal/config"
	"github.com/zarazaex69/mo/internal/domain"
	"github.com/zarazaex69/mo/internal/pkg/logger"
	"github.com/zarazaex69/mo/internal/pkg/utils"
)

// regex for parsing glm_block tool calls
var glmBlockRegex = regexp.MustCompile(`<glm_block[^>]*tool_call_name="([^"]+)"[^>]*>(.+?)</glm_block>`)

var phaseBak = "thinking"

func ParseSSEStream(resp *http.Response) <-chan *domain.ZaiResponse {
	ch := make(chan *domain.ZaiResponse)

	go func() {
		defer close(ch)

		// use larger buffer for utf-8 content
		scanner := bufio.NewScanner(resp.Body)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()

			if len(line) == 0 || !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := line[6:] // skip "data: "

			if strings.TrimSpace(data) == "[DONE]" {
				continue
			}

			var zaiResp domain.ZaiResponse
			if err := json.Unmarshal([]byte(data), &zaiResp); err != nil {
				// try fallback for weird payloads
				var raw struct {
					Data string `json:"data"`
				}
				if json.Unmarshal([]byte(data), &raw) == nil && raw.Data != "" {
					continue
				}
				logger.Debug().Err(err).Str("data", data).Msg("parse sse failed")
				continue
			}

			ch <- &zaiResp
		}

		if err := scanner.Err(); err != nil {
			logger.Error().Err(err).Msg("sse read error")
		}
	}()

	return ch
}

func FormatResponse(data *domain.ZaiResponse, cfg *config.Config, skipEdit bool) map[string]interface{} {
	if data == nil || data.Data == nil {
		return nil
	}

	phase := data.Data.Phase
	if phase == "" {
		phase = "other"
	}

	// edit_content is a positional patch, not a simple delta
	// in non-stream mode we skip it because delta_content has full text
	isEdit := false
	content := data.Data.DeltaContent
	if content == "" && data.Data.EditContent != "" {
		if skipEdit {
			return nil
		}
		content = data.Data.EditContent
		isEdit = true
	}
	if content == "" {
		return nil
	}

	// debug raw content from z.ai
	logger.Debug().
		Str("phase", phase).
		Str("raw_content", content).
		Bool("is_edit", isEdit).
		Int("len", len(content)).
		Msg("z.ai chunk")

	// handle tool_call phase
	if phase == "tool_call" {
		content = regexp.MustCompile(`\n*<glm_block[^>]*>\{"type": "mcp", "data": \{"metadata": \{`).ReplaceAllString(content, "{")
		content = regexp.MustCompile(`[}"], "result": "".*</glm_block>`).ReplaceAllString(content, "")
	} else if phase == "other" && phaseBak == "tool_call" && strings.Contains(content, "glm_block") {
		phase = "tool_call"
		content = regexp.MustCompile(`null, "display_result": "".*</glm_block>`).ReplaceAllString(content, "\"}")
	}

	thinkMode := cfg.Model.ThinkMode

	// handle thinking/answer phase
	if phase == "thinking" || (phase == "answer" && strings.Contains(content, "summary>")) {
		content = strings.ReplaceAll(content, "</thinking>", "")
		content = strings.ReplaceAll(content, "<Full>", "")
		content = strings.ReplaceAll(content, "</Full>", "")

		if phase == "thinking" {
			content = regexp.MustCompile(`\n*<summary>.*?</summary>\n*`).ReplaceAllString(content, "\n\n")
		}

		// convert to <reasoning> tags
		content = regexp.MustCompile(`<details[^>]*>\n*`).ReplaceAllString(content, "<reasoning>\n\n")
		content = regexp.MustCompile(`\n*</details>`).ReplaceAllString(content, "\n\n</reasoning>")

		switch thinkMode {
		case "reasoning":
			if phase == "thinking" {
				content = regexp.MustCompile(`\n>\s?`).ReplaceAllString(content, "\n")
			}
			content = regexp.MustCompile(`\n*<summary>.*?</summary>\n*`).ReplaceAllString(content, "")
			content = regexp.MustCompile(`<reasoning>\n*`).ReplaceAllString(content, "")
			content = regexp.MustCompile(`\n*</reasoning>`).ReplaceAllString(content, "")

		case "think":
			if phase == "thinking" {
				content = regexp.MustCompile(`\n>\s?`).ReplaceAllString(content, "\n")
			}
			content = regexp.MustCompile(`\n*<summary>.*?</summary>\n*`).ReplaceAllString(content, "")
			content = strings.ReplaceAll(content, "<reasoning>", "<think>")
			content = strings.ReplaceAll(content, "</reasoning>", "</think>")

		case "strip":
			content = regexp.MustCompile(`\n*<summary>.*?</summary>\n*`).ReplaceAllString(content, "")
			content = regexp.MustCompile(`<reasoning>\n*`).ReplaceAllString(content, "")
			content = regexp.MustCompile(`</reasoning>`).ReplaceAllString(content, "")

		default:
			content = regexp.MustCompile(`</reasoning>`).ReplaceAllString(content, "</reasoning>\n\n")
		}
	}

	phaseBak = phase

	if phase == "thinking" && thinkMode == "reasoning" {
		return map[string]interface{}{
			"role":              "assistant",
			"reasoning_content": content,
			"is_edit":           isEdit,
		}
	}

	if phase == "tool_call" {
		return map[string]interface{}{
			"tool_call": content,
		}
	}

	if content != "" {
		return map[string]interface{}{
			"role":    "assistant",
			"content": content,
			"is_edit": isEdit,
		}
	}

	return nil
}

func ExtractTextFromMessages(msgs []domain.Message) string {
	var texts []string

	for _, msg := range msgs {
		if s, ok := msg.Content.(string); ok {
			texts = append(texts, s)
			continue
		}

		if arr, ok := msg.Content.([]interface{}); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]interface{}); ok {
					if m["type"] == "text" {
						if t, ok := m["text"].(string); ok {
							texts = append(texts, t)
						}
					}
				}
			}
		}
	}

	return strings.Join(texts, " ")
}

func CountTokens(msgs []domain.Message, tokenizer utils.Tokener) int {
	text := ExtractTextFromMessages(msgs)
	return tokenizer.Count(text)
}

// ParseToolCall extracts tool call from glm_block format
// returns nil if no tool call found
func ParseToolCall(content string) *domain.ToolCall {
	matches := glmBlockRegex.FindStringSubmatch(content)
	if len(matches) < 3 {
		return nil
	}

	toolName := matches[1]
	jsonData := matches[2]

	// parse the mcp wrapper
	var wrapper struct {
		Type string `json:"type"`
		Data struct {
			Metadata struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"metadata"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(jsonData), &wrapper); err != nil {
		logger.Debug().Err(err).Msg("failed to parse tool call json")
		return nil
	}

	callID := wrapper.Data.Metadata.ID
	if callID == "" {
		callID = "call_" + utils.GenerateID()[:10]
	}

	args := wrapper.Data.Metadata.Arguments
	if args == "" {
		args = "{}"
	}

	return &domain.ToolCall{
		ID:   callID,
		Type: "function",
		Function: domain.FunctionCall{
			Name:      toolName,
			Arguments: args,
		},
	}
}

// StripToolCallBlock removes glm_block from content
func StripToolCallBlock(content string) string {
	if !strings.Contains(content, "glm_block") {
		return content
	}
	// remove the glm_block and surrounding text that's part of tool call
	return glmBlockRegex.ReplaceAllString(content, "")
}
