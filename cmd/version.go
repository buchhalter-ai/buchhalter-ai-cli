package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Output the version info",
	Long:  `See what version of buchhalter command line tool you're using.'`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("buchhalter v" + CliVersion)
	},
}
