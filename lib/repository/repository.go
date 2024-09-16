package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	schemaAPIEndpoint     = "/api/cli/schema"
	repositoryAPIEndpoint = "/api/cli/repository"
	metricsAPIEndpoint    = "/api/cli/metrics"
	userAuthAPIEndpoint   = "/api/cli/sync"
)

type BuchhalterAPIClient struct {
	logger            *slog.Logger
	apiHost           *url.URL
	apiToken          string
	authenticatedUser AuthenticatedUser
	configDirectory   string
	userAgent         string
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

type RunData []RunDataSupplier
type RunDataSupplier struct {
	Supplier         string  `json:"supplier,omitempty"`
	Version          string  `json:"version,omitempty"`
	Status           string  `json:"status,omitempty"`
	LastErrorMessage string  `json:"lastErrorMessage,omitempty"`
	Duration         float64 `json:"duration,omitempty"`
	NewFilesCount    int     `json:"newFilesCount,omitempty"`
}

type CliSyncResponse struct {
	Status string            `json:"status"`
	User   AuthenticatedUser `json:"user"`
}

type AuthenticatedUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Teams []Team `json:"teams"`
}

type Team struct {
	ID           string `json:"id"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Subscription string `json:"subscription"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type DocumentCheckResponse struct {
	Status string `json:"status"`
	File   string `json:"file"`
}

type DocumentUploadResponse struct {
	Status     string `json:"status"`
	DocumentID string `json:"document_id"`
}

type ErrorAPIResponse struct {
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

func NewBuchhalterAPIClient(logger *slog.Logger, apiHost, configDirectory, apiToken, cliVersion string) (*BuchhalterAPIClient, error) {
	u, err := url.Parse(apiHost)
	if err != nil {
		return nil, err
	}

	c := &BuchhalterAPIClient{
		logger:          logger,
		configDirectory: configDirectory,
		apiHost:         u,
		userAgent:       fmt.Sprintf("buchhalter-cli/v%s", cliVersion),
		apiToken:        apiToken,
	}

	return c, nil
}

func (c *BuchhalterAPIClient) UpdateOpenInvoiceCollectorDBIfAvailable(currentChecksum string) error {
	err := c.downloadFileFromAPIEndpoint(currentChecksum, repositoryAPIEndpoint, "oicdb.json")
	return err
}

func (c *BuchhalterAPIClient) UpdateOpenInvoiceCollectorDBSchemaIfAvailable(currentChecksum string) error {
	err := c.downloadFileFromAPIEndpoint(currentChecksum, schemaAPIEndpoint, "oicdb.schema.json")
	return err
}

func (c *BuchhalterAPIClient) downloadFileFromAPIEndpoint(currentChecksum, apiEndpoint, localFileName string) error {
	updateExists, err := c.updateExists(currentChecksum, apiEndpoint)
	if err != nil {
		return fmt.Errorf("you're offline - please connect to the internet for using buchhalter-cli: %w", err)
	}

	if updateExists {
		c.logger.Info("Starting to update the local file ...", "file", localFileName, "api_endpoint", apiEndpoint)
		client := &http.Client{
			Timeout: 10 * time.Second,
		}
		ctx := context.Background()
		apiUrl, err := url.JoinPath(c.apiHost.String(), apiEndpoint)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiUrl, nil)
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
			fileToUpdate := filepath.Join(c.configDirectory, localFileName)
			out, err := os.Create(fileToUpdate)
			if err != nil {
				return fmt.Errorf("couldn't create "+localFileName+" file: %w", err)
			}
			defer out.Close()

			bytesCopied, err := io.Copy(out, resp.Body)
			if err != nil {
				return fmt.Errorf("error copying response body to file: %w", err)
			}

			c.logger.Info("Starting to update the local file ... completed", "file", fileToUpdate, "bytes_written", bytesCopied, "api_endpoint", apiEndpoint)
			return nil
		}
		return fmt.Errorf("http request to %s failed with status code: %d", apiUrl, resp.StatusCode)
	}

	return nil
}

func (c *BuchhalterAPIClient) updateExists(currentChecksum, apiEndpoint string) (bool, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	ctx := context.Background()
	apiUrl, err := url.JoinPath(c.apiHost.String(), apiEndpoint)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, apiUrl, nil)
	if err != nil {
		return false, err
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		c.logger.Error("Error sending request", "url", apiUrl, "error", err)
		return false, fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		checksum := resp.Header.Get("x-checksum")
		if checksum != "" {
			if checksum == currentChecksum {
				c.logger.Info("No new updates available", "local_checksum", currentChecksum, "remote_checksum", checksum, "api_endpoint", apiEndpoint)
				return false, nil
			}

			c.logger.Info("New updates for available", "local_checksum", currentChecksum, "remote_checksum", checksum, "api_endpoint", apiEndpoint)
			return true, nil
		}

		return false, fmt.Errorf("update failed with checksum mismatch")
	}

	return false, fmt.Errorf("http request to %s failed with status code: %d", apiUrl, resp.StatusCode)
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
	apiUrl, err := url.JoinPath(c.apiHost.String(), metricsAPIEndpoint)
	if err != nil {
		return err
	}

	c.logger.Info("Sending metrics to Buchhalter SaaS",
		"url", apiUrl,
		"cliVersion", md.CliVersion,
		"oicdbVersion", md.OicdbVersion,
		"vaultVersion", md.VaultVersion,
		"chromeVersion", md.ChromeVersion,
		"os", md.OS,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiUrl, bytes.NewBuffer(mdj))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		c.logger.Error("Error sending request", "url", apiUrl, "error", err)
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	return fmt.Errorf("http request to %s failed with status code: %d", apiUrl, resp.StatusCode)
}

