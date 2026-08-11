package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// base32Alphabet is the RFC 4648 alphabet (without padding), as in the PHP auth.
const base32Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// generateTotpSecret creates a random 20-character base32 secret (equivalent to
// Auth::generateTotpSecret).
func generateTotpSecret() string {
	randomBytes := make([]byte, 20)
	_, _ = rand.Read(randomBytes)
	var builder strings.Builder
	for _, randomByte := range randomBytes {
		builder.WriteByte(base32Alphabet[int(randomByte)&31])
	}
	return builder.String()
}

// base32Decode decodes a base32 secret to raw bytes; incomplete trailing bits
// (<8) are discarded.
func base32Decode(secret string) []byte {
	secret = strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	var buffer uint64
	var bits uint
	var decoded []byte
	for index := 0; index < len(secret); index++ {
		position := strings.IndexByte(base32Alphabet, secret[index])
		if position < 0 {
			continue
		}
		buffer = buffer<<5 | uint64(position)
		bits += 5
		if bits >= 8 {
			bits -= 8
			decoded = append(decoded, byte(buffer>>bits))
		}
	}
	return decoded
}

// totpCode computes the 6-digit TOTP code for a 30-second window with the given
// offset (RFC 6238, HMAC-SHA1).
func totpCode(secret string, offset int) string {
	key := base32Decode(secret)
	counter := uint64(int64(time.Now().Unix()/30) + int64(offset))
	counterBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(counterBytes, counter)
	hasher := hmac.New(sha1.New, key)
	hasher.Write(counterBytes)
	sum := hasher.Sum(nil)
	dynamicOffset := sum[len(sum)-1] & 0x0f
	code := (binary.BigEndian.Uint32(sum[dynamicOffset:dynamicOffset+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", code)
}

// verifyTotp checks a code with ±1 period of drift and blocks replays (used
// codes are remembered for 90s) - like Auth::verifyTotp.
func (service *Auth) verifyTotp(secret, code string, userID int) bool {
	for _, offset := range []int{-1, 0, 1} {
		if totpCode(secret, offset) == code {
			usedKey := fmt.Sprintf("totp_used:%d:%s", userID, code)
			if _, used := service.used.Get(usedKey); used {
				return false
			}
			service.used.Set(usedKey, "1", 90*time.Second)
			return true
		}
	}
	return false
}
