package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
)

const (
	// defaultSecretKey is the hardcoded secret.
	// Used as fallback if ZAI_SECRET_KEY env var is not set.
	defaultSecretKey = "key-@@@@)))()((9))-xxxx&&&%%%%%"
)

// SignatureResult holds the generated signature and timestamp.
type SignatureResult struct {
	Signature string
	Timestamp int64
}

// SignatureGenerator defines the interface for generating signatures.
type SignatureGenerator interface {
	GenerateSignature(params map[string]string, lastUserMessage string) (*SignatureResult, error)
}

// defaultSignatureGenerator is the default implementation of SignatureGenerator.
type defaultSignatureGenerator struct{}

// NewSignatureGenerator creates a new default SignatureGenerator.
func NewSignatureGenerator() SignatureGenerator {
	return &defaultSignatureGenerator{}
}

// GenerateSignature generates a signature for the given parameters and last user message.
func (s *defaultSignatureGenerator) GenerateSignature(params map[string]string, lastUserMessage string) (*SignatureResult, error) {
	// Extract required parameters
	requestID := params["requestId"]
	timestampStr := params["timestamp"]
	userID := params["user_id"]

	if requestID == "" || timestampStr == "" || userID == "" {
		return nil, fmt.Errorf("missing required parameters for signature generation")
	}

	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp: %w", err)
	}

	// 1. Prepare Canonical String
	// Format: requestId,UUID,timestamp,TS,user_id,UUID
	canonical := fmt.Sprintf("requestId,%s,timestamp,%d,user_id,%s", requestID, timestamp, userID)

	// 2. Encode prompt to Base64
	w := base64.StdEncoding.EncodeToString([]byte(lastUserMessage))

	// 3. Form the string to sign: e|w|i
	c := fmt.Sprintf("%s|%s|%s", canonical, w, timestampStr)

	// 4. Calculate time window (5 minutes)
	window := timestamp / (5 * 60 * 1000)
	windowStr := strconv.FormatInt(window, 10)

	// Get secret key from environment variable or use default
	secretKey := os.Getenv("ZAI_SECRET_KEY")
	if secretKey == "" {
		secretKey = defaultSecretKey
	}

	// 5. Step 1: HMAC-SHA256(E, secret)
	// Key is secret, data is windowStr
	hash1, err := hmacSha256([]byte(secretKey), []byte(windowStr))
	if err != nil {
		return nil, fmt.Errorf("failed to generate first hmac: %w", err)
	}
	A := hex.EncodeToString(hash1)

	// 6. Step 2: HMAC-SHA256(c, A)
	// Key is A (hex string bytes), data is c
	hash2, err := hmacSha256([]byte(A), []byte(c))
	if err != nil {
		return nil, fmt.Errorf("failed to generate second hmac: %w", err)
	}
	signature := hex.EncodeToString(hash2)

	return &SignatureResult{
		Signature: signature,
		Timestamp: timestamp,
	}, nil
}

func hmacSha256(key, data []byte) ([]byte, error) {
	h := hmac.New(sha256.New, key)
	if _, err := h.Write(data); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
