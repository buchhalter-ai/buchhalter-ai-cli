/*
Copyright © 2023 buchhalter.ai <support@buchhalter.ai>
*/
package cmd

import (
	"errors"
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"io"
	"log"
	"os"
)

const (
	logoText = ` _                _     _           _ _            
| |              | |   | |         | | |           
| |__  _   _  ___| |__ | |__   __ _| | |_ ___ _ __ 
| '_ \| | | |/ __| '_ \| '_ \ / _' | | __/ _ \ '__|
| |_) | |_| | (__| | | | | | | (_| | | ||  __/ |
|_.__/ \__._|\___|_| |_|_| |_|\__._|_|\__\___|_|
`
)

var (
	longDescription = fmt.Sprintf(
		"%s\n%s\n%s%s\n",
		headerStyle(logoText),
		textStyle("Automatically sync all your incoming invoices from your suppliers. "),
		textStyle("More information at: "),
		textStyleBold("https://buchhalter.ai"),
	)
)

var textStyle = lipgloss.NewStyle().Render
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
}

func initConfig() {
	viper.SetConfigFile(".buchhalter.yaml")
	viper.AddConfigPath("$HOME/.buchhalter")
	// Initialize viper config
	err := viper.ReadInConfig()
	if err != nil {
		fmt.Println("Error reading config file:", err)
		return
	}
	// Set default values for viper config
	viper.SetDefault("one_password_cli_command", "/usr/local/bin/op")
	viper.SetDefault("one_password_base", "Base")
	viper.SetDefault("one_password_tag", "buchhalter-ai")

	// Create data directory if not exists
	path := "data"
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		err := os.Mkdir(path, os.ModePerm)
		if err != nil {
			log.Println(err)
		}
	}

	// Log settings
	lx, _ := rootCmd.Flags().GetBool("log")
	if lx == true {
		fileName := "data/buchhalter-cli.log"
		logFile, err := os.OpenFile(fileName, os.O_APPEND|os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			log.Panic(err)
		}
		defer logFile.Close()
		log.SetOutput(logFile)
	} else {
		log.SetOutput(io.Discard)
	}

	// optional: log date-time, filename, and line number
	log.SetFlags(log.Lshortfile | log.LstdFlags)
}
