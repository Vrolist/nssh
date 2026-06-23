package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const (
	TakeoverKey = "nssh_takeover_secret_v1"
)

type Transport interface {
	StartServer(handler func(int, map[string]string, string, int64) string) error
	SendCommand(cmd int, params map[string]string, timestamp int64) (string, error)
	Stop() error
}

type Request struct {
	Cmd       int               `json:"cmd"`
	Params    map[string]string `json:"params"`
	Timestamp int64             `json:"timestamp"`
	Key       string            `json:"key"`
}

type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func EncryptKeyWithTime(timestamp int64) string {
	data := fmt.Sprintf("%s_%d", TakeoverKey, timestamp)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

func VerifyKeyWithTime(timestamp int64, encryptedKey string) bool {
	expected := EncryptKeyWithTime(timestamp)
	return expected == encryptedKey
}
