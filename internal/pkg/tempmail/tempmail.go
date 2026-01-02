package tempmail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	apiURL = "https://api.internal.temp-mail.io/api/v3"
	ua     = "Mozilla/5.0 (X11; Linux x86_64; rv:146.0) Gecko/20100101 Firefox/146.0"
)

type Email struct {
	Address string
	Token   string
}

type Message struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Subject   string    `json:"subject"`
	BodyText  string    `json:"body_text"`
	BodyHTML  string    `json:"body_html"`
	CreatedAt time.Time `json:"created_at"`
}

type Client struct {
	http *http.Client
}

func New() *Client {
	return &Client{
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) headers() http.Header {
	h := http.Header{}
	h.Set("User-Agent", ua)
	h.Set("Accept", "*/*")
	h.Set("Content-Type", "application/json")
	h.Set("Application-Name", "web")
	h.Set("Application-Version", "4.0.0")
	h.Set("X-CORS-Header", "iaWg3pchvFx48fY")
	h.Set("Origin", "https://temp-mail.io")
	h.Set("Referer", "https://temp-mail.io/")
	return h
}

func (c *Client) CreateEmail() (*Email, error) {
	body := []byte(`{"min_name_length":10,"max_name_length":10}`)

	req, err := http.NewRequest("POST", apiURL+"/email/new", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header = c.headers()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %d", resp.StatusCode)
	}

	var result struct {
		Email string `json:"email"`
		Token string `json:"token"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &Email{Address: result.Email, Token: result.Token}, nil
}

func (c *Client) GetMessages(email string) ([]Message, error) {
	url := fmt.Sprintf("%s/email/%s/messages", apiURL, email)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header = c.headers()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bad status: %d, body: %s", resp.StatusCode, body)
	}

	var messages []Message
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return messages, nil
}

// WaitForMessage polls until a matching message arrives
func (c *Client) WaitForMessage(email, fromMatch, subjectMatch string, timeout, interval time.Duration) (*Message, error) {
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	if interval == 0 {
		interval = 3 * time.Second
	}

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		messages, err := c.GetMessages(email)
		if err != nil {
			return nil, err
		}

		for _, m := range messages {
			if fromMatch != "" && !strings.Contains(strings.ToLower(m.From), strings.ToLower(fromMatch)) {
				continue
			}
			if subjectMatch != "" && !strings.Contains(strings.ToLower(m.Subject), strings.ToLower(subjectMatch)) {
				continue
			}
			return &m, nil
		}

		time.Sleep(interval)
	}

	return nil, nil
}

// ExtractVerifyLink finds Z.ai verification link in message body
func ExtractVerifyLink(text string) string {
	idx := strings.Index(text, "https://chat.z.ai/auth/verify_email?")
	if idx == -1 {
		return ""
	}

	end := idx
	for end < len(text) && text[end] != ' ' && text[end] != '\n' && text[end] != '\r' {
		end++
	}

	link := text[idx:end]
	link = strings.ReplaceAll(link, "&amp;", "&")

	return link
}

func ExtractQwenActivationLink(text string) string {
	idx := strings.Index(text, "https://chat.qwen.ai/api/v1/auths/activate?")
	if idx == -1 {
		return ""
	}

	end := idx
	for end < len(text) && text[end] != ' ' && text[end] != '\n' && text[end] != '\r' && text[end] != ')' {
		end++
	}

	link := text[idx:end]
	link = strings.ReplaceAll(link, "&amp;", "&")

	return link
}
