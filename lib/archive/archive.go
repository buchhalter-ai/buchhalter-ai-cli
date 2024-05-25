package archive

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type DocumentArchive struct {
	logger *slog.Logger

	storageDirectory string
	fileIndex        map[string]File
}

type File struct {
	Path     string
	Supplier string
}

func NewDocumentArchive(logger *slog.Logger, archiveDirectory string) *DocumentArchive {
	return &DocumentArchive{
		logger:           logger,
		storageDirectory: archiveDirectory,

		fileIndex: map[string]File{},
	}
}

func (a *DocumentArchive) BuildArchiveIndex() error {
	// Iterate over all files in the archive directory and build an index with all existing file hashes.
	// This index will be used to detect if a downloaded invoice/file is new or already exists.
	err := filepath.Walk(a.storageDirectory, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Exclude `_local` directory
		localDir := fmt.Sprintf("%s%s_local", a.storageDirectory, string(os.PathSeparator))
		if strings.Contains(filePath, localDir) {
			return nil
		}

		// Exclude directories, hidden files and log files
		if !info.IsDir() && info.Name()[0:1] != "_" && info.Name()[0:1] != "." && path.Ext(info.Name()) != ".log" {
			hash, err := computeHash(filePath)
			if err != nil {
				return fmt.Errorf("error computing hash for %s: %w", filePath, err)
			}
			a.fileIndex[hash] = File{
				Path:     filePath,
				Supplier: a.determineSupplierFromPath(filePath),
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error walking the directory: %w", err)
	}

	a.logger.Info("Building document archive index ... completed", "files_in_index", len(a.fileIndex))

	return nil
}

func (a *DocumentArchive) FileExists(filePath string) bool {
	hash, _ := computeHash(filePath)
	return a.fileHashExists(hash)
}

func (a *DocumentArchive) AddFile(filePath string) error {
	// Right now, we overwrite the file if it exists already
	// if a.fileHashExists(filePath) {
	// 	return fmt.Errorf("file %s already exists in archive", filePath)
	// }

	hash, err := computeHash(filePath)
	if err != nil {
		return err
	}

	a.fileIndex[hash] = File{
		Path:     filePath,
		Supplier: a.determineSupplierFromPath(filePath),
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

func (a *DocumentArchive) fileHashExists(hash string) bool {
	if len(hash) == 0 {
		return false
	}

	if _, ok := a.fileIndex[hash]; ok {
		return true
	}

	return false
}

func (a *DocumentArchive) GetFileIndex() map[string]File {
	return a.fileIndex
}

func (a *DocumentArchive) determineSupplierFromPath(filePath string) string {
	p := path.Dir(filePath)
	_, file := filepath.Split(p)
	return file
}
