package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"buchhalter/lib/repository"
	"buchhalter/lib/utils"
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

var (
	// cliVersion is the version of the software.
	// Typically a branch or tag name.
	// Is set at compile time via ldflags.
	cliVersion = "main"

	// cliCommitHash reflects the current git sha.
	// Is set at compile time via ldflags.
	cliCommitHash = "none"

	// cliBuildTime is the compile date + time.
	// Is set at compile time via ldflags.
	cliBuildTime = "unknown"
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
var checkMark = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).SetString("✓")
var errorMark = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).SetString("❌")
var thanksMark = lipgloss.NewStyle().SetString("🙏")

type vaultConfiguration struct {
	ID       string `json:"id" mapstructure:"id"`
	Name     string `json:"name" mapstructure:"name"`
	Selected bool   `json:"selected" mapstructure:"selected"`
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "buchhalter",
	Short: "Automatically sync invoices from all your suppliers",
	Long:  longDescription,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute(version, commitHash, buildTime string) {
	cliVersion = version
	cliCommitHash = commitHash
	cliBuildTime = buildTime

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
	buchhalterDir := filepath.Join(homeDir, "buchhalter")
	buchhalterConfigDir := filepath.Join(homeDir, ".buchhalter")
	configFile := filepath.Join(buchhalterConfigDir, ".buchhalter.yaml")

	// Set default values for viper config
	// Documented settings
	viper.SetDefault("credential_provider_cli_command", "")
	viper.SetDefault("credential_provider_item_tag", "buchhalter-ai")
	viper.SetDefault("credential_provider_vaults", []vaultConfiguration{})
	viper.SetDefault("buchhalter_directory", buchhalterDir)
	viper.SetDefault("buchhalter_config_directory", buchhalterConfigDir)
	viper.SetDefault("buchhalter_config_file", configFile)
	viper.SetDefault("buchhalter_max_download_files_per_receipt", 2)
	viper.SetDefault("buchhalter_api_host", "https://app.buchhalter.ai/")
	viper.SetDefault("buchhalter_always_send_metrics", false)
	viper.SetDefault("dev", false)

	// Non documented settings (on purpose)
	// Those settings are either part of a different configuration file or are not meant to be changed by the user
	// E.g. when they are calculated based on other settings
	viper.SetDefault("buchhalter_api_token", "")
	// See below
	// - buchhalter_api_team_slug
	// - buchhalter_documents_directory

	// Check if config file exists or create it
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		err := utils.CreateDirectoryIfNotExists(buchhalterConfigDir)
		if err != nil {
			fmt.Println("Error creating config directory:", err)
			os.Exit(1)
		}

		// Write the full config into the configuration file
		// This was introduced to keep the value `buchhalter_always_send_metrics` persistent in the configuration file
		// This can turn into problems later on, because we don't need all configuration values in the configuration file persistent
		// Right now we don't have such a problem, hence we keep it as is.
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

	// Read local API settings
	dummyLogger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	buchhalterConfig := repository.NewBuchhalterConfig(dummyLogger, buchhalterConfigDir)
	apiConfig, err := buchhalterConfig.GetLocalAPIConfig()
	if err != nil {
		fmt.Println("Error reading api token file:", err)
		os.Exit(1)
	}
	viper.Set("buchhalter_api_token", apiConfig.APIKey)
	teamSlug := "default"
	if len(apiConfig.TeamSlug) > 0 {
		teamSlug = apiConfig.TeamSlug
	}
	viper.Set("buchhalter_api_team_slug", teamSlug)

	// Documents directory
	buchhalterDocumentsDirectory := filepath.Join(buchhalterDir, "documents", teamSlug)
	viper.Set("buchhalter_documents_directory", buchhalterDocumentsDirectory)

	// Create main directory if not exists
	err = utils.CreateDirectoryIfNotExists(buchhalterDir)
	if err != nil {
		fmt.Println("Error creating main directory:", err)
		os.Exit(1)
	}

	// Create documents directory if not exists
	err = utils.CreateDirectoryIfNotExists(buchhalterDocumentsDirectory)
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

func exitWithLogo(message string) {
	s := fmt.Sprintf(
		"%s\n%s\n%s%s\n%s\n\n%s",
		headerStyle(LogoText),
		textStyle("Automatically sync all your incoming invoices from your suppliers. "),
		textStyle("More information at: "),
		textStyleBold("https://buchhalter.ai"),
		textStyleGrayBold(fmt.Sprintf("Using CLI v%s", cliVersion)),
		textStyle(message),
	)
	fmt.Println(s)
	os.Exit(1)
}

func capitalizeFirstLetter(input string) string {
	if len(input) == 0 {
		return ""
	}

	first := strings.ToUpper(string(input[0]))
	rest := input[1:]
	return first + rest
}
