package repository

import (
	"encoding/json"
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
	b.logger.Info("Deleting API token file", "file", apiTokenFile)
	err := os.Remove(apiTokenFile)
	return err
}
