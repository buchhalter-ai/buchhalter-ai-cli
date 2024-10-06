package cmd

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// vaultRemoveCmd represents the `vault remove` command
var vaultRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a 1Password vault from the buchhalter-cli configuration",
	Long: `To use a 1Password vault inside buchhalter, you need to configure this.
This command provides you the tooling to remove a configured vault from buchhalter configuration.`,
	Run: RunVaultRemoveCommand,
}

func init() {
	vaultCmd.AddCommand(vaultRemoveCmd)
}

func RunVaultRemoveCommand(cmd *cobra.Command, args []string) {
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

	// Init UI
	credentialProviderVaults := []vaultConfiguration{}
	if err := viper.UnmarshalKey("credential_provider_vaults", &credentialProviderVaults); err != nil {
		exitMessage := fmt.Sprintf("Error reading configuration field `credential_provider_vaults`: %s", err)
		exitWithLogo(exitMessage)
	}

	viewModel := ViewModelVaultRemove{
		// UI
		actionsCompleted: []string{},

		// Vaults
		vaults: credentialProviderVaults,

		// Vault selection
		showSelection: true,
	}

	// Run the program
	p := tea.NewProgram(&viewModel)
	if _, err := p.Run(); err != nil {
		logger.Error("Error running program", "error", err)
		exitMessage := fmt.Sprintf("Error running program: %s", err)
		exitWithLogo(exitMessage)
	}
}

type ViewModelVaultRemove struct {
	// UI
	actionsCompleted []string
	actionError      string

	// Vaults
	vaults []vaultConfiguration

	// Vault selection
	showSelection   bool
	selectionCursor int
}

func removeVaultFromListByVaultID(vaults []vaultConfiguration, vaultID string) []vaultConfiguration {
	var newVaults []vaultConfiguration
	for _, vault := range vaults {
		if vault.ID != vaultID {
			newVaults = append(newVaults, vault)
		}
	}
	return newVaults
}

func (m ViewModelVaultRemove) Init() tea.Cmd {
	return nil
}

func (m ViewModelVaultRemove) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit

		case "enter":
			// We only allow enter if the vault selection is shown
			if !m.showSelection {
				return m, nil
			}

			selectedVaultName := m.vaults[m.selectionCursor].Name

			// Deactivate selection
			m.showSelection = false
			m.actionsCompleted = append(m.actionsCompleted, fmt.Sprintf("Selected vault `%s` remove from buchhalter-cli configuration", selectedVaultName))

			return m, func() tea.Msg {
				vaultID := m.vaults[m.selectionCursor].ID
				vaultName := m.vaults[m.selectionCursor].Name

				vaultsToWriteList := removeVaultFromListByVaultID(m.vaults, vaultID)
				viper.Set("credential_provider_vaults", vaultsToWriteList)
				configFile := viper.GetString("buchhalter_config_file")
				err := viper.WriteConfigAs(configFile)
				if err != nil {
					return writeConfigFileMsg{
						vaultName: vaultName,
						err:       err,
					}
				}
				return writeConfigFileMsg{vaultName: vaultName}
			}

		case "down", "j":
			// We only allow enter if the vault selection is shown
			if !m.showSelection {
				return m, nil
			}

			m.selectionCursor++
			if m.selectionCursor >= len(m.vaults) {
				m.selectionCursor = 0
			}

		case "up", "k":
			// We only allow enter if the vault selection is shown
			if !m.showSelection {
				return m, nil
			}

			m.selectionCursor--
			if m.selectionCursor < 0 {
				m.selectionCursor = len(m.vaults) - 1
			}
		}

	case writeConfigFileMsg:
		if msg.err != nil {
			m.actionError = fmt.Sprintf("Error writing config file: %s", msg.err)
			return m, tea.Quit
		}

		m.actionsCompleted = append(m.actionsCompleted, fmt.Sprintf("Removed vault `%s` from buchhalter-cli configuration", msg.vaultName))
		return m, tea.Quit
	}

	// If we don't have any vaults, we quit
	// Why sleeping at all? Because we want to output the "No vaults for buchhalter configured yet." message
	if len(m.vaults) == 0 {
		return m, func() tea.Msg {
			time.Sleep(100 * time.Millisecond)
			return tea.QuitMsg{}
		}
	}

	return m, nil
}

func (m ViewModelVaultRemove) View() string {
	s := strings.Builder{}
	s.WriteString(headerStyle(LogoText) + "\n\n")

	for _, actionCompleted := range m.actionsCompleted {
		s.WriteString(checkMark.Render() + " " + textStyleBold(actionCompleted) + "\n")
	}

	if len(m.actionError) > 0 {
		s.WriteString(errorMark.Render() + " " + textStyleBold(capitalizeFirstLetter(m.actionError)) + "\n")
	}

	if m.showSelection && len(m.vaults) > 0 {
		s.WriteString("The following vaults have been found in the buchhalter.ai configuration.\n")
		s.WriteString("Select the one you want to remove and press ENTER:\n\n")

		for i := 0; i < len(m.vaults); i++ {
			currentConfigValue := ""
			if m.vaults[i].Selected {
				currentConfigValue = textStyleBold(" (currently set as default)")
			}
			if m.selectionCursor == i {
				s.WriteString("(•) ")
			} else {
				s.WriteString("( ) ")
			}
			s.WriteString(m.vaults[i].Name)
			s.WriteString(currentConfigValue)
			s.WriteString("\n")
		}
	}

	if len(m.vaults) == 0 {
		s.WriteString(textStyleBold("No vaults for buchhalter configured yet.\nUse `buchhalter vault add` to add a new vault for buchhalter.\n"))
	} else {
		s.WriteString("\n(press q to quit)\n")
	}

	return s.String()
}
