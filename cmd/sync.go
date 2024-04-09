package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"buchhalter/lib/archive"
	"buchhalter/lib/browser"
	"buchhalter/lib/client"
	"buchhalter/lib/parser"
	"buchhalter/lib/repository"
	"buchhalter/lib/utils"
	"buchhalter/lib/vault"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	ChromeVersion string
	RunData       repository.RunData
)

type recipeToExecute struct {
	recipe      *parser.Recipe
	vaultItemId string
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Synchronize all invoices from your suppliers",
	Long:  "The sync command uses all buchhalter tagged credentials from your vault and synchronizes all invoices.",
	Run:   RunSyncCommand,
}

func init() {
	rootCmd.AddCommand(syncCmd)
}

func RunSyncCommand(cmd *cobra.Command, cmdArgs []string) {
	provider := ""
	if len(cmdArgs) > 0 {
		provider = cmdArgs[0]
	}

	// Init logging
	buchhalterDirectory := viper.GetString("buchhalter_directory")
	developmentMode := viper.GetBool("dev")
	logSetting, err := cmd.Flags().GetBool("log")
	if err != nil {
		fmt.Printf("Error reading log flag: %s\n", err)
		os.Exit(1)
	}
	logger, err := initializeLogger(logSetting, developmentMode, buchhalterDirectory)
	if err != nil {
		fmt.Printf("Error on initializing logging: %s\n", err)
		os.Exit(1)
	}
	logger.Info("Booting up", "development_mode", developmentMode)
	defer logger.Info("Shutting down")

	// Init document archive
	documentArchive := archive.NewDocumentArchive(logger, buchhalterDirectory)

	// Init vault provider
	vaultConfigBinary := viper.GetString("credential_provider_cli_command")
	vaultConfigBase := viper.GetString("credential_provider_vault")
	vaultConfigTag := viper.GetString("credential_provider_item_tag")
	logger.Info("Initializing credential provider", "provider", "1Password", "cli_command", vaultConfigBinary, "vault", vaultConfigBase, "tag", vaultConfigTag)
	vaultProvider, err := vault.GetProvider(vault.PROVIDER_1PASSWORD, vaultConfigBinary, vaultConfigBase, vaultConfigTag)
	if err != nil {
		logger.Error(vaultProvider.GetHumanReadableErrorMessage(err))
		fmt.Println(vaultProvider.GetHumanReadableErrorMessage(err))
		os.Exit(1)
	}

	buchhalterConfigDirectory := viper.GetString("buchhalter_config_directory")
	recipeParser := parser.NewRecipeParser(logger, buchhalterConfigDirectory, buchhalterDirectory)

	localOICDBChecksum, err := recipeParser.GetChecksumOfLocalOICDB()
	if err != nil {
		logger.Error("Error calculating checksum of local Open Invoice Collector Database", "error", err)
		fmt.Printf("Error calculating checksum of local Open Invoice Collector Database: %s\n", err)
		os.Exit(1)
	}

	repositoryUrl := viper.GetString("buchhalter_repository_url")
	metricsUrl := viper.GetString("buchhalter_metrics_url")
	buchhalterAPIClient := repository.NewBuchhalterAPIClient(logger, buchhalterConfigDirectory, repositoryUrl, metricsUrl, CliVersion)

	viewModel := initialModel(logger, vaultProvider, buchhalterAPIClient, recipeParser)
	p := tea.NewProgram(viewModel)

	// Load vault items/try to connect to vault
	vaultItems, err := vaultProvider.LoadVaultItems()
	if err != nil {
		logger.Error(vaultProvider.GetHumanReadableErrorMessage(err))
		fmt.Println(vaultProvider.GetHumanReadableErrorMessage(err))
		os.Exit(1)
	}

	// Check if vault items are available
	if len(vaultItems) == 0 {
		// TODO Add link with help article
		logger.Error("No credential items loaded from vault", "provider", "1Password", "cli_command", vaultConfigBinary, "vault", vaultConfigBase, "tag", vaultConfigTag)
		fmt.Printf("No credential items found in vault '%s' with tag '%s'. Please check your 1password vault items.\n", vaultConfigBase, vaultConfigTag)
		os.Exit(1)
	}
	logger.Info("Credential items loaded from vault", "num_items", len(vaultItems), "provider", "1Password", "cli_command", vaultConfigBinary, "vault", vaultConfigBase, "tag", vaultConfigTag)

	// Run recipes
	go runRecipes(p, logger, provider, localOICDBChecksum, vaultProvider, documentArchive, recipeParser, buchhalterAPIClient)

	if _, err := p.Run(); err != nil {
		logger.Error("Error running program", "error", err)
		fmt.Printf("Error running program: %s\n", err)
		os.Exit(1)
	}
}

