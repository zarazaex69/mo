package qwen

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	OAuthTokenURL = "https://chat.qwen.ai/api/v1/oauth2/token"
	DeviceCodeURL = "https://chat.qwen.ai/api/v1/oauth2/device/code"
	ClientID      = "f0304373b74a44d2b584a3fb70ca9e56"
	Scope         = "openid profile email model.completion"
)

type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	ResourceURL  string `json:"resource_url"`
	ExpiryDate   int64  `json:"expiry_date"`
}

type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	CodeVerifier            string `json:"-"`
}

func GeneratePKCE() (verifier, challenge string) {
	b := make([]byte, 32)
	rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)

	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
}

func RequestDeviceCode() (*DeviceCode, error) {
	verifier, challenge := GeneratePKCE()

	data := url.Values{}
	data.Set("client_id", ClientID)
	data.Set("scope", Scope)
	data.Set("code_challenge", challenge)
	data.Set("code_challenge_method", "S256")

	req, err := http.NewRequest("POST", DeviceCodeURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed: %d", resp.StatusCode)
	}

	var result DeviceCode
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	result.CodeVerifier = verifier
	return &result, nil
}

func PollForToken(deviceCode, codeVerifier string) (*OAuthToken, error) {
	data := url.Values{}
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	data.Set("client_id", ClientID)
	data.Set("device_code", deviceCode)
	data.Set("code_verifier", codeVerifier)

	req, err := http.NewRequest("POST", OAuthTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Error        string `json:"error"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
		ResourceURL  string `json:"resource_url"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if result.Error == "authorization_pending" {
		return nil, nil
	}

	if result.Error != "" {
		return nil, fmt.Errorf("oauth error: %s", result.Error)
	}

	if result.AccessToken == "" {
		return nil, fmt.Errorf("no access token in response")
	}

	return &OAuthToken{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		TokenType:    result.TokenType,
		ExpiresIn:    result.ExpiresIn,
		Scope:        result.Scope,
		ResourceURL:  result.ResourceURL,
		ExpiryDate:   time.Now().Add(time.Duration(result.ExpiresIn) * time.Second).UnixMilli(),
	}, nil
}

func RefreshToken(refreshToken string) (*OAuthToken, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("client_id", ClientID)

	req, err := http.NewRequest("POST", OAuthTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth failed with status %d", resp.StatusCode)
	}

	var result struct {
		Status       string `json:"status"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
		ResourceURL  string `json:"resource_url"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("oauth status: %s", result.Status)
	}

	return &OAuthToken{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		TokenType:    result.TokenType,
		ExpiresIn:    result.ExpiresIn,
		Scope:        result.Scope,
		ResourceURL:  result.ResourceURL,
		ExpiryDate:   time.Now().Add(time.Duration(result.ExpiresIn) * time.Second).UnixMilli(),
	}, nil
}

func IsTokenExpired(expiryDate int64) bool {
	return time.Now().UnixMilli() >= expiryDate
}
