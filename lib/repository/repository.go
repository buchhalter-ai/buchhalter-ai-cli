package repository

import (
	"buchhalter/lib/parser"
	"buchhalter/lib/vault"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/viper"
)

type Metric struct {
	MetricType    string `json:"type,omitempty"`
	Data          string `json:"data,omitempty"`
	CliVersion    string `json:"cliVersion,omitempty"`
	OicdbVersion  string `json:"oicdbVersion,omitempty"`
	VaultVersion  string `json:"vaultVersion,omitempty"`
	ChromeVersion string `json:"chromeVersion,omitempty"`
	OS            string `json:"os,omitempty"`
}

type RunData []RunDataProvider
type RunDataProvider struct {
	Provider         string  `json:"provider,omitempty"`
	Version          string  `json:"version,omitempty"`
	Status           string  `json:"status,omitempty"`
	LastErrorMessage string  `json:"lastErrorMessage,omitempty"`
	Duration         float64 `json:"duration,omitempty"`
	NewFilesCount    int     `json:"newFilesCount,omitempty"`
}

func updateExists(repositoryUrl, currentChecksum string) (bool, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, "HEAD", repositoryUrl, nil)
	if err != nil {
		return false, err
	}

	// TODO Add CLI version to User-Agent, see https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/User-Agent
	req.Header.Set("User-Agent", "buchhalter-cli")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("error sending request")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		checksum := resp.Header.Get("x-checksum")
		if checksum != "" {
			if checksum == currentChecksum {
				return false, nil
			}
			return true, nil
		}

		return false, fmt.Errorf("update failed with checksum mismatch")
	}

	return false, fmt.Errorf("http request failed with status code: %d", resp.StatusCode)
}

func UpdateIfAvailable() error {
	repositoryUrl := viper.GetString("buchhalter_repository_url")
	currentChecksum := viper.GetString("buchhalter_repository_checksum")
	updateExists, err := updateExists(repositoryUrl, currentChecksum)
	if err != nil {
		fmt.Printf("You're offline. Please connect to the internet for using buchhalter-cli")
		os.Exit(1)
	}
	if updateExists {
		client := &http.Client{
			Timeout: 10 * time.Second,
		}
		ctx := context.Background()
		req, err := http.NewRequestWithContext(ctx, "GET", repositoryUrl, nil)
		if err != nil {
			if err != nil {
				return err
			}
		}
		req.Header.Set("User-Agent", "buchhalter-cli")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			out, err := os.Create(filepath.Join(viper.GetString("buchhalter_config_directory"), "oicdb.json"))
			if err != nil {
				return fmt.Errorf("couldn't create oicdb.json file: %w", err)
			}
			defer out.Close()
			_, err = io.Copy(out, resp.Body)
			if err != nil {
				return fmt.Errorf("error copying response body to file: %w", err)
			}
		} else {
			return fmt.Errorf("http request failed with status code: %d", resp.StatusCode)
		}
	}
	return nil
}

func SendMetrics(rd RunData, v string, c string) {
	metricsUrl := viper.GetString("buchhalter_metrics_url")
	rdx, err := json.Marshal(rd)
	if err != nil {
		log.Fatal("Error marshalling run data:", err)
	}
	md := Metric{
		MetricType:    "runMetrics",
		Data:          string(rdx),
		CliVersion:    v,
		OicdbVersion:  parser.OicdbVersion,
		VaultVersion:  vault.VaultVersion,
		ChromeVersion: c,
		OS:            runtime.GOOS,
	}
	mdj, err := json.Marshal(md)
	if err != nil {
		log.Fatal("Error marshalling run data:", err)
		return
	}

	client := &http.Client{}
	ctx := context.Background() // Consider using a meaningful context
	req, err := http.NewRequestWithContext(ctx, "POST", metricsUrl, bytes.NewBuffer(mdj))
	if err != nil {
		log.Println("Error creating request:", err)
		return
	}
	req.Header.Set("User-Agent", "buchhalter-cli")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Println("Error sending request:", err)
		fmt.Printf("Response status: %s", resp.Status)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return
	} else {
		fmt.Printf("HTTP request failed with status code: %d", resp.StatusCode)
		return
	}
}
