/*
Copyright © 2023 buchhalter.ai <support@buchhalter.ai>
*/
package cmd

import (
	"buchhalter/lib/utils"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	LogoText = `
 _                _     _           _ _            
| |              | |   | |         | | |           
| |__  _   _  ___| |__ | |__   __ _| | |_ ___ _ __ 
| '_ \| | | |/ __| '_ \| '_ \ / _' | | __/ _ \ '__|
| |_) | |_| | (__| | | | | | | (_| | | ||  __/ |
|_.__/ \__._|\___|_| |_|_| |_|\__._|_|\__\___|_|
`
)

const (
	CliVersion = "0.0.1"
)

var (
	longDescription = fmt.Sprintf(
		"%s\n%s\n%s%s\n",
		headerStyle(LogoText),
		textStyle("Automatically sync all your incoming invoices from your suppliers. "),
		textStyle("More information at: "),
		textStyleBold("https://buchhalter.ai"),
	)
)

var textStyle = lipgloss.NewStyle().Render
var textStyleGrayBold = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#666666")).Render
var textStyleBold = lipgloss.NewStyle().Bold(true).Render
var headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#9FC131")).Render

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "buchhalter",
	Short: "Automatically sync invoices from all your suppliers",
	Long:  longDescription,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Disable the `completion` command
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	rootCmd.PersistentFlags().BoolP("log", "l", false, "log debug output")
	rootCmd.PersistentFlags().BoolP("dev", "d", false, "development mode (e.g. without OICDB recipe updates and sending metrics)")
	err := viper.BindPFlag("dev", rootCmd.PersistentFlags().Lookup("dev"))
	if err != nil {
		fmt.Printf("Failed to bind 'dev' flag: %v\n", err)
		os.Exit(1)
	}

	err = viper.BindPFlag("log", rootCmd.PersistentFlags().Lookup("log"))
	if err != nil {
		fmt.Printf("Failed to bind 'log' flag: %v\n", err)
		os.Exit(1)
	}
}

func initConfig() {
	homeDir, _ := os.UserHomeDir()
	buchhalterConfigDir := filepath.Join(homeDir, ".buchhalter")
	configFile := filepath.Join(buchhalterConfigDir, ".buchhalter.yaml")
	buchhalterDir := filepath.Join(homeDir, "buchhalter")

	// Set default values for viper config
	// TODO Verify if all of these settings are documented
	viper.SetDefault("credential_provider_cli_command", "")
	viper.SetDefault("credential_provider_vault", "Base")
	viper.SetDefault("credential_provider_item_tag", "buchhalter-ai")
	viper.SetDefault("buchhalter_directory", buchhalterDir)
	viper.SetDefault("buchhalter_config_directory", buchhalterConfigDir)
	viper.SetDefault("buchhalter_max_download_files_per_receipt", 2)
	viper.SetDefault("buchhalter_api_host", "https://app.buchhalter.ai/")
	viper.SetDefault("buchhalter_always_send_metrics", false)
	viper.SetDefault("dev", false)

	// Check if config file exists or create it
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		err := utils.CreateDirectoryIfNotExists(buchhalterConfigDir)
		if err != nil {
			fmt.Println("Error creating config directory:", err)
			os.Exit(1)
		}

		secret := uuid.New().String()
		viper.Set("buchhalter_secret", secret)
		err = viper.WriteConfigAs(configFile)
		if err != nil {
			fmt.Println("Error creating config file:", err)
			os.Exit(1)
		}
	}
	viper.SetConfigFile(configFile)

	// Initialize viper config
	err := viper.ReadInConfig()
	if err != nil {
		fmt.Println("Error reading config file:", err)
		os.Exit(1)
	}

	// Create main directory if not exists
	err = utils.CreateDirectoryIfNotExists(buchhalterDir)
	if err != nil {
		fmt.Println("Error creating main directory:", err)
		os.Exit(1)
	}
}

func initializeLogger(logSetting, developmentMode bool, buchhalterDir string) (*slog.Logger, error) {
	var logger *slog.Logger

	// Basic level: Info
	// We increase the level to Debug if development mode is enabled
	handlerOptions := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	if developmentMode {
		handlerOptions.Level = slog.LevelDebug
	}

	if logSetting {
		fileName := filepath.Join(buchhalterDir, "buchhalter-cli.log")
		outputWriter, err := os.OpenFile(fileName, os.O_APPEND|os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			return nil, fmt.Errorf("can't open %s for logging: %+v", fileName, err)
		}
		// defer outputWriter.Close()
		logger = slog.New(slog.NewTextHandler(outputWriter, handlerOptions))
	} else {
		logger = slog.New(slog.NewTextHandler(io.Discard, handlerOptions))
	}

	return logger, nil
}
