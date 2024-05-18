package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"buchhalter/lib/repository"
)

var disconnectCmd = &cobra.Command{
	Use:   "disconnect",
	Short: "Disconnects you from the Buchhalter Platform",
	Long:  "The disconnect command logs your computer out from the Buchhalter Platform.",
	Run:   RunDisconnectCommand,
}

func init() {
	rootCmd.AddCommand(disconnectCmd)
}

func RunDisconnectCommand(cmd *cobra.Command, cmdArgs []string) {
	// Init logging
	buchhalterDirectory := viper.GetString("buchhalter_directory")
	developmentMode := viper.GetBool("dev")
	logSetting, err := cmd.Flags().GetBool("log")
	if err != nil {
		fmt.Printf("Error reading log flag: %s\n", err)
		os.Exit(1)
	}
	logger, err := initializeLogger(logSetting, developmentMode, buchhalterDirectory)
	if err != nil {
		fmt.Printf("Error on initializing logging: %s\n", err)
		os.Exit(1)
	}
	logger.Info("Booting up", "development_mode", developmentMode)
	defer logger.Info("Shutting down")

	// Print welcome message
	s := fmt.Sprintf(
		"%s\n%s\n%s%s\n%s\n",
		headerStyle(LogoText),
		textStyle("Automatically sync all your incoming invoices from your suppliers. "),
		textStyle("More information at: "),
		textStyleBold("https://buchhalter.ai"),
		textStyleGrayBold(fmt.Sprintf("Using CLI %s", CliVersion)),
	)
	fmt.Println(s)
	fmt.Println(textStyle("Disconnecting from the Buchhalter Platform ..."))

	// Delete file
	homeDir, _ := os.UserHomeDir()
	buchhalterConfigDir := filepath.Join(homeDir, ".buchhalter")
	buchhalterConfig := repository.NewBuchhalterConfig(logger, buchhalterConfigDir)
	err = buchhalterConfig.DeleteLocalAPIConfig()
	if err != nil {
		logger.Error("Error deleting API token file", "error", err)
		fmt.Println(textStyle("Disconnecting from the Buchhalter Platform ... unsuccessful. Please try again."))
		os.Exit(1)
	}

	fmt.Println(textStyle("Disconnecting from the Buchhalter Platform ... successful"))
}
