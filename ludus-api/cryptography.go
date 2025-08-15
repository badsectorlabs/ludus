package ludusapi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"
)

const (
	defaultIterationCount = 100
	keyLength             = 32
)

var hashFunc = sha256.New
var machineID = getMachineIDOrFallback()
var hostSSHKey = getHostSSHKeyOrFallback()

// Get the machine ID from the /etc/machine-id file
// If we can't read the machine ID, return a static string and print a warning to the console
func getMachineIDOrFallback() string {
	// Get the machine ID from the /etc/machine-id file
	machineID, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		log.Printf("Error reading /etc/machine-id, database encryption will use a known, shared key: %v", err)
		return "208bbe5dd02cc23a3d7450816a641fed" // If we can't read the machine ID, return a static string
	}
	return strings.TrimSuffix(string(machineID), "\n")
}

// Get the host SSH key from the /etc/ssh/ssh_host_ed25519_key.pub file
// If we can't read the host SSH key, return a static string and print a warning to the console
func getHostSSHKeyOrFallback() string {
	hostSSHKey, err := os.ReadFile("/etc/ssh/ssh_host_ed25519_key.pub")
	if err != nil {
		log.Printf("Error reading /etc/ssh/ssh_host_ed25519_key.pub, database encryption will use a known, shared salt: %v", err)
		return "hZD6RwYxrcQ7CS4lRxjdKI7thWp3jg48kaVts8DEE8pQrJXAi6s1X2eQp1jxJFBQL3yn" // If we can't read the host SSH key, return a static string
	}
	// The key is in the format "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl root@debian"
	// Take the second part of the string to use as the salt
	return strings.Split(string(hostSSHKey), " ")[1]
}

// Encrypts a string with AES-256-GCM using a key derived from the machine ID and host SSH key
// Returns a base64 encoded string
func EncryptStringForDatabase(data string) (string, error) {
	key, err := pbkdf2.Key(hashFunc, machineID, []byte(hostSSHKey), defaultIterationCount, keyLength)
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

// Decrypts a base64 encoded string with AES-256-GCM using a key derived from the machine ID and host SSH key
// Returns a string
func DecryptStringFromDatabase(encryptedData string) (string, error) {
	key, err := pbkdf2.Key(hashFunc, machineID, []byte(hostSSHKey), defaultIterationCount, keyLength)
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