func runRecipes(p *tea.Program, logger *slog.Logger, provider, localOICDBChecksum string, vaultProvider *vault.Provider1Password, documentArchive *archive.DocumentArchive, recipeParser *parser.RecipeParser, buchhalterAPIClient *repository.BuchhalterAPIClient) {
	p.Send(resultStatusUpdate{title: "Build archive index"})

	logger.Info("Building document archive index ...")
	err := documentArchive.BuildArchiveIndex()
	if err != nil {
		// TODO Implement better error handling
		logger.Error("Error building document archive index", "error", err)
		fmt.Println(err)
	}

	developmentMode := viper.GetBool("dev")
	if !developmentMode {
		p.Send(resultStatusUpdate{title: "Checking for repository updates"})

		logger.Info("Checking for OICDB repository updates ...", "local_checksum", localOICDBChecksum)
		err := buchhalterAPIClient.UpdateIfAvailable(localOICDBChecksum)
		if err != nil {
			logger.Error("Error checking for OICDB repository updates", "error", err)
			fmt.Println(err)
			os.Exit(1)
		}
	}

	recipesToExecute := prepareRecipes(logger, provider, vaultProvider, recipeParser)
	// No credentials found for provider/recipes
	if len(recipesToExecute) == 0 {
		// TODO Implement better error handling
		logger.Error("No recipes found for suppliers", "provider", provider)
		fmt.Println("No recipes found for suppliers")
		os.Exit(1)
	}

	var t string
	recipeCount := len(recipesToExecute)
	if recipeCount == 1 {
		t = "Running one recipe..."
		logger.Info("Running one recipe ...", "supplier", recipesToExecute[0].recipe.Provider)
	} else {
		t = "Running recipes for " + fmt.Sprintf("%d", recipeCount) + " suppliers..."
		logger.Info("Running recipes for multiple suppliers...", "num_suppliers", recipeCount)
	}
	p.Send(resultStatusUpdate{title: t})
	p.Send(ResultProgressUpdate{Percent: 0.001})

	buchhalterDirectory := viper.GetString("buchhalter_directory")
	buchhalterConfigDirectory := viper.GetString("buchhalter_config_directory")

	totalStepCount := 0
	stepCountInCurrentRecipe := 0
	baseCountStep := 0
	var recipeResult utils.RecipeResult
	for i := range recipesToExecute {
		totalStepCount += len(recipesToExecute[i].recipe.Steps)
	}
	for i := range recipesToExecute {
		startTime := time.Now()
		stepCountInCurrentRecipe = len(recipesToExecute[i].recipe.Steps)
		p.Send(resultStatusUpdate{title: "Downloading invoices from " + recipesToExecute[i].recipe.Provider + ":", hasError: false})

		// Load username, password, totp from vault
		logger.Info("Requesting credentials from vault", "supplier", recipesToExecute[i].recipe.Provider)
		recipeCredentials, err := vaultProvider.GetCredentialsByItemId(recipesToExecute[i].vaultItemId)
		if err != nil {
			// TODO Implement better error handling
			logger.Error(vaultProvider.GetHumanReadableErrorMessage(err))
			fmt.Println(vaultProvider.GetHumanReadableErrorMessage(err))
			continue
		}

		logger.Info("Downloading invoices ...", "supplier", recipesToExecute[i].recipe.Provider, "supplier_type", recipesToExecute[i].recipe.Type)
		switch recipesToExecute[i].recipe.Type {
		case "browser":
			browserDriver := browser.NewBrowserDriver(logger, recipeCredentials, buchhalterDirectory, documentArchive)
			recipeResult = browserDriver.RunRecipe(p, totalStepCount, stepCountInCurrentRecipe, baseCountStep, recipesToExecute[i].recipe)
			if ChromeVersion == "" {
				ChromeVersion = browserDriver.ChromeVersion
			}
			// TODO Should we quit it here or inside RunRecipe?
			err = browserDriver.Quit()
			if err != nil {
				// TODO Implement better error handling
				fmt.Println(err)
			}
		case "client":
			clientDriver := client.NewClientAuthBrowserDriver(logger, recipeCredentials, buchhalterConfigDirectory, buchhalterDirectory, documentArchive)
			recipeResult = clientDriver.RunRecipe(p, totalStepCount, stepCountInCurrentRecipe, baseCountStep, recipesToExecute[i].recipe)
			if ChromeVersion == "" {
				ChromeVersion = clientDriver.ChromeVersion
			}
			// TODO Should we quit it here or inside RunRecipe?
			err = clientDriver.Quit()
			if err != nil {
				// TODO Implement better error handling
				fmt.Println(err)
			}
		}
		rdx := repository.RunDataProvider{
			Provider:         recipesToExecute[i].recipe.Provider,
			Version:          recipesToExecute[i].recipe.Version,
			Status:           recipeResult.StatusText,
			LastErrorMessage: recipeResult.LastErrorMessage,
			Duration:         time.Since(startTime).Seconds(),
			NewFilesCount:    recipeResult.NewFilesCount,
		}
		RunData = append(RunData, rdx)
		// TODO Check for recipeResult.LastErrorMessage
		p.Send(resultMsg{duration: time.Since(startTime), newFilesCount: recipeResult.NewFilesCount, step: recipeResult.StatusTextFormatted, errorMessage: recipeResult.LastErrorMessage})
		logger.Info("Downloading invoices ... completed", "supplier", recipesToExecute[i].recipe.Provider, "supplier_type", recipesToExecute[i].recipe.Type, "duration", time.Since(startTime), "new_files", recipeResult.NewFilesCount)

		baseCountStep += stepCountInCurrentRecipe
	}

	alwaysSendMetrics := viper.GetBool("buchhalter_always_send_metrics")
	if !developmentMode && alwaysSendMetrics {
		logger.Info("Sending usage metrics to Buchhalter API", "always_send_metrics", alwaysSendMetrics, "development_mode", developmentMode)
		err = buchhalterAPIClient.SendMetrics(RunData, CliVersion, ChromeVersion, vaultProvider.Version, recipeParser.OicdbVersion)
		if err != nil {
			// TODO Implement better error handling
			logger.Error("Error sending usage metrics to Buchhalter API", "error", err)
			fmt.Println(err)
		}

		p.Send(quitMsg{})

	} else if developmentMode {
		p.Send(quitMsg{})

	} else {
		p.Send(resultModeUpdate{
			mode:    "sendMetrics",
			title:   "Let's improve buchhalter-cli together!",
			details: "Allow buchhalter-cli to send anonymized usage data to our api?",
		})
	}
}

