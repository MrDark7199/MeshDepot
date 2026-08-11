// Package crypto encrypts platform credentials with a per-user derived key.
//
// New values are AES-256-GCM in the format base64( "MDG1" + nonce[12] +
// ciphertext+tag ). GCM authenticates: a modified ciphertext fails to open
// instead of decrypting to attacker-chosen plaintext.
//
// Values written before that change are AES-256-CBC + PKCS7 in the format
// base64( iv[16] + ciphertext ). They are still readable (there is no migration
// for rows already in the database), but nothing writes that format any more; a
// credential is upgraded the next time it is saved or its token is refreshed.
// CBC without a MAC is malleable, so treat legacy values as tamper-able by
// anyone with write access to the database.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
)

const (
	initializationVectorLength = 16
	keyContext                 = "meshdepot-cred-v2:"
	// gcmMagic marks the authenticated format. It is not a security boundary,
	// only a format hint: a legacy value whose random IV happens to start with
	// these bytes simply fails the GCM open and falls back to CBC.
	gcmMagic = "MDG1"
)

// Crypto encrypts platform credentials with a master key (APP_KEY).
type Crypto struct {
	appKey []byte
}

// New creates a Crypto helper with the given master key.
func New(appKey string) *Crypto {
	return &Crypto{appKey: []byte(appKey)}
}

// deriveKey derives the 32-byte AES key for a user:
// HMAC-SHA256(APP_KEY, "meshdepot-cred-v2:" + userID).
func (helper *Crypto) deriveKey(userID int) []byte {
	hasher := hmac.New(sha256.New, helper.appKey)
	hasher.Write([]byte(keyContext + strconv.Itoa(userID)))
	return hasher.Sum(nil)
}

// Encrypt encrypts plaintext for a user with AES-256-GCM and a fresh random
// nonce. It returns base64( "MDG1" + nonce + ciphertext+tag ).
func (helper *Crypto) Encrypt(plaintext string, userID int) (string, error) {
	aead, failure := helper.aead(userID)
	if failure != nil {
		return "", failure
	}
	nonce := make([]byte, aead.NonceSize())
	if _, failure := rand.Read(nonce); failure != nil {
		return "", failure
	}
	// The magic and the nonce are the prefix of the output and are also the
	// additional data, so neither can be swapped between two stored values.
	envelope := append([]byte(gcmMagic), nonce...)
	sealed := aead.Seal(nil, nonce, []byte(plaintext), envelope)
	return base64.StdEncoding.EncodeToString(append(envelope, sealed...)), nil
}

// Decrypt decrypts a value produced by Encrypt, and still reads the legacy
// CBC format written before the GCM switch. ok=false means unreadable:
// malformed input, a wrong key, or - for GCM - a modified ciphertext.
func (helper *Crypto) Decrypt(encrypted string, userID int) (string, bool) {
	raw, failure := base64.StdEncoding.DecodeString(encrypted)
	if failure != nil {
		return "", false
	}
	if plaintext, ok := helper.decryptGCM(raw, userID); ok {
		return plaintext, true
	}
	return helper.decryptLegacyCBC(raw, userID)
}

// aead builds the AES-256-GCM instance for a user.
func (helper *Crypto) aead(userID int) (cipher.AEAD, error) {
	block, failure := aes.NewCipher(helper.deriveKey(userID))
	if failure != nil {
		return nil, failure
	}
	return cipher.NewGCM(block)
}

// decryptGCM reads the authenticated format. It returns ok=false for anything
// that is not that format, so the caller can try the legacy one.
func (helper *Crypto) decryptGCM(raw []byte, userID int) (string, bool) {
	if len(raw) < len(gcmMagic) || string(raw[:len(gcmMagic)]) != gcmMagic {
		return "", false
	}
	aead, failure := helper.aead(userID)
	if failure != nil {
		return "", false
	}
	envelopeLength := len(gcmMagic) + aead.NonceSize()
	if len(raw) < envelopeLength+aead.Overhead() {
		return "", false
	}
	envelope, sealed := raw[:envelopeLength], raw[envelopeLength:]
	plaintext, failure := aead.Open(nil, envelope[len(gcmMagic):], sealed, envelope)
	if failure != nil {
		return "", false
	}
	return string(plaintext), true
}

// decryptLegacyCBC reads base64( iv[16] + ciphertext ) as written before the
// GCM switch.
func (helper *Crypto) decryptLegacyCBC(raw []byte, userID int) (string, bool) {
	if len(raw) <= initializationVectorLength {
		return "", false
	}
	initializationVector, ciphertext := raw[:initializationVectorLength], raw[initializationVectorLength:]
	block, failure := aes.NewCipher(helper.deriveKey(userID))
	if failure != nil || len(ciphertext)%block.BlockSize() != 0 {
		return "", false
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, initializationVector).CryptBlocks(plaintext, ciphertext)
	unpadded, failure := pkcs7Unpad(plaintext, block.BlockSize())
	if failure != nil {
		return "", false
	}
	return string(unpadded), true
}

// pkcs7Unpad removes PKCS7 padding and validates it. Only the legacy CBC path
// needs it; nothing pads any more.
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	length := len(data)
	if length == 0 || length%blockSize != 0 {
		return nil, errors.New("pkcs7: invalid length")
	}
	padLength := int(data[length-1])
	if padLength == 0 || padLength > blockSize {
		return nil, errors.New("pkcs7: invalid padding")
	}
	for _, paddingByte := range data[length-padLength:] {
		if int(paddingByte) != padLength {
			return nil, errors.New("pkcs7: bad padding byte")
		}
	}
	return data[:length-padLength], nil
}
