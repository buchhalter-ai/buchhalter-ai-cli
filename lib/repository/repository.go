package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"buchhalter/lib/parser"
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
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, repositoryUrl, nil)
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

	return false, fmt.Errorf("http request to %s failed with status code: %d", repositoryUrl, resp.StatusCode)
}

func UpdateIfAvailable(buchhalterConfigDirectory, repositoryUrl, currentChecksum string) error {
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
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, repositoryUrl, nil)
		if err != nil {
			if err != nil {
				return err
			}
		}

		// TODO Add CLI version to User-Agent, see https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/User-Agent
		req.Header.Set("User-Agent", "buchhalter-cli")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			out, err := os.Create(filepath.Join(buchhalterConfigDirectory, "oicdb.json"))
			if err != nil {
				return fmt.Errorf("couldn't create oicdb.json file: %w", err)
			}
			defer out.Close()

			_, err = io.Copy(out, resp.Body)
			if err != nil {
				return fmt.Errorf("error copying response body to file: %w", err)
			}

			return nil
		}
		return fmt.Errorf("http request to %s failed with status code: %d", repositoryUrl, resp.StatusCode)
	}

	return nil
}

func SendMetrics(metricsUrl string, runData RunData, cliVersion, chromeVersion, vaultVersion string) error {
	rdx, err := json.Marshal(runData)
	if err != nil {
		return fmt.Errorf("error marshalling run data: %w", err)
	}

	md := Metric{
		MetricType:    "runMetrics",
		Data:          string(rdx),
		CliVersion:    cliVersion,
		OicdbVersion:  parser.OicdbVersion,
		VaultVersion:  vaultVersion,
		ChromeVersion: chromeVersion,
		OS:            runtime.GOOS,
	}
	mdj, err := json.Marshal(md)
	if err != nil {
		return fmt.Errorf("error marshalling run data: %w", err)
	}

	client := &http.Client{}
	ctx := context.Background() // Consider using a meaningful context
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, metricsUrl, bytes.NewBuffer(mdj))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	// TODO Add CLI version to User-Agent, see https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/User-Agent
	req.Header.Set("User-Agent", "buchhalter-cli")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	return fmt.Errorf("http request to %s failed with status code: %d", metricsUrl, resp.StatusCode)
}
