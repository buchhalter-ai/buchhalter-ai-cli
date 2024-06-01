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
		exitMessage := fmt.Sprintf("Error reading log flag: %s", err)
		exitWithLogo(exitMessage)
	}
	logger, err := initializeLogger(logSetting, developmentMode, buchhalterDirectory)
	if err != nil {
		exitMessage := fmt.Sprintf("Error on initializing logging: %s", err)
		exitWithLogo(exitMessage)
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
		textStyleGrayBold(fmt.Sprintf("Using CLI %s", cliVersion)),
	)
	if developmentMode {
		s += textStyleGrayBold(fmt.Sprintf("Build time: %s\nCommit: %s\n", cliBuildTime, cliCommitHash))
	}
	fmt.Println(s)
	fmt.Println(textStyle("Disconnecting from the Buchhalter Platform ..."))

	// Delete file
	homeDir, _ := os.UserHomeDir()
	buchhalterConfigDir := filepath.Join(homeDir, ".buchhalter")
	buchhalterConfig := repository.NewBuchhalterConfig(logger, buchhalterConfigDir)
	err = buchhalterConfig.DeleteLocalAPIConfig()
	if err != nil {
		logger.Error("Error deleting API token file", "error", err)
		exitMessage := fmt.Sprintln(textStyle("Disconnecting from the Buchhalter Platform ... unsuccessful. Please try again."))
		exitWithLogo(exitMessage)
	}

	fmt.Println(textStyle("Disconnecting from the Buchhalter Platform ... successful"))
}
