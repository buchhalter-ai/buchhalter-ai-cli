package repository

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
)

type BuchhalterConfig struct {
	logger *slog.Logger

	configDirectory string
}

type APIConfig struct {
	APIKey   string `json:"api_key"`
	TeamSlug string `json:"team_slug"`
}

const apiTokenFileName = ".buchhalter-api-token"

func NewBuchhalterConfig(logger *slog.Logger, configDirectory string) *BuchhalterConfig {
	return &BuchhalterConfig{
		logger:          logger,
		configDirectory: configDirectory,
	}
}

func (b *BuchhalterConfig) WriteLocalAPIConfig(apiToken, teamSlug string) error {
	apiConfig := APIConfig{
		APIKey:   apiToken,
		TeamSlug: teamSlug,
	}
	fileContent, err := json.Marshal(apiConfig)
	if err != nil {
		return err
	}

	apiTokenFile := filepath.Join(b.configDirectory, apiTokenFileName)
	b.logger.Info("Writing API token to file", "file", apiTokenFile)
	err = os.WriteFile(apiTokenFile, fileContent, 0644)
	return err
}

func (b *BuchhalterConfig) DeleteLocalAPIConfig() error {
	apiTokenFile := filepath.Join(b.configDirectory, apiTokenFileName)
	if _, err := os.Stat(apiTokenFile); errors.Is(err, os.ErrNotExist) {
		b.logger.Info("API token file does not exist", "file", apiTokenFile)
		return nil
	}

	b.logger.Info("Deleting API token file", "file", apiTokenFile)
	err := os.Remove(apiTokenFile)
	return err
}

func (b *BuchhalterConfig) GetLocalAPIConfig() (*APIConfig, error) {
	c := &APIConfig{}

	apiTokenFile := filepath.Join(b.configDirectory, apiTokenFileName)
	if _, err := os.Stat(apiTokenFile); err == nil {
		fileContent, err := os.ReadFile(apiTokenFile)
		if err != nil {
			return c, err
		}

		err = json.Unmarshal(fileContent, c)
		if err != nil {
			return c, err
		}
	}

	return c, nil
}