func prepareRecipes(logger *slog.Logger, provider string, vaultProvider *vault.Provider1Password, recipeParser *parser.RecipeParser) []recipeToExecute {
	developmentMode := viper.GetBool("dev")
	logger.Info("Loading recipes for suppliers ...", "development_mode", developmentMode)
	loadRecipeResult, err := recipeParser.LoadRecipes(developmentMode)
	if err != nil {
		// TODO Implement better error handling
		logger.Error("Error loading recipes for suppliers", "error", err)
		fmt.Println(loadRecipeResult)
		fmt.Println(err)
	}

	// Run single provider recipe
	var r []recipeToExecute
	stepCount := 0
	vaultItems := vaultProvider.VaultItems
	if provider != "" {
		logger.Info("Search for credentials for suppliers recipe ...", "supplier", provider)
		for i := range vaultItems {
			// Check if a recipe exists for the item
			recipe := recipeParser.GetRecipeForItem(vaultItems[i], vaultProvider.UrlsByItemId)
			if recipe != nil && provider == recipe.Provider {
				r = append(r, recipeToExecute{recipe, vaultItems[i].ID})
				logger.Info("Search for credentials for suppliers recipe ... found", "supplier", provider, "credentials_id", vaultItems[i].ID)
			}
		}

	} else {
		logger.Info("Search for matching pairs of recipes for supplier recipes and credentials ...")

		// Run all recipes
		for i := range vaultItems {
			// Check if a recipe exists for the item
			recipe := recipeParser.GetRecipeForItem(vaultItems[i], vaultProvider.UrlsByItemId)
			if recipe != nil {
				stepCount = stepCount + len(recipe.Steps)
				r = append(r, recipeToExecute{recipe, vaultItems[i].ID})
				logger.Info("Search for matching pairs of recipes for supplier recipes and credentials ... found", "supplier", recipe.Provider, "credentials_id", vaultItems[i].ID)
			}
		}
	}

	return r
}

