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

// vaultAddCmd represents the `vault add` command
var vaultAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Configure a (new) 1Password vault to buchhalter-cli configuration",
	Long: `To use a 1Password vault inside buchhalter, you need to allow buchhalter to use the vault by configuring this.
During configuration you can add a buchhalter SaaS API key to the vault configuration.

Vaults that have been configured already will be overwritten.`,
	Run: RunVaultAddCommand,
}

func init() {
	vaultCmd.AddCommand(vaultAddCmd)
}

func RunVaultAddCommand(cmd *cobra.Command, args []string) {
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

	// Init vaults from configuration
	credentialProviderVaults := []vaultConfiguration{}
	if err := viper.UnmarshalKey("credential_provider_vaults", &credentialProviderVaults); err != nil {
		exitMessage := fmt.Sprintf("Error reading configuration field `credential_provider_vaults`: %s", err)
		exitWithLogo(exitMessage)
	}
	selectedVault := getSelectedVaultConfiguration(credentialProviderVaults)
	selectedVaultName := ""
	if selectedVault != nil {
		selectedVaultName = selectedVault.ID
	}
	viewModel := ViewModelVaultAdd{
		// UI
		actionsCompleted: []string{},
		actionInProgress: "Initializing connection to Password Vault",
		spinner:          spinnerModel,

		// Vaults
		vaults: credentialProviderVaults,

		// Vault selection
		showSelection:        false,
		defaultVaultInConfig: selectedVaultName,
	}

	// Run the program
	p := tea.NewProgram(&viewModel)
	if _, err := p.Run(); err != nil {
		logger.Error("Error running program", "error", err)
		exitMessage := fmt.Sprintf("Error running program: %s", err)
		exitWithLogo(exitMessage)
	}
}

func getVaultFromVaultListByVaultID(vaults []vaultConfiguration, vaultID string) *vaultConfiguration {
	for _, vault := range vaults {
		if vault.ID == vaultID {
			return &vault
		}
	}

	return nil
}

type ViewModelVaultAdd struct {
	// UI
	actionsCompleted []string
	actionInProgress string
	actionError      string
	spinner          spinner.Model

	// Vaults
	vaults []vaultConfiguration

	// Vault selection
	showSelection        bool
	selectionCursor      int
	defaultVaultInConfig string
	selectionChoices     []vault.Vault
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

func (m ViewModelVaultAdd) Init() tea.Cmd {
	return tea.Batch(vaultSelectInitCmd, m.spinner.Tick)
}

func (m ViewModelVaultAdd) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

			selectedVaultName := m.selectionChoices[m.selectionCursor].Name

			// Deactivate selection
			m.showSelection = false
			m.actionInProgress = ""
			m.actionsCompleted = append(m.actionsCompleted, fmt.Sprintf("Selected the 1Password vault %s to be added to buchhalter-cli configuration", selectedVaultName))

			return m, func() tea.Msg {
				vaultID := m.selectionChoices[m.selectionCursor].ID
				vaultName := m.selectionChoices[m.selectionCursor].Name

				// Prefill existing API key and selected value if vault exists in configuration already
				existingApiKey := ""
				existingSelectedValue := false
				existingVault := getVaultFromVaultListByVaultID(m.vaults, vaultID)
				if existingVault != nil {
					existingApiKey = existingVault.BuchhalterAPIKey
					existingSelectedValue = existingVault.Selected
				}

				// Craft new vault configuration
				vaultToWrite := vaultConfiguration{
					ID:               vaultID,
					Name:             vaultName,
					BuchhalterAPIKey: existingApiKey,
					Selected:         existingSelectedValue,
				}
				vaultsToWriteList := replaceOrAddVaultByIDInVaultConfigList(m.vaults, vaultToWrite)

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

		// No vaults found in 1Password
		if len(msg.vaults) == 0 {
			m.actionError = "No vaults found in 1Password"
			return m, tea.Quit
		}

		m.actionsCompleted = append(m.actionsCompleted, "Initializing connection to Password Vault")

		// Show Vault selection
		m.actionInProgress = "Select the 1Password vault that should be used with buchhalter-cli"
		m.showSelection = true

	case writeConfigFileMsg:
		if msg.err != nil {
			m.actionError = fmt.Sprintf("Error writing config file: %s", msg.err)
			return m, tea.Quit
		}

		m.actionsCompleted = append(m.actionsCompleted, fmt.Sprintf("Added 1Password vault '%s' to buchhalter configuration", msg.vaultName))
		return m, tea.Quit
	}

	return m, nil
}

func (m ViewModelVaultAdd) View() string {
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
			msgsInBrackets := []string{}
			// Check if we have this vault already in the local buchhalter configuration
			if getVaultFromVaultListByVaultID(m.vaults, m.selectionChoices[i].ID) != nil {
				msgsInBrackets = append(msgsInBrackets, "already configured")
			}

			// Is this vault selected as default in configuration?
			if m.selectionChoices[i].ID == m.defaultVaultInConfig {
				msgsInBrackets = append(msgsInBrackets, "currently set as default")
			}

			if m.selectionCursor == i {
				s.WriteString("(•) ")
			} else {
				s.WriteString("( ) ")
			}

			s.WriteString(m.selectionChoices[i].Name)
			if len(msgsInBrackets) > 0 {
				s.WriteString(fmt.Sprintf(" (%s)", textStyleBold(strings.Join(msgsInBrackets, ", "))))
			}

			s.WriteString("\n")
		}
	}

	s.WriteString("\n(press q to quit)\n")

	return s.String()
}
