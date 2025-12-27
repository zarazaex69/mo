package httpclient

import (
	"net/http"
	"time"
)

type Client struct {
	http *http.Client
}

func New(timeout time.Duration) *Client {
	return &Client{
		http: &http.Client{Timeout: timeout},
	}
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.http.Do(req)
}