func sendMetrics(buchhalterAPIClient *repository.BuchhalterAPIClient, a bool, vaultVersion, oicdbVersion string) {
	// TODO Add logging for sendMetrics

	err := buchhalterAPIClient.SendMetrics(RunData, CliVersion, ChromeVersion, vaultVersion, oicdbVersion)
	if err != nil {
		// TODO Implement better error handling
		fmt.Println(err)
	}
	if a {
		viper.Set("buchhalter_always_send_metrics", true)
		err = viper.WriteConfig()
		if err != nil {
			// TODO Implement better error handling
			fmt.Println(err)
		}
	}
}

/**
 * Bubbletea UI
 */
const (
	padding  = 2
	maxWidth = 80
)

var (
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#D6D58E")).Margin(1, 0)
	dotStyle      = helpStyle.Copy().UnsetMargins()
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#EA4335"))
	durationStyle = dotStyle.Copy()
	spinnerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#D6D58E"))
	appStyle      = lipgloss.NewStyle().Margin(1, 2, 0, 2)
	choices       = []string{"Yes", "No", "Always yes (don't ask again)"}
)

// viewModel is the bubbletea application main viewModel (view)
type viewModel struct {
	mode          string
	currentAction string
	details       string
	showProgress  bool
	progress      progress.Model
	spinner       spinner.Model
	results       []resultMsg
	quitting      bool
	hasError      bool
	cursor        int
	choice        string

	vaultProvider       *vault.Provider1Password
	buchhalterAPIClient *repository.BuchhalterAPIClient
	recipeParser        *parser.RecipeParser
	logger              *slog.Logger
}

type quitMsg struct{}

type resultMsg struct {
	duration      time.Duration
	step          string
	errorMessage  string
	newFilesCount int
}

func (r resultMsg) String() string {
	s := len(r.step)
	if r.duration == 0 {
		if r.step != "" {
			r.step = r.step + " " + strings.Repeat(".", maxWidth-1-s)
			return r.step
		}
		return dotStyle.Render(strings.Repeat(".", maxWidth))
	}
	d := r.duration.Round(time.Second).String()
	fill := strings.Repeat(".", maxWidth-1-s-(len(d)-8))
	return fmt.Sprintf("%s %s%s", r.step, fill, durationStyle.Render(d))
}

type resultModeUpdate struct {
	mode    string
	title   string
	details string
}

type resultStatusUpdate struct {
	title    string
	hasError bool
}

type ResultProgressUpdate struct {
	Percent float64
}

type tickMsg time.Time

// initialModel returns the model for the bubbletea application.
func initialModel(logger *slog.Logger, vaultProvider *vault.Provider1Password, buchhalterAPIClient *repository.BuchhalterAPIClient, recipeParser *parser.RecipeParser) viewModel {
	const numLastResults = 5

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	m := viewModel{
		mode:          "sync",
		currentAction: "Initializing...",
		details:       "Loading...",
		showProgress:  true,
		progress:      progress.New(progress.WithGradient("#9FC131", "#DBF227")),
		spinner:       s,
		results:       make([]resultMsg, numLastResults),
		hasError:      false,

		vaultProvider:       vaultProvider,
		buchhalterAPIClient: buchhalterAPIClient,
		recipeParser:        recipeParser,
		logger:              logger,
	}

	return m
}

// Init initializes the bubbletea application.
// Returns an initial command for the application to run.
func (m viewModel) Init() tea.Cmd {
	return tea.Sequence(
		m.spinner.Tick,
	)
}

