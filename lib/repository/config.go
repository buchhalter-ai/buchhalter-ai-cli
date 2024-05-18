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

	apiTokenFile := filepath.Join(b.configDirectory, ".buchhalter-api-token")
	b.logger.Info("Writing API token to file", "file", apiTokenFile)
	err = os.WriteFile(apiTokenFile, fileContent, 0644)
	return err
}
