package ludusapi

import (
	"github.com/pocketbase/pocketbase/tools/security"
)

// Encrypts a string with AES-256-GCM using a key in the ludus config.yml
// Returns a base64 encoded string
func EncryptStringForDatabase(data string) (string, error) {
	encryptedString, err := security.Encrypt([]byte(data), ServerConfiguration.DatabaseEncryptionKey)
	if err != nil {
		return "", err
	}
	return encryptedString, nil
}

// Decrypts a base64 encoded string with AES-256-GCM using a key in the ludus config.yml
// Returns a string
func DecryptStringFromDatabase(encryptedData string) (string, error) {
	decryptedBytes, err := security.Decrypt(encryptedData, ServerConfiguration.DatabaseEncryptionKey)
	if err != nil {
		return "", err
	}
	return string(decryptedBytes), nil
}
