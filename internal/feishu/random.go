package feishu

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
