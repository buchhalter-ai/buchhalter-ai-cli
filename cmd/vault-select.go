package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"buchhalter/lib/vault"
)

// selectCmd represents the `vault select` command
var selectCmd = &cobra.Command{
	Use:   "select",
	Short: "Select the 1Password vault that should be used with buchhalter-cli",
	Long: `Your secrets can be organized via vaults inside 1Password. buchhalter-cli is respecting these vaults to only retrieve items from a single vault. To know which vault should be used, the vault need to be selected.longer description that spans multiple lines and likely contains examples. For example:

(•) Private
( ) ACME Corp
( ) Reddit Side Project

The chosen Vault name will be stores inside a local configuration for later use.`,
	Run: RunVaultSelectCommand,
}

func init() {
	vaultCmd.AddCommand(selectCmd)
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

	// Init UI
	spinnerModel := spinner.New()
	spinnerModel.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))

	credentialProviderVaults := []vaultConfiguration{}
	if err := viper.UnmarshalKey("credential_provider_vaults", &credentialProviderVaults); err != nil {
		exitMessage := fmt.Sprintf("Error reading configuration field `credential_provider_vaults`: %s", err)
		exitWithLogo(exitMessage)
	}
	selectedVault := getSelectedVaultConfiguration(credentialProviderVaults)
	selectedVaultName := ""
	if selectedVault != nil {
		selectedVaultName = selectedVault.Name
	}
	viewModel := ViewModelVaultSelect{
		// UI
		actionsCompleted: []string{},
		actionInProgress: "Initializing connection to Password Vault",
		spinner:          spinnerModel,

		// Vaults
		vaults: credentialProviderVaults,

		// Vault selection
		showSelection:   false,
		selectionChoice: selectedVaultName,
	}

	// Run the program
	p := tea.NewProgram(&viewModel)
	if _, err := p.Run(); err != nil {
		logger.Error("Error running program", "error", err)
		exitMessage := fmt.Sprintf("Error running program: %s", err)
		exitWithLogo(exitMessage)
	}
}

func getSelectedVaultConfiguration(entries []vaultConfiguration) *vaultConfiguration {
	for _, entry := range entries {
		if entry.Selected {
			return &entry
		}
	}

	return nil
}

func replaceVaultInVaultConfigListByName(entries []vaultConfiguration, newVault vaultConfiguration) []vaultConfiguration {
	for i, entry := range entries {
		if entry.Name == newVault.Name {
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

func getVaultNameByVaultID(vaults []vault.Vault, vaultID string) string {
	for _, vault := range vaults {
		if vault.ID == vaultID {
			return vault.Name
		}
	}

	return ""
}

type ViewModelVaultSelect struct {
	// UI
	actionsCompleted []string
	actionInProgress string
	actionError      string
	spinner          spinner.Model

	// Vaults
	vaults []vaultConfiguration

	// Vault selection
	showSelection    bool
	selectionCursor  int
	selectionChoice  string
	selectionChoices []vault.Vault
}

type vaultSelectErrorMsg struct {
	err error
}

type vaultSelectInitSuccessMsg struct {
	vaults []vault.Vault
}

func vaultSelectInitCmd() tea.Msg {
	// Init vault provider
	vaultConfigBinary := viper.GetString("credential_provider_cli_command")
	vaultProvider, err := vault.GetProvider(vault.PROVIDER_1PASSWORD, vaultConfigBinary, "", "")
	if err != nil {
		return vaultSelectErrorMsg{err: vaultProvider.GetHumanReadableErrorMessage(err)}
	}

	// Get vaults
	vaults, err := vaultProvider.GetVaults()
	if err != nil {
		return vaultSelectErrorMsg{err: vaultProvider.GetHumanReadableErrorMessage(err)}
	}
	return vaultSelectInitSuccessMsg{
		vaults: vaults,
	}
}

type writeConfigFileMsg struct {
	vaultName string
	err       error
}

func (m ViewModelVaultSelect) Init() tea.Cmd {
	return tea.Batch(vaultSelectInitCmd, m.spinner.Tick)
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

			// Deactivate selection
			m.showSelection = false
			m.actionInProgress = ""
			m.actionsCompleted = append(m.actionsCompleted, "Selected the 1Password vault that should be used with buchhalter-cli")

			// Send the choice on the channel and exit.
			m.selectionChoice = m.selectionChoices[m.selectionCursor].ID

			return m, func() tea.Msg {
				vaultID := m.selectionChoice
				vaultName := getVaultNameByVaultID(m.selectionChoices, m.selectionChoice)

				vaultToWrite := vaultConfiguration{
					ID:               vaultID,
					Name:             vaultName,
					BuchhalterAPIKey: "",
					Selected:         true,
				}

				vaultsToWriteList := resetSelectedVaultInVaultConfigList(m.vaults)
				vaultsToWriteList = replaceVaultInVaultConfigListByName(vaultsToWriteList, vaultToWrite)

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
			if m.selectionCursor >= len(m.selectionChoices) {
				m.selectionCursor = 0
			}

		case "up", "k":
			// We only allow enter if the vault selection is shown
			if !m.showSelection {
				return m, nil
			}

			m.selectionCursor--
			if m.selectionCursor < 0 {
				m.selectionCursor = len(m.selectionChoices) - 1
			}
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case vaultSelectErrorMsg:
		m.actionError = fmt.Sprintf("%s", msg.err)
		return m, tea.Quit

	case vaultSelectInitSuccessMsg:
		m.selectionChoices = msg.vaults
		m.actionInProgress = ""
		m.actionsCompleted = append(m.actionsCompleted, "Initializing connection to Password Vault")

		// Show Vault selection
		m.actionInProgress = "Select the 1Password vault that should be used with buchhalter-cli"
		m.showSelection = true

	case writeConfigFileMsg:
		if msg.err != nil {
			m.actionError = fmt.Sprintf("Error creating config file: %s", msg.err)
			return m, tea.Quit
		}

		m.actionsCompleted = append(m.actionsCompleted, fmt.Sprintf("Configured 1Password vault '%s' as new default", msg.vaultName))
		return m, tea.Quit
	}

	return m, nil
}

func (m ViewModelVaultSelect) View() string {
	s := strings.Builder{}
	s.WriteString(headerStyle(LogoText) + "\n\n")

	for _, actionCompleted := range m.actionsCompleted {
		s.WriteString(checkMark.Render() + " " + textStyleBold(actionCompleted) + "\n")
	}

	if len(m.actionInProgress) > 0 {
		s.WriteString(m.spinner.View() + " " + textStyleBold(m.actionInProgress) + "\n")
	}

	if len(m.actionError) > 0 {
		s.WriteString(errorMark.Render() + " " + textStyleBold(capitalizeFirstLetter(m.actionError)) + "\n")
	}

	if m.showSelection {
		s.WriteString("\n")
		for i := 0; i < len(m.selectionChoices); i++ {
			currentConfigValue := ""
			if m.selectionChoices[i].Name == m.selectionChoice || m.selectionChoices[i].ID == m.selectionChoice {
				currentConfigValue = textStyleBold(" (currently configured)")
			}
			if m.selectionCursor == i {
				s.WriteString("(•) ")
			} else {
				s.WriteString("( ) ")
			}
			s.WriteString(m.selectionChoices[i].Name)
			s.WriteString(currentConfigValue)
			s.WriteString("\n")
		}
	}

	s.WriteString("\n(press q to quit)\n")

	return s.String()
}
