/*
Copyright © 2023 buchhalter.ai <support@buchhalter.ai>
*/
package cmd

import (
	"buchhalter/lib/utils"
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"io"
	"log"
	"os"
	"path/filepath"
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
		log.Fatal(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().BoolP("log", "l", false, "Log debug output")
	rootCmd.PersistentFlags().BoolP("dev", "d", false, "Development mode without updates and sending metrics")
	err := viper.BindPFlag("dev", rootCmd.PersistentFlags().Lookup("dev"))
	if err != nil {
		log.Fatalf("Failed to bind 'dev' flag: %v", err)
	}

	err = viper.BindPFlag("log", rootCmd.PersistentFlags().Lookup("log"))
	if err != nil {
		log.Fatalf("Failed to bind 'log' flag: %v", err)
	}
}

func initConfig() {
	hd, _ := os.UserHomeDir()
	bbd := filepath.Join(hd, ".buchhalter")
	cf := filepath.Join(bbd, ".buchhalter.yaml")
	bd := filepath.Join(hd, "buchhalter")

	// Set default values for viper config
	viper.SetDefault("one_password_cli_command", "/usr/local/bin/op")
	viper.SetDefault("one_password_base", "Base")
	viper.SetDefault("one_password_tag", "buchhalter-ai")
	viper.SetDefault("buchhalter_directory", bd)
	viper.SetDefault("buchhalter_config_directory", bbd)
	viper.SetDefault("buchhalter_repository_url", "https://app.buchhalter.ai/api/cli/repository")
	viper.SetDefault("buchhalter_metrics_url", "https://app.buchhalter.ai/api/cli/metrics")

	// Check if config file exists or create it
	if _, err := os.Stat(cf); os.IsNotExist(err) {
		utils.CreateDirectoryIfNotExists(bbd)
		s := uuid.New().String()
		viper.Set("buchhalter_secret", s)
		err := viper.WriteConfigAs(cf)
		if err != nil {
			fmt.Println("Error creating config file:", err)
			os.Exit(1)
		}
	}
	viper.SetConfigFile(cf)
	// Initialize viper config
	err := viper.ReadInConfig()
	if err != nil {
		fmt.Println("Error reading config file:", err)
		os.Exit(1)
	}

	// Create main directory if not exists
	utils.CreateDirectoryIfNotExists(bd)

	// Log settings
	lx, _ := rootCmd.Flags().GetBool("log")
	if lx {
		fileName := filepath.Join(bd, "buchhalter-cli.log")
		logFile, err := os.OpenFile(fileName, os.O_APPEND|os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			log.Panic(err)
		}
		defer logFile.Close()
		log.SetOutput(logFile)
		log.SetFlags(log.Lshortfile | log.LstdFlags)
	} else {
		log.SetOutput(io.Discard)
	}
}
