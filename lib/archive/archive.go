package archive

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var fileHashes []string

func BuildArchiveIndex(archiveDirectory string) error {
	// Iterate over all files in the archive directory and build an index with all existing file hashes.
	// This index will be used to detect if a downloaded invoice/file is new or already exists.
	err := filepath.Walk(archiveDirectory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name()[0:1] != "_" && info.Name()[0:1] != "." {
			hash, err := computeHash(path)
			if err != nil {
				return fmt.Errorf("error computing hash for %s: %w", path, err)
			}
			fileHashes = append(fileHashes, hash)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error walking the directory: %w", err)
	}

	return nil
}

func computeHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	buf := make([]byte, 8192) // 8KB buffer

	for {
		n, err := file.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return "", err
			}
		}
		if n == 0 {
			break
		}

		_, err = hasher.Write(buf[:n])
		if err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func FileExists(filePath string) bool {
	hash, _ := computeHash(filePath)
	return fileHashExists(hash)
}

func fileHashExists(hash string) bool {
	for _, fh := range fileHashes {
		if fh == hash && hash != "" {
			return true
		}
	}
	return false
}