func (c *BuchhalterAPIClient) GetAuthenticatedUser() (*CliSyncResponse, error) {
	// If we don't have an API token, we can't authenticate
	if len(c.apiToken) == 0 {
		return nil, nil
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	ctx := context.Background()
	apiUrl, err := url.JoinPath(c.apiHost.String(), userAuthAPIEndpoint)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiUrl, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", c.apiToken))
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// No auth possible
	if resp.StatusCode == http.StatusForbidden {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http request to %s failed with status code: %d", apiUrl, resp.StatusCode)
	}

	var cliSyncResponse CliSyncResponse
	err = json.NewDecoder(resp.Body).Decode(&cliSyncResponse)
	if err != nil {
		return nil, err
	}

	// Store authenticated user
	c.authenticatedUser = cliSyncResponse.User

	return &cliSyncResponse, nil
}

func (c *BuchhalterAPIClient) DoesDocumentExist(documentHash string) (bool, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	ctx := context.Background()

	// TODO How do we select the correct team?
	// For now we just get the first one
	teamId := c.authenticatedUser.Teams[0].ID

	requestPayload := struct {
		FileChecksum string `json:"file_checksum"`
	}{
		FileChecksum: documentHash,
	}
	jsonRequestPayload, err := json.Marshal(requestPayload)
	if err != nil {
		return false, err
	}

	apiEndpoint := fmt.Sprintf("api/cli/%s/check", teamId)
	apiUrl, err := url.JoinPath(c.apiHost.String(), apiEndpoint)
	if err != nil {
		return false, err
	}
	c.logger.Info("Checking document existence", "url", apiUrl)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiUrl, bytes.NewReader(jsonRequestPayload))
	if err != nil {
		return false, err
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", c.apiToken))
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("http request to %s failed with status code: %d", apiUrl, resp.StatusCode)
	}

	var checkResponse DocumentCheckResponse
	err = json.NewDecoder(resp.Body).Decode(&checkResponse)
	if err != nil {
		return false, err
	}

	if checkResponse.Status == "new" {
		return false, nil
	}

	return true, nil
}

func (c *BuchhalterAPIClient) UploadDocument(filePath, supplier string) error {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	ctx := context.Background()

	// Prepare a form that you will submit to that URL.
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)

	fileName := filepath.Base(filePath)

	// Add file to request
	fileWriter, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		c.logger.Error("Error creating form `file`", "file", fileName, "error", err)
		return err
	}
	fileHandle, err := os.Open(filePath)
	if err != nil {
		c.logger.Error("Error opening file", "file", fileName, "error", err)
		return err
	}
	defer fileHandle.Close()
	_, err = io.Copy(fileWriter, fileHandle)
	if err != nil {
		c.logger.Error("Error copying file", "file", fileName, "error", err)
		return err
	}

	// Add supplier to request
	supplierWriter, err := writer.CreateFormField("supplier")
	if err != nil {
		c.logger.Error("Error creating form `supplier`", "supplier", supplier, "error", err)
		return err
	}
	buf := bytes.NewBufferString(supplier)
	if _, err = io.Copy(supplierWriter, buf); err != nil {
		c.logger.Error("Error copying supplier", "supplier", supplier, "error", err)
		return err
	}

	err = writer.Close()
	if err != nil {
		c.logger.Error("Error closing writer", "error", err)
		return err
	}

	// TODO How do we select the correct team?
	// For now we just get the first one
	teamId := c.authenticatedUser.Teams[0].ID

	apiEndpoint := fmt.Sprintf("api/cli/%s/upload", teamId)
	apiUrl, err := url.JoinPath(c.apiHost.String(), apiEndpoint)
	if err != nil {
		return err
	}
	c.logger.Info("Upload document to API", "url", apiUrl, "file", filePath, "supplier", supplier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiUrl, body)
	if err != nil {
		c.logger.Error("Error creating request", "url", apiUrl, "file", filePath, "supplier", supplier, "error", err)
		return err
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", c.apiToken))
	resp, err := client.Do(req)
	if err != nil {
		c.logger.Error("Error sending request", "url", apiUrl, "file", filePath, "supplier", supplier, "error", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errorResponse ErrorAPIResponse
		err = json.NewDecoder(resp.Body).Decode(&errorResponse)
		if err != nil {
			c.logger.Error("Error decoding error response", "url", apiUrl, "file", filePath, "supplier", supplier, "status_code", resp.StatusCode, "error", err)
			return err
		}

		c.logger.Error("Upload document to API ... failed", "url", apiUrl, "file", filePath, "supplier", supplier, "status_code", resp.StatusCode, "error_code", errorResponse.ErrorCode, "error_message", errorResponse.ErrorMessage)
		return fmt.Errorf("http request to %s failed with status code: %d (code: %s, message %s)", apiUrl, resp.StatusCode, errorResponse.ErrorCode, errorResponse.ErrorMessage)
	}

	var uploadResponse DocumentUploadResponse
	err = json.NewDecoder(resp.Body).Decode(&uploadResponse)
	if err != nil {
		c.logger.Error("Error decoding upload response", "url", apiUrl, "file", filePath, "supplier", supplier, "error", err)
		return err
	}

	c.logger.Info("Upload document to API ... success", "url", apiUrl, "file", filePath, "supplier", supplier, "status_code", resp.StatusCode, "status", uploadResponse.Status, "document_id", uploadResponse.DocumentID)

	return nil
}
