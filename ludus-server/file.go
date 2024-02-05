package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/user"
	"strconv"
)

// Chown a file to a user and their group
func chownFileToUsername(filePath string, username string) {
	runnerUser, err := user.Lookup(username)
	if err != nil {
		fmt.Printf("Failed to lookup user %s for chown of %s\n", err, filePath)
		return
	}

	uid, err := strconv.Atoi(runnerUser.Uid)
	if err != nil {
		fmt.Printf("Failed to convert UID to integer: %s\n", err)
		return
	}

	gid, err := strconv.Atoi(runnerUser.Gid)
	if err != nil {
		fmt.Printf("Failed to convert GID to integer: %s\n", err)
		return
	}

	// Change ownership of the file
	err = os.Chown(filePath, uid, gid)
	if err != nil {
		fmt.Printf("Failed to change ownership of the file: %s\n", err)
		return
	}
}

// return true if the file exists and is not a directory, else false
func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		// Permission error potentially
		log.Printf("%v", err)
		return false
	}
	return !info.IsDir()
}

// return true if the path exists (file or directory)
func exists(filePath string) bool {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return false
	}

	return true
}

// copy a file from src to dst
func copy(src, dst string) {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		log.Fatal(err.Error())
	}

	if !sourceFileStat.Mode().IsRegular() {
		log.Fatal(fmt.Errorf("%s is not a regular file", src))
	}

	source, err := os.Open(src)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer destination.Close()
	_, err = io.Copy(destination, source)
	if err != nil {
		log.Fatal(err.Error())
	}
}
