package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strconv"
)

const defaultSecret = "key-@@@@)))()((9))-xxxx&&&%%%%%"

type SignatureResult struct {
	Signature string
	Timestamp int64
}

type SignatureGenerator interface {
	GenerateSignature(params map[string]string, lastUserMsg string) (*SignatureResult, error)
}

type sigGen struct{}

func NewSignatureGenerator() SignatureGenerator {
	return &sigGen{}
}

func (s *sigGen) GenerateSignature(params map[string]string, lastUserMsg string) (*SignatureResult, error) {
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

	canonical := fmt.Sprintf("requestId,%s,timestamp,%d,user_id,%s", reqID, ts, userID)

	w := base64.StdEncoding.EncodeToString([]byte(lastUserMsg))

	c := fmt.Sprintf("%s|%s|%s", canonical, w, tsStr)

	// 5 min window
	window := ts / (5 * 60 * 1000)
	windowStr := strconv.FormatInt(window, 10)

	secret := os.Getenv("ZAI_SECRET_KEY")
	if secret == "" {
		secret = defaultSecret
	}

	h1, err := hmacSha256([]byte(secret), []byte(windowStr))
	if err != nil {
		return nil, fmt.Errorf("hmac step1: %w", err)
	}
	a := hex.EncodeToString(h1)

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

// GeneratePassword creates a random password with given length
func GeneratePassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"

	result := make([]byte, length)
	for i := range result {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		result[i] = charset[n.Int64()]
	}
	return string(result)
}
