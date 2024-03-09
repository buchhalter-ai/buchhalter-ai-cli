package utils

import (
	"archive/zip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/spf13/viper"
	"io"
	"io/fs"
	"log"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	downloadsDirectory string
	documentsDirectory string
)

type StepResult struct {
	Status  string
	Message string
	Break   bool
}

type ResultProgressUpdate struct {
	Percent float64
}

type ResultTitleAndDescriptionUpdate struct {
	Title       string
	Description string
}

type RecipeResult struct {
	Status              string
	StatusText          string
	StatusTextFormatted string
	LastStepId          string
	LastStepDescription string
	LastErrorMessage    string
	NewFilesCount       int
}

func InitProviderDirectories(provider string) (string, string) {
	wd := viper.GetString("buchhalter_directory")
	downloadsDirectory = filepath.Join(wd, "_tmp", provider)
	documentsDirectory = filepath.Join(wd, provider)
	CreateDirectoryIfNotExists(downloadsDirectory)
	CreateDirectoryIfNotExists(documentsDirectory)
	return downloadsDirectory, documentsDirectory
}

func CreateDirectoryIfNotExists(path string) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		err := os.MkdirAll(path, os.ModePerm)
		if err != nil {
			log.Println(err)
		}
	}
}

func TruncateDirectory(path string) {
	os.RemoveAll(path)
}

func FindFiles(root, ext string) []string {
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
		log.Fatal(err)
	}

	return a
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
		CreateDirectoryIfNotExists(path.Dir(name))
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
	const charset = "abcdefghijklmnopqrstuvwxyz" +
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var result []byte
	for i := 0; i < length; i++ {
		index, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		result = append(result, charset[index.Int64()])
	}

	return encode(result)[:length]
}

func Oauth2Pkce(length int) (verifier, challenge string, err error) {
	verifier = RandomString(length)
	hasher := sha256.New()
	hasher.Write([]byte(verifier))
	challenge = encode(hasher.Sum(nil))
	return verifier, challenge, nil
}

func encode(msg []byte) string {
	encoded := base64.StdEncoding.EncodeToString(msg)
	encoded = strings.Replace(encoded, "+", "-", -1)
	encoded = strings.Replace(encoded, "/", "_", -1)
	encoded = strings.Replace(encoded, "=", "", -1)
	return encoded
}
