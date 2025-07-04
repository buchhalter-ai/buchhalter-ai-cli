package cmd

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"buchhalter/lib/repository"
	"buchhalter/lib/utils"
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

	// Text input for SaaS API key
	apiKeyTextInput := textinput.New()
	apiKeyTextInput.Placeholder = "Your buchhalter SaaS API key"
	apiKeyTextInput.Focus()
	apiKeyTextInput.CharLimit = 64
	apiKeyTextInput.Width = 64

	viewModel := ViewModelVaultAdd{
		// UI
		actionsCompleted: []utils.UIAction{},
		actionInProgress: "Initializing connection to Password Vault",
		spinner:          spinnerModel,

		// Vaults
		vaults: credentialProviderVaults,

		// Vault selection
		showSelection:        false,
		defaultVaultInConfig: selectedVaultName,

		// SaaS API key Input
		showAPIKeyInput: false,
		apiKeyTextInput: apiKeyTextInput,
		apiKey:          "",

		// Cmd
		logger: logger,
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
	actionsCompleted []utils.UIAction
	actionInProgress string
	spinner          spinner.Model

	// Vaults
	vaults []vaultConfiguration

	// Vault selection
	showSelection        bool
	selectionCursor      int
	defaultVaultInConfig string
	selectionChoices     []vault.Vault

	// SaaS API key Input
	showAPIKeyInput bool
	apiKeyTextInput textinput.Model
	apiKey          string

	// Cmd
	logger *slog.Logger
}

type vaultSelectErrorMsg struct {
	err error
}

type vaultSelectInitSuccessMsg struct {
	vaults []vault.Vault
}

type verifySaaSAPIKeyResultMsg struct {
	success bool
	message string
}

type triggerConfigurationWriteMsg struct {
}

func vaultSelectInitCmd(logger *slog.Logger) tea.Msg {
	// Init vault provider
	vaultConfigBinary := viper.GetString("credential_provider_cli_command")
	vaultProvider, err := vault.GetProvider(vault.PROVIDER_1PASSWORD, vaultConfigBinary, "", "", logger)
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
	// Pass m.logger to vaultSelectInitCmd
	// vaultSelectInitCmd needs to be adapted to return a Cmd, or we wrap it
	// For now, let's create a command that calls it with the logger
	initCmd := func() tea.Msg {
		return vaultSelectInitCmd(m.logger)
	}
	return tea.Batch(initCmd, m.spinner.Tick, textinput.Blink)
}