// Update updates the bubbletea application model.
// Handles incoming events and updates the model accordingly.
func (m viewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.logger.Info("Initiating shutdown sequence", "key_hit", msg.String())

			mn := quit(m)
			return mn, tea.Quit

		case "enter":
			// Send the choice on the channel and exit.
			m.choice = choices[m.cursor]
			m.mode = "sync"
			switch m.choice {
			case "Yes":
				sendMetrics(m.buchhalterAPIClient, false, m.vaultProvider.Version, m.recipeParser.OicdbVersion)
				mn := quit(m)
				return mn, tea.Quit

			case "No":
				mn := quit(m)
				return mn, tea.Quit

			case "Always yes (don't ask again)":
				sendMetrics(m.buchhalterAPIClient, true, m.vaultProvider.Version, m.recipeParser.OicdbVersion)
				mn := quit(m)
				return mn, tea.Quit
			}

		case "down", "j":
			m.cursor++
			if m.cursor >= len(choices) {
				m.cursor = 0
			}

		case "up", "k":
			m.cursor--
			if m.cursor < 0 {
				m.cursor = len(choices) - 1
			}
		}

		return m, nil

	case quitMsg:
		m.logger.Info("Initiating shutdown sequence")

		mn := quit(m)
		return mn, tea.Quit

	case tea.WindowSizeMsg:
		m.progress.Width = msg.Width - padding*2 - 4
		if m.progress.Width > maxWidth {
			m.progress.Width = maxWidth
		}
		return m, nil

	case resultMsg:
		m.results = append(m.results[1:], msg)
		if msg.errorMessage != "" {
			m.hasError = true
			m.details = msg.errorMessage
			mn := quit(m)
			return mn, tea.Quit
		}
		return m, nil

	case resultStatusUpdate:
		m.currentAction = msg.title
		if msg.hasError {
			m.hasError = true
		}
		return m, nil

	case resultModeUpdate:
		m.currentAction = msg.title
		m.details = msg.details
		m.mode = msg.mode
		m.showProgress = false
		return m, nil

	case ResultProgressUpdate:
		cmd := m.progress.SetPercent(msg.Percent)
		return m, cmd

	case utils.ResultProgressUpdate:
		cmd := m.progress.SetPercent(msg.Percent)
		return m, cmd

	case utils.ResultTitleAndDescriptionUpdate:
		m.currentAction = msg.Title
		m.details = msg.Description
		return m, nil

	case tickMsg:
		if m.progress.Percent() == 1.0 {
			m.showProgress = false
			return m, nil
		}
		cmd := m.progress.IncrPercent(0.25)
		return m, tea.Batch(tickCmd(), cmd)

	// FrameMsg is sent when the progress bar wants to animate itself
	case progress.FrameMsg:
		progressModel, cmd := m.progress.Update(msg)
		m.progress = progressModel.(progress.Model)
		return m, cmd

	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
}

// View renders the bubbletea application view.
// Renders the UI based on the data in the model.
func (m viewModel) View() string {
	var s string
	s = fmt.Sprintf(
		"%s\n%s\n%s%s\n%s\n",
		headerStyle(LogoText),
		textStyle("Automatically sync all your incoming invoices from your suppliers. "),
		textStyle("More information at: "),
		textStyleBold("https://buchhalter.ai"),
		textStyleGrayBold(fmt.Sprintf("Using OICDB %s and CLI %s", m.recipeParser.OicdbVersion, CliVersion)),
	) + "\n"

	if !m.hasError {
		s += m.spinner.View() + m.currentAction
		s += helpStyle.Render("  " + m.details)
	} else {
		s += errorStyle.Render("ERROR: " + m.currentAction)
		s += helpStyle.Render("  " + m.details)
	}

	s += "\n"

	if m.showProgress {
		s += m.progress.View() + "\n\n"
	}

	if !m.hasError && m.mode == "sync" {
		for _, res := range m.results {
			s += res.String() + "\n"
		}
	}

	if m.mode == "sendMetrics" && !m.quitting {
		for i := 0; i < len(choices); i++ {
			if m.cursor == i {
				s += "(•) "
			} else {
				s += "( ) "
			}
			s += choices[i]
			s += "\n"
		}
	}

	if !m.quitting {
		s += helpStyle.Render("Press q to exit")
	}

	if m.quitting {
		s += "\n"
	}

	return appStyle.Render(s)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second*1, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func quit(m viewModel) viewModel {
	if m.hasError {
		m.currentAction = "ERROR while running recipes!"
		m.quitting = true
		m.showProgress = false

	} else {
		m.currentAction = "Thanks for using buchhalter.ai!"
		m.quitting = true
		m.showProgress = false
		m.details = "HAVE A NICE DAY! :)"
	}

	// TODO Double check where we need to quit running browser sessions
	// TODO Wait group for browser and client
	/*
		go func() {
			err := browser.Quit()
			if err != nil {
				// TODO implement better error handling
				fmt.Println(err)
			}
		}()
	*/
	/*
		go func() {
			err := client.Quit()
			if err != nil {
				// TODO implement better error handling
				fmt.Println(err)
			}
		}()
	*/

	return m
}
