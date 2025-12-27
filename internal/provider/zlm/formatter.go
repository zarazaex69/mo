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

var phaseBak = "thinking"

func ParseSSEStream(resp *http.Response) <-chan *domain.ZaiResponse {
	ch := make(chan *domain.ZaiResponse)

	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(resp.Body)

		for scanner.Scan() {
			line := scanner.Bytes()

			if len(line) == 0 || !strings.HasPrefix(string(line), "data: ") {
				continue
			}

			data := line[6:] // skip "data: "

			if strings.TrimSpace(string(data)) == "[DONE]" {
				continue
			}

			var zaiResp domain.ZaiResponse
			if err := json.Unmarshal(data, &zaiResp); err != nil {
				// try fallback for weird payloads
				var raw struct {
					Data string `json:"data"`
				}
				if json.Unmarshal(data, &raw) == nil && raw.Data != "" {
					continue
				}
				logger.Debug().Err(err).Msg("parse sse failed")
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

func FormatResponse(data *domain.ZaiResponse, cfg *config.Config) map[string]interface{} {
	if data == nil || data.Data == nil {
		return nil
	}

	phase := data.Data.Phase
	if phase == "" {
		phase = "other"
	}

	content := data.Data.DeltaContent
	if content == "" {
		content = data.Data.EditContent
	}
	if content == "" {
		return nil
	}

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
