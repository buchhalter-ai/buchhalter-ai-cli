package cmd

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// vaultSelectCmd represents the `vault select` command
var vaultSelectCmd = &cobra.Command{
	Use:   "select",
	Short: "Select the default 1Password vault that should be used with buchhalter-cli",
	Long: `Your secrets can be organized via vaults inside 1Password. buchhalter-cli is respecting these vaults to only retrieve items from a single vault. To know which vault should be used, the vault need to be selected.
The chosen Vault name will be stores inside a local configuration for later use`,
	Run: RunVaultSelectCommand,
}

func init() {
	vaultCmd.AddCommand(vaultSelectCmd)
}

func RunVaultSelectCommand(cmd *cobra.Command, args []string) {
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

	credentialProviderVaults := []vaultConfiguration{}
	if err := viper.UnmarshalKey("credential_provider_vaults", &credentialProviderVaults); err != nil {
		exitMessage := fmt.Sprintf("Error reading configuration field `credential_provider_vaults`: %s", err)
		exitWithLogo(exitMessage)
	}

	viewModel := ViewModelVaultSelect{
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

func replaceOrAddVaultByIDInVaultConfigList(entries []vaultConfiguration, newVault vaultConfiguration) []vaultConfiguration {
	for i, entry := range entries {
		if entry.ID == newVault.ID {
			entries[i] = newVault
			return entries
		}
	}

	return append(entries, newVault)
}

func resetSelectedVaultInVaultConfigList(entries []vaultConfiguration) []vaultConfiguration {
	for i := range entries {
		entries[i].Selected = false
	}

	return entries
}

type ViewModelVaultSelect struct {
	// UI
	actionsCompleted []string
	actionError      string

	// Vaults
	vaults []vaultConfiguration

	// Vault selection
	showSelection   bool
	selectionCursor int
}

type writeConfigFileMsg struct {
	vaultName string
	err       error
}

func (m ViewModelVaultSelect) Init() tea.Cmd {
	return nil
}

func (m ViewModelVaultSelect) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			m.actionsCompleted = append(m.actionsCompleted, fmt.Sprintf("Selected vault `%s` to mark as new default in buchhalter-cli configuration", selectedVaultName))

			return m, func() tea.Msg {
				vaultID := m.vaults[m.selectionCursor].ID
				vaultName := m.vaults[m.selectionCursor].Name

				vaultToWrite := vaultConfiguration{
					ID:               vaultID,
					Name:             vaultName,
					BuchhalterAPIKey: m.vaults[m.selectionCursor].BuchhalterAPIKey,
					Selected:         true,
				}

				vaultsToWriteList := resetSelectedVaultInVaultConfigList(m.vaults)
				vaultsToWriteList = replaceOrAddVaultByIDInVaultConfigList(vaultsToWriteList, vaultToWrite)

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

		m.actionsCompleted = append(m.actionsCompleted, fmt.Sprintf("Configured 1Password vault '%s' as new default in buchhalter-cli configuration", msg.vaultName))
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

func (m ViewModelVaultSelect) View() string {
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
		s.WriteString("Select the one you want to select as a new default vault and press ENTER:\n\n")

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
