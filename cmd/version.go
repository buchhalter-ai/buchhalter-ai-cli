package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Output the version info",
	Long:  `See what version of buchhalter command line tool you're using.'`,
	Run: func(cmd *cobra.Command, args []string) {
		developmentMode := viper.GetBool("dev")
		versionString := fmt.Sprintf("buchhalter v%s", cliVersion)
		if developmentMode {
			versionString = fmt.Sprintf("buchhalter v%s\nBuild time: %s\nCommit: %s", cliVersion, cliBuildTime, cliCommitHash)
		}

		fmt.Println(versionString)
	},
}
