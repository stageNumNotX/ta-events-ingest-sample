package traffic

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func newClickID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mint click_id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
