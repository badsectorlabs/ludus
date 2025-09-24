package ludusapi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const (
	defaultIterationCount = 100
	keyLength             = 32
)

var hashFunc = sha256.New

// Encrypts a string with AES-256-GCM using a key derived from the values in the ludus config.yml
// Returns a base64 encoded string
func EncryptStringForDatabase(data string) (string, error) {
	key, err := pbkdf2.Key(hashFunc, ServerConfiguration.DatabaseEncryptionPassword, []byte(ServerConfiguration.DatabaseEncryptionSalt), defaultIterationCount, keyLength)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	encryptedData := gcm.Seal(nonce, nonce, []byte(data), nil)
	return base64.StdEncoding.EncodeToString(encryptedData), nil
}

// Decrypts a base64 encoded string with AES-256-GCM using a key derived from the values in the ludus config.yml
// Returns a string
func DecryptStringFromDatabase(encryptedData string) (string, error) {
	key, err := pbkdf2.Key(hashFunc, ServerConfiguration.DatabaseEncryptionPassword, []byte(ServerConfiguration.DatabaseEncryptionSalt), defaultIterationCount, keyLength)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	decodedData, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(decodedData) < nonceSize {
		return "", fmt.Errorf("invalid cipher text")
	}

	nonce := decodedData[:nonceSize]
	ciphertext := decodedData[nonceSize:]

	decryptedData, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(decryptedData), nil
}
