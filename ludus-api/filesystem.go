package ludusapi

import (
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
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

func chownDirRecursive(filePath string, uid, gid int) error {
	return filepath.Walk(filePath, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := os.Chown(path, uid, gid); err != nil {
			return fmt.Errorf("failed to chown %s: %w", path, err)
		}
		return nil
	})
}

func chownDirToUsernameRecursive(filePath string, username string) {
	uid, gid, err := getUIDandGIDFromUsername(username)
	if err != nil {
		fmt.Printf("Failed to get UID and GID for user %s: %s\n", username, err)
		return
	}
	if err := chownDirRecursive(filePath, uid, gid); err != nil {
		fmt.Printf("Failed to chown directory %s: %s\n", filePath, err)
	}
}

func setRangeDirPermissions(filePath string) {
	uid, _, err := getUIDandGIDFromUsername("ludus")
	if err != nil {
		logger.Error("Failed to get UID for user ludus: " + err.Error())
		return
	}
	group, err := user.LookupGroup("ludus")
	if err != nil {
		logger.Error("Failed to get GID for group ludus: " + err.Error())
		return
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		logger.Error("Failed to convert ludus GID to integer: " + err.Error())
		return
	}
	if err := chownDirRecursive(filePath, uid, gid); err != nil {
		logger.Error("Failed to chown range directory: " + err.Error())
		return
	}
	if err := os.Chmod(filePath, 0770); err != nil {
		logger.Error("Failed to chmod range directory: " + err.Error())
	}
}
