/*
Copyright © 2023 buchhalter.ai <support@buchhalter.ai>
*/
package cmd

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
	Short: "Sync invoices from Saas Providers",
	Long:  longDescription,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	viper.SetConfigFile(".env")
	viper.ReadInConfig()
	rootCmd.Flags().BoolP("debug", "d", false, "Display debug output")
}
