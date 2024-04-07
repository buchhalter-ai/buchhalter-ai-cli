package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type BuchhalterAPIClient struct {
	logger *slog.Logger

	configDirectory string
	repositoryUrl   string
	metricsUrl      string
	userAgent       string
}

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

func NewBuchhalterAPIClient(logger *slog.Logger, configDirectory, repositoryUrl, metricsUrl, cliVersion string) *BuchhalterAPIClient {
	return &BuchhalterAPIClient{
		logger:          logger,
		configDirectory: configDirectory,
		repositoryUrl:   repositoryUrl,
		metricsUrl:      metricsUrl,
		userAgent:       fmt.Sprintf("buchhalter-cli/%s", cliVersion),
	}
}

func (c *BuchhalterAPIClient) UpdateIfAvailable(currentChecksum string) error {
	updateExists, err := c.updateExists(c.repositoryUrl, currentChecksum)
	if err != nil {
		return fmt.Errorf("you're offline - please connect to the internet for using buchhalter-cli: %w", err)
	}

	if updateExists {
		c.logger.Info("Starting to update the local OICDB repository ...")
		client := &http.Client{
			Timeout: 10 * time.Second,
		}
		ctx := context.Background()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.repositoryUrl, nil)
		if err != nil {
			return err
		}

		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			fileToUpdate := filepath.Join(c.configDirectory, "oicdb.json")
			out, err := os.Create(fileToUpdate)
			if err != nil {
				return fmt.Errorf("couldn't create oicdb.json file: %w", err)
			}
			defer out.Close()

			bytesCopied, err := io.Copy(out, resp.Body)
			if err != nil {
				return fmt.Errorf("error copying response body to file: %w", err)
			}

			c.logger.Info("Starting to update the local OICDB repository ... completed", "database", fileToUpdate, "bytes_written", bytesCopied)
			return nil
		}
		return fmt.Errorf("http request to %s failed with status code: %d", c.repositoryUrl, resp.StatusCode)
	}

	return nil
}

func (c *BuchhalterAPIClient) updateExists(repositoryUrl, currentChecksum string) (bool, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, repositoryUrl, nil)
	if err != nil {
		return false, err
	}

	req.Header.Set("User-Agent", c.userAgent)
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
				c.logger.Info("No new updates for OICDB repository available", "local_checksum", currentChecksum, "remote_checksum", checksum)
				return false, nil
			}

			c.logger.Info("New updates for OICDB repository available", "local_checksum", currentChecksum, "remote_checksum", checksum)
			return true, nil
		}

		return false, fmt.Errorf("update failed with checksum mismatch")
	}

	return false, fmt.Errorf("http request to %s failed with status code: %d", repositoryUrl, resp.StatusCode)
}

func (c *BuchhalterAPIClient) SendMetrics(runData RunData, cliVersion, chromeVersion, vaultVersion, oicdbVersion string) error {
	rdx, err := json.Marshal(runData)
	if err != nil {
		return fmt.Errorf("error marshalling run data: %w", err)
	}

	md := Metric{
		MetricType:    "runMetrics",
		Data:          string(rdx),
		CliVersion:    cliVersion,
		OicdbVersion:  oicdbVersion,
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.metricsUrl, bytes.NewBuffer(mdj))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("User-Agent", c.userAgent)
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

	return fmt.Errorf("http request to %s failed with status code: %d", c.metricsUrl, resp.StatusCode)
}
