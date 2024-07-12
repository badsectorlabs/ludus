package ludusapi

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Return the contents of a provided file
func GetFileContents(path string) (string, error) {
	dat, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(dat), nil
}

// Return the line count of a file or an error
func lineCounter(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	buf := make([]byte, 32*1024)
	count := 0
	lineSep := []byte{'\n'}

	for {
		c, err := file.Read(buf)
		count += bytes.Count(buf[:c], lineSep)

		switch {
		case err == io.EOF:
			return count, nil

		case err != nil:
			return count, err
		}
	}
}

func writeStringToFile(path string, contents string) {
	// Open a file. If it doesn't exist, create it, otherwise append to it
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0660)
	if err != nil {
		log.Println(err.Error())
	}
	defer file.Close()

	// Write the contents string to the file
	_, err = file.WriteString(contents)
	if err != nil {
		log.Println(err.Error())
	}
}

func findFiles(rootDir, pattern1, pattern2 string) ([]string, error) {
	var files []string

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (strings.HasSuffix(path, pattern1) || strings.HasSuffix(path, pattern2)) {
			files = append(files, path)
		}
		return nil
	})

	return files, err
}

// Untar takes a source tar file and untars it into the specified destination directory.
func Untar(tarFile, destDir string) error {
	// Open the tar file
	file, err := os.Open(tarFile)
	if err != nil {
		return fmt.Errorf("failed to open tar file: %w", err)
	}
	defer file.Close()

	// Create a new tar reader
	tarReader := tar.NewReader(file)

	// Iterate through the files in the tar archive
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break // End of tar file
		}
		if err != nil {
			return fmt.Errorf("failed to read tar file: %w", err)
		}

		// Construct the full path for the file/directory
		path := filepath.Join(destDir, header.Name)

		// Check the file type
		switch header.Typeflag {
		case tar.TypeDir: // Directory
			// Create directory if it doesn't exist
			if err := os.MkdirAll(path, 0755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
		case tar.TypeReg: // Regular file
			// Create the directory for the file (may be the first file deeply nested in a dir)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
			// Create the file and write its content
			outFile, err := os.Create(path)
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}

			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return fmt.Errorf("failed to write file: %w", err)
			}
			outFile.Close()
		default:
			fmt.Printf("Unsupported type: %v in %s\n", header.Typeflag, header.Name)
		}
	}

	return nil
}

// Returns false if the filePath does not exist or was modified more than recent seconds ago
// Returns true if the filePath was modified less than or exactly recent seconds ago
func modifiedTimeLessThan(filePath string, recent time.Duration) bool {
	fileInfo, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		// File does not exist
		return false
	}

	// Check the last modified time
	lastModTime := fileInfo.ModTime()
	return time.Since(lastModTime) <= recent*time.Second
}

// Updates the access and modification time of a file
// Will create the file if it does not exist
func touch(filePath string) error {
	_, err := os.OpenFile(filePath, os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	currentTime := time.Now().Local()
	return os.Chtimes(filePath, currentTime, currentTime)
}

func getFileOwner(filename string) (string, error) {
	// Get file information
	fileInfo, err := os.Stat(filename)
	if err != nil {
		return "", err
	}

	// Get file ownership information
	fileStat := fileInfo.Sys().(*syscall.Stat_t)
	uid := strconv.Itoa(int(fileStat.Uid))
	gid := strconv.Itoa(int(fileStat.Gid))

	// Look up username and groupname from UID and GID
	username, err := getUsername(uid)
	if err != nil {
		return "", err
	}

	groupname, err := getGroupname(gid)
	if err != nil {
		return "", err
	}

	// Return username:groupname
	return username + ":" + groupname, nil
}

func getUsername(uid string) (string, error) {
	cmd := exec.Command("getent", "passwd", uid)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(string(output), ":", 4)
	return parts[0], nil
}

func getGroupname(gid string) (string, error) {
	cmd := exec.Command("getent", "group", gid)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(string(output), ":", 4)
	return parts[0], nil
}

func changeFileOwner(filename, owner string) error {
	// Execute chown command
	cmd := exec.Command("chown", owner, filename)
	err := cmd.Run()
	return err
}

func copyFileContents(srcFileName, dstFileName string) error {
	// Open the source file for reading
	srcFile, err := os.Open(srcFileName)
	if err != nil {
		return fmt.Errorf("error opening source file: %w", err)
	}
	defer srcFile.Close()

	// Read the contents of the source file
	contents, err := io.ReadAll(srcFile)
	if err != nil {
		return fmt.Errorf("error reading from source file: %w", err)
	}

	// Open the destination file for writing, create it if it does not exist, or append to it if it does
	dstFile, err := os.OpenFile(dstFileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("error opening destination file: %w", err)
	}
	defer dstFile.Close()

	// Write the contents to the destination file
	if _, err := dstFile.Write(contents); err != nil {
		return fmt.Errorf("error writing to destination file: %w", err)
	}

	return nil
}
