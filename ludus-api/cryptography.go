package ludusapi

import (
	"errors"

	"github.com/pocketbase/pocketbase/tools/security"
)

// Encrypts a string with AES-256-GCM using a key in the ludus config.yml
// Returns a base64 encoded string
func EncryptStringForDatabase(data string) (string, error) {
	if data == "" {
		return "", errors.New("data passed to EncryptStringForDatabase is empty")
	}
	encryptedString, err := security.Encrypt([]byte(data), ServerConfiguration.DatabaseEncryptionKey)
	if err != nil {
		return "", err
	}
	return encryptedString, nil
}

// Decrypts a base64 encoded string with AES-256-GCM using a key in the ludus config.yml
// Returns a string
func DecryptStringFromDatabase(encryptedData string) (string, error) {
	if encryptedData == "" {
		return "", errors.New("encrypted data passed to DecryptStringFromDatabase is empty")
	}
	decryptedBytes, err := security.Decrypt(encryptedData, ServerConfiguration.DatabaseEncryptionKey)
	if err != nil {
		return "", err
	}
	return string(decryptedBytes), nil
}
