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

const defaultSecretKey = "key-@@@@)))()((9))-xxxx&&&%%%%%"

type SignatureResult struct {
	Signature string
	Timestamp int64
}

type SignatureGenerator interface {
	GenerateSignature(params map[string]string, lastUserMsg string) (*SignatureResult, error)
}

type signatureGen struct{}

func NewSignatureGenerator() SignatureGenerator {
	return &signatureGen{}
}

func (s *signatureGen) GenerateSignature(params map[string]string, lastUserMsg string) (*SignatureResult, error) {
	reqID := params["requestId"]
	tsStr := params["timestamp"]
	userID := params["user_id"]

	if reqID == "" || tsStr == "" || userID == "" {
		return nil, fmt.Errorf("missing required params")
	}

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp: %w", err)
	}

	// canonical string format
	canonical := fmt.Sprintf("requestId,%s,timestamp,%d,user_id,%s", reqID, ts, userID)

	// base64 encode the prompt
	w := base64.StdEncoding.EncodeToString([]byte(lastUserMsg))

	// string to sign: canonical|prompt|timestamp
	c := fmt.Sprintf("%s|%s|%s", canonical, w, tsStr)

	// 5 min window
	window := ts / (5 * 60 * 1000)
	windowStr := strconv.FormatInt(window, 10)

	secret := os.Getenv("ZAI_SECRET_KEY")
	if secret == "" {
		secret = defaultSecretKey
	}

	// step 1: hmac(secret, window)
	h1, err := hmacSha256([]byte(secret), []byte(windowStr))
	if err != nil {
		return nil, fmt.Errorf("hmac step1: %w", err)
	}
	a := hex.EncodeToString(h1)

	// step 2: hmac(a, c)
	h2, err := hmacSha256([]byte(a), []byte(c))
	if err != nil {
		return nil, fmt.Errorf("hmac step2: %w", err)
	}

	return &SignatureResult{
		Signature: hex.EncodeToString(h2),
		Timestamp: ts,
	}, nil
}

func hmacSha256(key, data []byte) ([]byte, error) {
	h := hmac.New(sha256.New, key)
	_, err := h.Write(data)
	if err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
