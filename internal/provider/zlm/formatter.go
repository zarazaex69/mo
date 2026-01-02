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

var glmBlockRegex = regexp.MustCompile(`<glm_block[^>]*tool_call_name="([^"]+)"[^>]*>(.+?)</glm_block>`)

// pre-compiled regexes
var (
	reGlmBlockStart  = regexp.MustCompile(`\n*<glm_block[^>]*>\{"type": "mcp", "data": \{"metadata": \{`)
	reGlmBlockEnd    = regexp.MustCompile(`[}"], "result": "".*</glm_block>`)
	reGlmBlockClose  = regexp.MustCompile(`null, "display_result": "".*</glm_block>`)
	reSummary        = regexp.MustCompile(`\n*<summary>.*?</summary>\n*`)
	reDetailsOpen    = regexp.MustCompile(`<details[^>]*>\n*`)
	reDetailsClose   = regexp.MustCompile(`\n*</details>`)
	reQuotePrefix    = regexp.MustCompile(`\n>\s?`)
	reReasoningOpen  = regexp.MustCompile(`<reasoning>\n*`)
	reReasoningClose = regexp.MustCompile(`\n*</reasoning>`)
)

type Formatter struct {
	cfg       *config.Config
	prevPhase string
}

func NewFormatter(cfg *config.Config) *Formatter {
	return &Formatter{
		cfg:       cfg,
		prevPhase: "thinking",
	}
}

func (f *Formatter) Format(data *domain.ZaiResponse) map[string]any {
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

	logger.Debug().
		Str("phase", phase).
		Int("len", len(content)).
		Msg("z.ai chunk")

	// tool_call handling
	if phase == "tool_call" {
		content = reGlmBlockStart.ReplaceAllString(content, "{")
		content = reGlmBlockEnd.ReplaceAllString(content, "")
	} else if phase == "other" && f.prevPhase == "tool_call" && strings.Contains(content, "glm_block") {
		phase = "tool_call"
		content = reGlmBlockClose.ReplaceAllString(content, "\"}")
	}

	content = f.formatThinking(phase, content)
	f.prevPhase = phase

	if phase == "thinking" && f.cfg.Model.ThinkMode == "reasoning" {
		return map[string]any{"role": "assistant", "reasoning_content": content}
	}

	if phase == "tool_call" {
		return map[string]any{"tool_call": content}
	}

	if content != "" {
		return map[string]any{"role": "assistant", "content": content}
	}

	return nil
}

func (f *Formatter) formatThinking(phase, content string) string {
	if phase != "thinking" && !(phase == "answer" && strings.Contains(content, "summary>")) {
		return content
	}

	content = strings.ReplaceAll(content, "</thinking>", "")
	content = strings.ReplaceAll(content, "<Full>", "")
	content = strings.ReplaceAll(content, "</Full>", "")

	if phase == "thinking" {
		content = reSummary.ReplaceAllString(content, "\n\n")
	}

	content = reDetailsOpen.ReplaceAllString(content, "<reasoning>\n\n")
	content = reDetailsClose.ReplaceAllString(content, "\n\n</reasoning>")

	switch f.cfg.Model.ThinkMode {
	case "reasoning":
		if phase == "thinking" {
			content = reQuotePrefix.ReplaceAllString(content, "\n")
		}
		content = reSummary.ReplaceAllString(content, "")

	case "think":
		if phase == "thinking" {
			content = reQuotePrefix.ReplaceAllString(content, "\n")
		}
		content = reSummary.ReplaceAllString(content, "")
		content = strings.ReplaceAll(content, "<reasoning>", "<think>")
		content = strings.ReplaceAll(content, "</reasoning>", "</think>")

	case "strip":
		content = reSummary.ReplaceAllString(content, "")
		content = reReasoningOpen.ReplaceAllString(content, "")
		content = reReasoningClose.ReplaceAllString(content, "")

	default:
		content = reReasoningClose.ReplaceAllString(content, "</reasoning>\n\n")
	}

	return content
}

func ParseSSEStream(resp *http.Response) <-chan *domain.ZaiResponse {
	ch := make(chan *domain.ZaiResponse)

	go func() {
		defer close(ch)

		scanner := bufio.NewScanner(resp.Body)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if len(line) == 0 || !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := line[6:]
			if strings.TrimSpace(data) == "[DONE]" {
				continue
			}

			var zaiResp domain.ZaiResponse
			if err := json.Unmarshal([]byte(data), &zaiResp); err != nil {
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

func ExtractTextFromMessages(msgs []domain.Message) string {
	var texts []string

	for _, msg := range msgs {
		if s, ok := msg.Content.(string); ok {
			texts = append(texts, s)
			continue
		}

		if arr, ok := msg.Content.([]any); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
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
	return tokenizer.Count(ExtractTextFromMessages(msgs))
}

func ParseToolCall(content string) *domain.ToolCall {
	matches := glmBlockRegex.FindStringSubmatch(content)
	if len(matches) < 3 {
		return nil
	}

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

	if err := json.Unmarshal([]byte(matches[2]), &wrapper); err != nil {
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
			Name:      matches[1],
			Arguments: args,
		},
	}
}

func StripToolCallBlock(content string) string {
	if !strings.Contains(content, "glm_block") {
		return content
	}
	return glmBlockRegex.ReplaceAllString(content, "")
}
