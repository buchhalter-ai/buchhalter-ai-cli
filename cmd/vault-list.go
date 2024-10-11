package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// vaultListCmd represents the `vault list` command
var vaultListCmd = &cobra.Command{
	Use:   "list",
	Short: "Lists the configured 1Password vaults that are used by buchhalter-cli",
	Long: `To use a 1Password vault inside buchhalter, you need to configure this.
This command provides you an overview about the configured 1Password vaults that are used by buchhalter-cli.`,
	Run: RunVaultListCommand,
}

func init() {
	vaultCmd.AddCommand(vaultListCmd)
}

func RunVaultListCommand(cmd *cobra.Command, args []string) {
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

	// Get vaults from configuration
	credentialProviderVaults := []vaultConfiguration{}
	if err := viper.UnmarshalKey("credential_provider_vaults", &credentialProviderVaults); err != nil {
		exitMessage := fmt.Sprintf("Error reading configuration field `credential_provider_vaults`: %s", err)
		exitWithLogo(exitMessage)
	}

	// UI
	fmt.Printf("%s\n", renderConfiguredVaults(credentialProviderVaults))
}

func renderConfiguredVaults(vaults []vaultConfiguration) string {
	s := strings.Builder{}
	s.WriteString(headerStyle(LogoText))

	if len(vaults) > 0 {
		s.WriteString("\nConfigured 1Password vaults for buchhalter.ai:\n\n")
		for _, vault := range vaults {
			// API Key or not?
			emojy := inactiveMark.Render()
			if len(vault.BuchhalterAPIKey) > 0 {
				emojy = checkMark.Render()
			}

			s.WriteString(fmt.Sprintf("%s %s", emojy, vault.Name))
			if vault.Selected {
				s.WriteString(textStyleBold(" (currently configured)"))
			}

			s.WriteString("\n")
		}

		s.WriteString("\n")
		s.WriteString("Legend:\n")
		s.WriteString(fmt.Sprintf("%s Vault has a configured buchhalter SaaS API Key\n", checkMark.Render()))
		s.WriteString(fmt.Sprintf("%s Vault has no configured buchhalter SaaS API Key\n", inactiveMark.Render()))

		s.WriteString("\n")
		s.WriteString("Want to reconfigure your buchhalter vaults?\n")
		s.WriteString("• `buchhalter vault add` to add a new vault to buchhalter configuration\n")
		s.WriteString("• `buchhalter vault select` to select a new vault as default\n")
		s.WriteString("• `buchhalter vault remove` to remove an existing vault from buchhalter configuration\n")
	}

	if len(vaults) == 0 {
		s.WriteString(textStyleBold("\nNo vaults for buchhalter configured yet.\nUse `buchhalter vault add` to add a new vault for buchhalter.\n"))
	}

	return s.String()
}