func (m ViewModelVaultAdd) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit

		case "enter":
			// We only allow enter if the vault selection OR the API key input field is shown
			if !m.showSelection && !m.showAPIKeyInput {
				return m, nil
			}

			// Vault selection
			if m.showSelection {
				selectedVaultName := m.selectionChoices[m.selectionCursor].Name

				// Deactivate selection
				m.showSelection = false
				m.actionInProgress = ""
				m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
					Message: fmt.Sprintf("Selected the 1Password vault %s to be added to buchhalter-cli configuration", selectedVaultName),
					Style:   utils.UIActionStyleSuccess,
				})

				// Show API key input
				m.showAPIKeyInput = true
				m.actionInProgress = "Enter the buchhalter SaaS-API Key that should be used with buchhalter-cli"

				return m, nil
			}

			// SaaS API key input
			if m.showAPIKeyInput {
				apiKey := m.apiKeyTextInput.Value()
				apiKey = strings.TrimSpace(apiKey)

				// Deactivate API key input
				m.showAPIKeyInput = false
				m.actionInProgress = ""

				switch {
				// API keys are 64 characters long
				case len(apiKey) == 64:
					m.apiKey = apiKey

					apiKey = maskString(apiKey)
					m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
						Message: fmt.Sprintf("buchhalter SaaS API Key %s received", apiKey),
						Style:   utils.UIActionStyleSuccess,
					})

					m.actionInProgress = "Validating buchhalter SaaS API Key ..."
					return m, func() tea.Msg {
						// Validating API key
						verifyResult, verifyMessage := verifyBuchhalterAPIKey(m.logger, m.apiKey)
						return verifySaaSAPIKeyResultMsg{
							success: verifyResult,
							message: verifyMessage,
						}
					}
				case len(apiKey) == 0:
					m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
						Message: "Skipping. No buchhalter SaaS API Key added to buchhalter-cli configuration",
						Style:   utils.UIActionStyleSuccess,
					})
				default:
					m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
						Message: fmt.Sprintf("Skipping. buchhalter SaaS API Key has not the correct length (%d chars, expected a 64 char key)", len(apiKey)),
						Style:   utils.UIActionStyleError,
					})
				}
			}

			return m, func() tea.Msg {
				return triggerConfigurationWriteMsg{}
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
		m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
			Message: fmt.Sprintf("%s", msg.err),
			Style:   utils.UIActionStyleError,
		})
		return m, tea.Quit

	case vaultSelectInitSuccessMsg:
		m.selectionChoices = msg.vaults
		m.actionInProgress = ""

		// No vaults found in 1Password
		if len(msg.vaults) == 0 {
			m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
				Message: "No vaults found in 1Password",
				Style:   utils.UIActionStyleError,
			})
			return m, tea.Quit
		}

		m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
			Message: "Initializing connection to Password Vault",
			Style:   utils.UIActionStyleSuccess,
		})

		// Show Vault selection
		m.actionInProgress = "Select the 1Password vault that should be used with buchhalter-cli"
		m.showSelection = true

	case verifySaaSAPIKeyResultMsg:
		m.actionInProgress = ""
		if !msg.success {
			m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
				Message: msg.message,
				Style:   utils.UIActionStyleError,
			})

			// Resetting API Key
			m.apiKey = ""
		} else {
			m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
				Message: msg.message,
				Style:   utils.UIActionStyleSuccess,
			})
		}

		return m, func() tea.Msg {
			return triggerConfigurationWriteMsg{}
		}

	case triggerConfigurationWriteMsg:
		return m, func() tea.Msg {
			vaultID := m.selectionChoices[m.selectionCursor].ID
			vaultName := m.selectionChoices[m.selectionCursor].Name

			// Prefill existing API key and selected value if vault exists in configuration already
			existingSelectedValue := false
			existingVault := getVaultFromVaultListByVaultID(m.vaults, vaultID)
			if existingVault != nil {
				existingSelectedValue = existingVault.Selected
			}

			// If the API key is not 64 characters long, we invalidate it
			configAPIKey := m.apiKey
			if len(configAPIKey) != 64 {
				configAPIKey = ""
			}

			// Craft new vault configuration
			vaultToWrite := vaultConfiguration{
				ID:               vaultID,
				Name:             vaultName,
				BuchhalterAPIKey: configAPIKey,
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

	case writeConfigFileMsg:
		if msg.err != nil {
			m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
				Message: fmt.Sprintf("Error writing config file: %s", msg.err),
				Style:   utils.UIActionStyleError,
			})
			return m, tea.Quit
		}

		m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
			Message: fmt.Sprintf("Added 1Password vault '%s' to buchhalter configuration", msg.vaultName),
			Style:   utils.UIActionStyleSuccess,
		})
		return m, tea.Quit
	}

	if m.showAPIKeyInput {
		var cmd tea.Cmd
		m.apiKeyTextInput, cmd = m.apiKeyTextInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m ViewModelVaultAdd) View() string {
	s := strings.Builder{}
	s.WriteString(headerStyle(LogoText) + "\n\n")

	for _, actionCompleted := range m.actionsCompleted {
		switch actionCompleted.Style {
		case utils.UIActionStyleSuccess:
			s.WriteString(checkMark.Render() + " " + textStyleBold(actionCompleted.Message) + "\n")
		case utils.UIActionStyleError:
			s.WriteString(errorMark.Render() + " " + errorStyle.Render(capitalizeFirstLetter(actionCompleted.Message)) + "\n")
		}
	}

	if len(m.actionInProgress) > 0 {
		s.WriteString(m.spinner.View() + " " + textStyleBold(m.actionInProgress) + "\n")
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

	if m.showAPIKeyInput {
		s.WriteString("\n")
		s.WriteString(m.apiKeyTextInput.View())
		s.WriteString("\n")
	}

	s.WriteString("\n(press q to quit)\n")

	return s.String()
}

func maskString(input string) string {
	start := input[:3]
	end := input[len(input)-3:]

	masked := strings.Repeat("*", len(input)-6)
	return start + masked + end
}

func verifyBuchhalterAPIKey(logger *slog.Logger, apiKey string) (bool, string) {
	buchhalterConfigDirectory := viper.GetString("buchhalter_config_directory")
	apiHost := viper.GetString("buchhalter_api_host")
	buchhalterAPIClient, err := repository.NewBuchhalterAPIClient(logger, apiHost, buchhalterConfigDirectory, apiKey, cliVersion)
	if err != nil {
		return false, "Error initializing API client"
	}

	logger.Info("Making API call")
	cliSyncResponse, err := buchhalterAPIClient.GetAuthenticatedUser()
	if err != nil {
		return false, "API call not successful, response could not be read"
	}

	if cliSyncResponse == nil {
		return false, "API Key is not valid"
	}

	return true, "API Key is valid"
}
