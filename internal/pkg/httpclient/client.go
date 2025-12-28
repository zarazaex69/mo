package httpclient

import (
	"net/http"
	"net/url"
	"os"
	"time"
)

type Client struct {
	http *http.Client
}

func New(timeout time.Duration) *Client {
	transport := &http.Transport{}

	// support ALL_PROXY env
	if proxy := os.Getenv("ALL_PROXY"); proxy != "" {
		if proxyURL, err := url.Parse(proxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	return &Client{
		http: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.http.Do(req)
}
