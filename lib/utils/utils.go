package utils

import (
	"archive/zip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	randomStringCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// ViewProgressUpdateMsg updates the progress bar in the bubbletea application.
// "Percent" represents the percentage of the progress bar.
type ViewProgressUpdateMsg struct {
	Percent float64
}

type ViewStatusUpdateMsg struct {
	Message    string
	Details    string
	Err        error
	Completed  bool
	ShouldQuit bool
}

// RecipeResult represents the result of a single recipe execution.
type RecipeResult struct {
	Status              string
	StatusText          string
	StatusTextFormatted string
	LastStepId          string
	LastStepDescription string
	LastErrorMessage    string
	NewFilesCount       int
}

// StepResult represents the result of a single step execution.
type StepResult struct {
	Status  string
	Message string
	Break   bool
}

func InitSupplierDirectories(buchhalterDirectory, supplier string) (string, string, error) {
	downloadsDirectory := filepath.Join(buchhalterDirectory, "_tmp", supplier)
	documentsDirectory := filepath.Join(buchhalterDirectory, supplier)
	err := CreateDirectoryIfNotExists(downloadsDirectory)
	if err != nil {
		return "", "", err
	}
	err = CreateDirectoryIfNotExists(documentsDirectory)
	if err != nil {
		return downloadsDirectory, "", err
	}
	return downloadsDirectory, documentsDirectory, nil
}

func CreateDirectoryIfNotExists(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		err := os.MkdirAll(path, os.ModePerm)
		if err != nil {
			return err
		}
	}

	return nil
}

func TruncateDirectory(path string) error {
	return os.RemoveAll(path)
}

func FindFiles(root, ext string) ([]string, error) {
	var a []string
	err := filepath.WalkDir(root, func(s string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if filepath.Ext(d.Name()) == ext {
			a = append(a, s)
		}
		return nil
	})

	if err != nil {
		return a, err
	}

	return a, nil
}

func CopyFile(src, dst string) (int64, error) {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return 0, err
	}

	if !sourceFileStat.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer destination.Close()

	nBytes, err := io.Copy(destination, source)
	return nBytes, err
}

func UnzipFile(source, dest string) error {
	read, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer read.Close()

	for _, file := range read.File {
		if file.Mode().IsDir() {
			continue
		}
		open, err := file.Open()
		if err != nil {
			return err
		}
		// Sanitize the filename to prevent path traversal
		name := filepath.Join(dest, filepath.Base(file.Name))
		err = CreateDirectoryIfNotExists(path.Dir(name))
		if err != nil {
			return err
		}

		create, err := os.Create(name)
		if err != nil {
			return err
		}
		defer create.Close()

		_, err = create.ReadFrom(open)
		if err != nil {
			return err
		}
	}

	return nil
}

func RandomString(length int) string {
	if length == 0 {
		return ""
	}

	maxRandInt := big.NewInt(int64(len(randomStringCharset)))
	var result []byte
	for i := 0; i < length; i++ {
		index, _ := rand.Int(rand.Reader, maxRandInt)
		result = append(result, randomStringCharset[index.Int64()])
	}

	return encode(result)[:length]
}

func Oauth2Pkce(length int) (string, string, error) {
	verifier := RandomString(length)
	hasher := sha256.New()
	_, err := hasher.Write([]byte(verifier))
	challenge := encode(hasher.Sum(nil))

	return verifier, challenge, err
}

func encode(msg []byte) string {
	encoded := base64.StdEncoding.EncodeToString(msg)
	encoded = strings.Replace(encoded, "+", "-", -1)
	encoded = strings.Replace(encoded, "/", "_", -1)
	encoded = strings.Replace(encoded, "=", "", -1)
	return encoded
}

func WriteStringToFile(filePath, content string) error {
	return os.WriteFile(filePath, []byte(content), 0644)
}
