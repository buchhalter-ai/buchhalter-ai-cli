package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// vaultCmd represents the vault command
var vaultCmd = &cobra.Command{
	Use:   "vault",
	Short: "Sub-Commands to manage the password vault",
	Long:  `Sub-Commands to manage the password vault.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Nothing to see here. Try `buchhalter help vault`.")
	},
}

func init() {
	rootCmd.AddCommand(vaultCmd)
}
