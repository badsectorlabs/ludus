package ludusapi

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Chown a file to a user and their group
func chownFileToUsername(filePath string, username string) {
	uid, gid, err := getUIDandGIDFromUsername(username)
	if err != nil {
		logger.Error("Failed to get UID and GID for user " + username + ": " + err.Error())
		return
	}

	// Change ownership of the file
	err = os.Chown(filePath, uid, gid)
	if err != nil {
		logger.Error("Failed to change ownership of the file: " + err.Error())
		return
	}
}

func chownDirToUsernameRecursive(filePath string, username string) {
	uid, gid, err := getUIDandGIDFromUsername(username)
	if err != nil {
		fmt.Printf("Failed to get UID and GID for user %s: %s\n", username, err)
		return
	}
	err = filepath.Walk(filePath, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// os.Chown requires numeric UID and GID
		if err := os.Chown(path, uid, gid); err != nil {
			return fmt.Errorf("failed to chown %s: %w", path, err)
		}
		return nil
	})
	if err != nil {
		fmt.Printf("Failed to chown directory %s: %s\n", filePath, err)
		return
	}
}
