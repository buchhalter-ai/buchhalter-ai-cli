package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"buchhalter/lib/archive"
	"buchhalter/lib/browser"
	"buchhalter/lib/parser"
	"buchhalter/lib/repository"
	"buchhalter/lib/utils"
	"buchhalter/lib/vault"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pkg/errors"
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
	supplier := ""
	if len(cmdArgs) > 0 {
		supplier = cmdArgs[0]
	}

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

	// Init document archive
	buchhalterDocumentsDirectory := viper.GetString("buchhalter_documents_directory")
	documentArchive := archive.NewDocumentArchive(logger, buchhalterDocumentsDirectory)

	// Init vault provider
	vaultConfigBinary := viper.GetString("credential_provider_cli_command")
	vaultConfigBase := viper.GetString("credential_provider_vault")
	vaultConfigTag := viper.GetString("credential_provider_item_tag")
	logger.Info("Initializing credential provider", "provider", "1Password", "cli_command", vaultConfigBinary, "vault", vaultConfigBase, "tag", vaultConfigTag)
	vaultProvider, err := vault.GetProvider(vault.PROVIDER_1PASSWORD, vaultConfigBinary, vaultConfigBase, vaultConfigTag)
	if err != nil {
		logger.Error(vaultProvider.GetHumanReadableErrorMessage(err))
		exitMessage := fmt.Sprintln(vaultProvider.GetHumanReadableErrorMessage(err))
		exitWithLogo(exitMessage)
	}

	buchhalterConfigDirectory := viper.GetString("buchhalter_config_directory")
	recipeParser := parser.NewRecipeParser(logger, buchhalterConfigDirectory, buchhalterDirectory)

	localOICDBChecksum, err := recipeParser.GetChecksumOfLocalOICDB()
	if err != nil {
		logger.Error("Error calculating checksum of local Open Invoice Collector Database", "error", err)
		exitMessage := fmt.Sprintf("Error calculating checksum of local Open Invoice Collector Database: %s", err)
		exitWithLogo(exitMessage)
	}

	localOICDBSchemaChecksum, err := recipeParser.GetChecksumOfLocalOICDBSchema()
	if err != nil {
		logger.Error("Error calculating checksum of local Open Invoice Collector Database Schema", "error", err)
		exitMessage := fmt.Sprintf("Error calculating checksum of local Open Invoice Collector Database Schema: %s", err)
		exitWithLogo(exitMessage)
	}

	apiHost := viper.GetString("buchhalter_api_host")
	apiToken := viper.GetString("buchhalter_api_token")
	buchhalterAPIClient, err := repository.NewBuchhalterAPIClient(logger, apiHost, buchhalterConfigDirectory, apiToken, cliVersion)
	if err != nil {
		logger.Error("Error initializing Buchhalter API client", "error", err)
		exitMessage := fmt.Sprintf("Error initializing Buchhalter API client: %s", err)
		exitWithLogo(exitMessage)
	}

	viewModel := initialModel(logger, vaultProvider, buchhalterAPIClient, recipeParser)
	p := tea.NewProgram(viewModel)

	// Load vault items/try to connect to vault
	vaultItems, err := vaultProvider.LoadVaultItems()
	if err != nil {
		logger.Error(vaultProvider.GetHumanReadableErrorMessage(err))
		exitMessage := fmt.Sprintln(vaultProvider.GetHumanReadableErrorMessage(err))
		exitWithLogo(exitMessage)
	}

	// Check if vault items are available
	if len(vaultItems) == 0 {
		// TODO Add link with help article
		logger.Error("No credential items loaded from vault", "provider", "1Password", "cli_command", vaultConfigBinary, "vault", vaultConfigBase, "tag", vaultConfigTag)
		exitMessage := fmt.Sprintf("No credential items found in vault '%s' with tag '%s'. Please check your 1password vault items.", vaultConfigBase, vaultConfigTag)
		exitWithLogo(exitMessage)
	}
	logger.Info("Credential items loaded from vault", "num_items", len(vaultItems), "provider", "1Password", "cli_command", vaultConfigBinary, "vault", vaultConfigBase, "tag", vaultConfigTag)

	// Run recipes
	go runRecipes(p, logger, supplier, localOICDBChecksum, localOICDBSchemaChecksum, vaultProvider, documentArchive, recipeParser, buchhalterAPIClient)

	if _, err := p.Run(); err != nil {
		logger.Error("Error running program", "error", err)
		exitMessage := fmt.Sprintf("Error running program: %s", err)
		exitWithLogo(exitMessage)
	}
}

func runRecipes(p *tea.Program, logger *slog.Logger, supplier, localOICDBChecksum, localOICDBSchemaChecksum string, vaultProvider *vault.Provider1Password, documentArchive *archive.DocumentArchive, recipeParser *parser.RecipeParser, buchhalterAPIClient *repository.BuchhalterAPIClient) {
	p.Send(utils.ViewStatusUpdateMsg{Message: "Building archive index"})
	logger.Info("Building document archive index ...")

	err := documentArchive.BuildArchiveIndex()
	if err != nil {
		logger.Error("Error building document archive index", "error", err)
		p.Send(utils.ViewStatusUpdateMsg{Err: fmt.Errorf("error building document archive index: %w", err)})
	}
	p.Send(utils.ViewStatusUpdateMsg{
		Message:   "Building archive index",
		Completed: true,
	})

	// Check for OICDB schema updates
	p.Send(utils.ViewStatusUpdateMsg{Message: "Checking for OICDB schema updates"})
	logger.Info("Checking for OICDB schema updates ...", "local_checksum", localOICDBSchemaChecksum)

	err = buchhalterAPIClient.UpdateOpenInvoiceCollectorDBSchemaIfAvailable(localOICDBSchemaChecksum)
	if err != nil {
		logger.Error("Error checking for OICDB schema updates", "error", err)
		p.Send(utils.ViewStatusUpdateMsg{Err: fmt.Errorf("error checking for OICDB schema updates: %w", err)})
	}
	p.Send(utils.ViewStatusUpdateMsg{
		Message:   "Checking for OICDB schema updates",
		Completed: true,
	})

	developmentMode := viper.GetBool("dev")
	if !developmentMode {
		// Check for OICDB repository updates
		p.Send(utils.ViewStatusUpdateMsg{Message: "Checking for OICDB repository updates"})
		logger.Info("Checking for OICDB repository updates ...", "local_checksum", localOICDBChecksum)

		err = buchhalterAPIClient.UpdateOpenInvoiceCollectorDBIfAvailable(localOICDBChecksum)
		if err != nil {
			logger.Error("Error checking for OICDB repository updates", "error", err)
			p.Send(utils.ViewStatusUpdateMsg{Err: fmt.Errorf("error for OICDB repository updates: %w", err)})
		}
		p.Send(utils.ViewStatusUpdateMsg{
			Message:   "Checking for OICDB repository updates",
			Completed: true,
		})
	}

	statusUpdateMessage := "Loading recipes and credentials for suppliers"
	if len(supplier) > 0 {
		statusUpdateMessage = fmt.Sprintf("Loading recipe and credentials for supplier `%s`", supplier)
	}
	p.Send(utils.ViewStatusUpdateMsg{
		Message: statusUpdateMessage,
	})
	recipesToExecute, err := prepareRecipes(logger, supplier, vaultProvider, recipeParser)
	if err != nil {
		// No error logging needed. This is done in `prepareRecipes`
		p.Send(utils.ViewStatusUpdateMsg{
			Err:        fmt.Errorf("error loading recipes: %w", err),
			ShouldQuit: true,
		})
		return
	}

	// No pair of credentials found for supplier/recipes
	if len(recipesToExecute) == 0 {
		loggingErrorMessage := "No recipes found for suppliers"
		if len(supplier) > 0 {
			loggingErrorMessage = fmt.Sprintf("No recipe found for supplier `%s`", supplier)
		}
		logger.Error(loggingErrorMessage, "supplier", supplier, "error", err)
		p.Send(utils.ViewStatusUpdateMsg{
			Err:        errors.New(loggingErrorMessage),
			ShouldQuit: true,
		})
		return
	}
	p.Send(utils.ViewStatusUpdateMsg{
		Message:   statusUpdateMessage,
		Completed: true,
	})

	recipeCount := len(recipesToExecute)
	if recipeCount == 1 {
		statusUpdateMessage = fmt.Sprintf("Running one recipe for supplier `%s` ...", recipesToExecute[0].recipe.Supplier)
		logger.Info("Running one recipe ...", "supplier", recipesToExecute[0].recipe.Supplier)
	} else {
		statusUpdateMessage = fmt.Sprintf("Running recipes for %d suppliers ...", recipeCount)
		logger.Info("Running recipes for multiple suppliers ...", "num_suppliers", recipeCount)
	}
	p.Send(utils.ViewStatusUpdateMsg{Message: statusUpdateMessage})
	p.Send(utils.ViewProgressUpdateMsg{Percent: 0.001})

	buchhalterDocumentsDirectory := viper.GetString("buchhalter_documents_directory")
	buchhalterConfigDirectory := viper.GetString("buchhalter_config_directory")
	buchhalterMaxDownloadFilesPerReceipt := viper.GetInt("buchhalter_max_download_files_per_receipt")

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

		// Load username, password, totp from vault
		p.Send(utils.ViewStatusUpdateMsg{
			Message: fmt.Sprintf("Requesting credentials from vault for supplier `%s`", recipesToExecute[i].recipe.Supplier),
		})
		logger.Info("Requesting credentials from vault", "supplier", recipesToExecute[i].recipe.Supplier)
		recipeCredentials, err := vaultProvider.GetCredentialsByItemId(recipesToExecute[i].vaultItemId)
		if err != nil {
			logger.Error(vaultProvider.GetHumanReadableErrorMessage(err))
			// TODO Let `GetHumanReadableErrorMessage` return a proper error
			p.Send(utils.ViewStatusUpdateMsg{
				Err: errors.New(vaultProvider.GetHumanReadableErrorMessage(err)),
			})
			continue
		}
		p.Send(utils.ViewStatusUpdateMsg{
			Message:   fmt.Sprintf("Requested credentials from vault for supplier `%s`", recipesToExecute[i].recipe.Supplier),
			Completed: true,
		})

		p.Send(utils.ViewStatusUpdateMsg{Message: fmt.Sprintf("Downloading invoices from `%s`", recipesToExecute[i].recipe.Supplier)})
		logger.Info("Downloading invoices ...", "supplier", recipesToExecute[i].recipe.Supplier, "supplier_type", recipesToExecute[i].recipe.Type)
		switch recipesToExecute[i].recipe.Type {
		case "browser":
			browserDriver, err := browser.NewBrowserDriver(logger, recipeCredentials, buchhalterDocumentsDirectory, documentArchive, buchhalterMaxDownloadFilesPerReceipt)
			if err != nil {
				p.Send(utils.ViewStatusUpdateMsg{
					Err: fmt.Errorf("error downloading invoices from %s: %w", recipesToExecute[i].recipe.Supplier, err),
				})
				// TODO Should we continue here or abort?
			}

			// Send the browser context to the view layer
			// This is needed in case of an external abort signal (e.g. CTRL+C).
			p.Send(updateBrowserContext{ctx: browserDriver.GetContext()})

			// TODO Catch error here
			recipeResult = browserDriver.RunRecipe(p, totalStepCount, stepCountInCurrentRecipe, baseCountStep, recipesToExecute[i].recipe)
			if ChromeVersion == "" {
				ChromeVersion = browserDriver.ChromeVersion
			}

			// We don't need to call `chromedp.Cancel()` here.
			// The browserDriver will be closed gracefully when the recipe is finished.
			// In case of an external abort signal (e.g. CTRL+C), bubbletea will call `chromedp.Cancel()`.

		case "client":
			clientDriver, err := browser.NewClientAuthBrowserDriver(logger, recipeCredentials, buchhalterConfigDirectory, buchhalterDocumentsDirectory, documentArchive)
			if err != nil {
				p.Send(utils.ViewStatusUpdateMsg{
					Err: fmt.Errorf("error downloading invoices from %s: %w", recipesToExecute[i].recipe.Supplier, err),
				})
				// TODO Should we continue here or abort?
			}

			// Send the browser context to the view layer
			// This is needed in case of an external abort signal (e.g. CTRL+C).
			p.Send(updateBrowserContext{ctx: clientDriver.GetContext()})

			// TODO Catch error here
			recipeResult = clientDriver.RunRecipe(p, totalStepCount, stepCountInCurrentRecipe, baseCountStep, recipesToExecute[i].recipe)
			if ChromeVersion == "" {
				ChromeVersion = clientDriver.ChromeVersion
			}

			// We don't need to call `chromedp.Cancel()` here.
			// The browserDriver will be closed gracefully when the recipe is finished.
			// In case of an external abort signal (e.g. CTRL+C), bubbletea will call `chromedp.Cancel()`.
		}

		rdx := repository.RunDataSupplier{
			Supplier:         recipesToExecute[i].recipe.Supplier,
			Version:          recipesToExecute[i].recipe.Version,
			Status:           recipeResult.StatusText,
			LastErrorMessage: recipeResult.LastErrorMessage,
			Duration:         time.Since(startTime).Seconds(),
			NewFilesCount:    recipeResult.NewFilesCount,
		}
		RunData = append(RunData, rdx)

		// TODO Check for recipeResult.LastErrorMessage
		p.Send(viewMsgRecipeDownloadResultMsg{
			duration:      time.Since(startTime),
			newFilesCount: recipeResult.NewFilesCount,
			step:          recipeResult.StatusTextFormatted,
			errorMessage:  recipeResult.LastErrorMessage,
		})
		logger.Info("Downloading invoices ... completed", "supplier", recipesToExecute[i].recipe.Supplier, "supplier_type", recipesToExecute[i].recipe.Type, "duration", time.Since(startTime), "new_files", recipeResult.NewFilesCount)
		invoiceLabel := "invoices"
		if recipeResult.NewFilesCount == 1 {
			invoiceLabel = "invoice"
		}
		p.Send(utils.ViewStatusUpdateMsg{
			Message:   fmt.Sprintf("Downloaded %d %s from `%s`", recipeResult.NewFilesCount, invoiceLabel, recipesToExecute[i].recipe.Supplier),
			Completed: true,
		})

		baseCountStep += stepCountInCurrentRecipe
	}

	// If we have a premium user run, upload the documents to the buchhalter API
	logger.Info("Checking if we have a premium subscription to Buchhalter API ...")
	p.Send(utils.ViewStatusUpdateMsg{
		Message: "Checking if a premium subscription to Buchhalter API exists",
	})
	user, err := buchhalterAPIClient.GetAuthenticatedUser()
	if err != nil {
		logger.Error("Error retrieving authenticated user", "error", err)
		p.Send(utils.ViewStatusUpdateMsg{Err: fmt.Errorf("error retrieving a premium subscription to Buchhalter API: %w", err)})
	}
	if user != nil && len(user.User.ID) > 0 {
		statusUpdateMessage = "Uploading documents to Buchhalter API"
		if len(supplier) > 0 {
			statusUpdateMessage = fmt.Sprintf("Uploading documents of supplier `%s` to Buchhalter API", supplier)
		}
		p.Send(utils.ViewStatusUpdateMsg{Message: statusUpdateMessage})

		countUploadedFiles := 0
		countSkippedExistFiles := 0
		fileIndex := documentArchive.GetFileIndex()
		for fileChecksum, fileInfo := range fileIndex {
			// If the user is only working on a specific supplier, skip the upload of documents for other suppliers
			if len(supplier) > 0 && fileInfo.Supplier != supplier {
				logger.Info("Skipping document upload to Buchhalter API due to mismatch in supplier", "file", fileInfo.Path, "selected_supplier", supplier, "file_supplier", fileInfo.Supplier)
				continue
			}

			logger.Info("Uploading document to Buchhalter API ...", "file", fileInfo.Path, "checksum", fileChecksum)
			result, err := buchhalterAPIClient.DoesDocumentExist(fileChecksum)
			if err != nil {
				// TODO Implement better error handling
				logger.Error("Error checking if document exists already in Buchhalter API", "file", fileInfo.Path, "checksum", fileChecksum, "error", err)
				continue
			}
			// If the file exists already, skip it
			if result {
				logger.Info("Uploading document to Buchhalter API ... exists already", "file", fileInfo.Path, "checksum", fileChecksum)
				countSkippedExistFiles++
				continue
			}
			logger.Info("Uploading document to Buchhalter API ... does not exist already", "file", fileInfo.Path, "checksum", fileChecksum)

			err = buchhalterAPIClient.UploadDocument(fileInfo.Path, fileInfo.Supplier)
			if err != nil {
				// TODO Implement better error handling
				logger.Error("Error uploading document to Buchhalter API", "file", fileInfo.Path, "supplier", fileInfo.Supplier, "error", err)
				continue
			}
			countUploadedFiles++
		}
		documentsLabel := "documents"
		if countUploadedFiles == 1 {
			documentsLabel = "document"
		}
		statusUpdateMessage = fmt.Sprintf("Uploaded %d %s to Buchhalter API (%d skipped, because they already exist)", countUploadedFiles, documentsLabel, countSkippedExistFiles)
		if len(supplier) > 0 {
			statusUpdateMessage = fmt.Sprintf("Uploaded %d %s of supplier `%s` to Buchhalter API (%d skipped, because they already exist)", countUploadedFiles, documentsLabel, supplier, countSkippedExistFiles)
		}
		p.Send(utils.ViewStatusUpdateMsg{
			Message:   statusUpdateMessage,
			Completed: true,
		})
	} else {
		logger.Info("Skipping document upload to Buchhalter API due to missing premium subscription")
		p.Send(utils.ViewStatusUpdateMsg{
			Message:   "Skipping document upload to Buchhalter API due to missing premium subscription",
			Completed: true,
		})
	}
	// TODO Move to own function/bubbletea cmd
	alwaysSendMetrics := viper.GetBool("buchhalter_always_send_metrics")
	if !developmentMode && alwaysSendMetrics {
		logger.Info("Sending usage metrics to Buchhalter API", "always_send_metrics", alwaysSendMetrics, "development_mode", developmentMode)
		p.Send(utils.ViewStatusUpdateMsg{Message: "Sending usage metrics to Buchhalter API"})
		err = buchhalterAPIClient.SendMetrics(RunData, cliVersion, ChromeVersion, vaultProvider.Version, recipeParser.OicdbVersion)
		if err != nil {
			logger.Error("Error sending usage metrics to Buchhalter API", "error", err)
			p.Send(utils.ViewStatusUpdateMsg{
				Err:        fmt.Errorf("error sending usage metrics to Buchhalter API: %w", err),
				ShouldQuit: true,
			})
			return
		}

		p.Send(utils.ViewStatusUpdateMsg{
			Message:    "Sending usage metrics to Buchhalter API",
			Completed:  true,
			ShouldQuit: true,
		})

	} else if developmentMode {
		p.Send(viewQuitMsg{})

	} else {
		p.Send(viewMsgModeUpdate{
			mode:    "sendMetrics",
			title:   "Let's improve buchhalter-cli together!",
			details: "Allow buchhalter-cli to send anonymized usage data to our api?",
		})
	}
}

// TODO Distinguish between "no recipes found" and "no credentials found"
// TODO Rename function to something more meaningful
func prepareRecipes(logger *slog.Logger, supplier string, vaultProvider *vault.Provider1Password, recipeParser *parser.RecipeParser) ([]recipeToExecute, error) {
	var r []recipeToExecute

	developmentMode := viper.GetBool("dev")
	logger.Info("Loading recipes for suppliers ...", "development_mode", developmentMode)
	loadRecipeResult, err := recipeParser.LoadRecipes(developmentMode)
	if err != nil {
		logger.Error("Error loading recipes for suppliers", "error", err, "load_recipe_result", loadRecipeResult)
		return r, err
	}

	// Run single supplier recipe
	stepCount := 0
	vaultItems := vaultProvider.VaultItems
	if supplier != "" {
		logger.Info("Search for credentials for suppliers recipe ...", "supplier", supplier)
		for i := range vaultItems {
			// Check if a recipe exists for the item
			recipe := recipeParser.GetRecipeForItem(vaultItems[i], vaultProvider.UrlsByItemId)
			if recipe != nil && supplier == recipe.Supplier {
				r = append(r, recipeToExecute{recipe, vaultItems[i].ID})
				logger.Info("Search for credentials for suppliers recipe ... found", "supplier", supplier, "credentials_id", vaultItems[i].ID)
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
				logger.Info("Search for matching pairs of recipes for supplier recipes and credentials ... found", "supplier", recipe.Supplier, "credentials_id", vaultItems[i].ID)
			}
		}
	}

	return r, nil
}

func sendMetrics(buchhalterAPIClient *repository.BuchhalterAPIClient, a bool, vaultVersion, oicdbVersion string) {
	// TODO Add logging for sendMetrics

	err := buchhalterAPIClient.SendMetrics(RunData, cliVersion, ChromeVersion, vaultVersion, oicdbVersion)
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
	dotStyle      = helpStyle.UnsetMargins()
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#EA4335"))
	durationStyle = dotStyle
	spinnerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#D6D58E"))
	appStyle      = lipgloss.NewStyle().Margin(1, 2, 0, 2)
	choices       = []string{"Yes", "No", "Always yes (don't ask again)"}
)

// viewModel is the bubbletea application main viewModel (view)
type viewModel struct {
	mode string

	// UI
	actionsCompleted []string
	actionInProgress string
	actionError      string
	actionDetails    string
	spinner          spinner.Model

	currentAction string
	details       string
	showProgress  bool
	progress      progress.Model
	results       []viewMsgRecipeDownloadResultMsg
	quitting      bool
	hasError      bool
	cursor        int
	choice        string

	vaultProvider       *vault.Provider1Password
	buchhalterAPIClient *repository.BuchhalterAPIClient
	recipeParser        *parser.RecipeParser
	logger              *slog.Logger

	// Browser
	browserCtx context.Context
}

// updateBrowserContext is a message type to update the browser context in the bubbletea application.
type updateBrowserContext struct {
	ctx context.Context
}

// viewQuitMsg initiates the shutdown sequence for the bubbletea application.
type viewQuitMsg struct{}

// viewMsgRecipeDownloadResultMsg registers a recipe download result in the bubbletea application.
type viewMsgRecipeDownloadResultMsg struct {
	duration      time.Duration
	step          string
	errorMessage  string
	newFilesCount int
}

func (r viewMsgRecipeDownloadResultMsg) String() string {
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

// viewMsgModeUpdate updates the mode of the bubbletea application.
// "Mode" represents special code pathed of the applications.
//
// Examples: sendMetrics, sync, etc.
type viewMsgModeUpdate struct {
	mode    string
	title   string
	details string
}

type tickMsg time.Time

// initialModel returns the model for the bubbletea application.
func initialModel(logger *slog.Logger, vaultProvider *vault.Provider1Password, buchhalterAPIClient *repository.BuchhalterAPIClient, recipeParser *parser.RecipeParser) viewModel {
	const numLastResults = 5

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	m := viewModel{
		mode:         "sync",
		showProgress: true,
		progress:     progress.New(progress.WithGradient("#9FC131", "#DBF227")),
		spinner:      s,
		results:      make([]viewMsgRecipeDownloadResultMsg, numLastResults),
		hasError:     false,

		vaultProvider:       vaultProvider,
		buchhalterAPIClient: buchhalterAPIClient,
		recipeParser:        recipeParser,
		logger:              logger,

		// Browser
		browserCtx: nil,
	}

	return m
}

// Init initializes the bubbletea application.
// Returns an initial command for the application to run.
func (m viewModel) Init() tea.Cmd {
	return m.spinner.Tick
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

	case utils.ViewStatusUpdateMsg:
		m.actionInProgress = msg.Message
		m.actionDetails = msg.Details

		if msg.Completed {
			// TODO Add error also to completed if it doesn't quit?
			m.actionsCompleted = append(m.actionsCompleted, msg.Message)
			m.actionInProgress = ""
		}

		if msg.Err != nil {
			m.actionError = msg.Err.Error()
		}

		if msg.ShouldQuit {
			return m, func() tea.Msg {
				return viewQuitMsg{}
			}
		}

		return m, nil

	case updateBrowserContext:
		m.logger.Info("Updating browser context")
		m.browserCtx = msg.ctx
		return m, nil

	case viewQuitMsg:
		m.logger.Info("Initiating shutdown sequence")

		mn := quit(m)
		return mn, tea.Quit

	case tea.WindowSizeMsg:
		m.progress.Width = msg.Width - padding*2 - 4
		if m.progress.Width > maxWidth {
			m.progress.Width = maxWidth
		}
		return m, nil

	case viewMsgRecipeDownloadResultMsg:
		m.results = append(m.results[1:], msg)
		if msg.errorMessage != "" {
			m.hasError = true
			m.details = msg.errorMessage
			mn := quit(m)
			return mn, tea.Quit
		}
		return m, nil

	case viewMsgModeUpdate:
		m.actionInProgress = msg.title
		m.actionDetails = msg.details

		m.mode = msg.mode
		m.showProgress = false
		return m, nil

	case utils.ViewProgressUpdateMsg:
		cmd := m.progress.SetPercent(msg.Percent)
		return m, cmd

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
	s := strings.Builder{}
	s.WriteString(fmt.Sprintf(
		"%s\n%s\n%s%s\n%s\n",
		headerStyle(LogoText),
		textStyle("Automatically sync all your incoming invoices from your suppliers. "),
		textStyle("More information at: "),
		textStyleBold("https://buchhalter.ai"),
		textStyleGrayBold(fmt.Sprintf("Using OICDB %s and CLI %s", m.recipeParser.OicdbVersion, cliVersion)),
	) + "\n")

	for _, actionCompleted := range m.actionsCompleted {
		s.WriteString(checkMark.Render() + " " + textStyleBold(actionCompleted) + "\n")
	}

	if len(m.actionInProgress) > 0 {
		if len(m.actionDetails) > 0 {
			s.WriteString(m.spinner.View() + textStyleBold(m.actionInProgress))
			s.WriteString(helpStyle.Render("  " + m.actionDetails))
		} else {
			s.WriteString(m.spinner.View() + textStyleBold(m.actionInProgress) + "\n")
		}
	}

	if len(m.actionError) > 0 {
		s.WriteString(errorkMark.Render() + " " + errorStyle.Render(m.actionError) + "\n")
		if len(m.actionDetails) > 0 {
			s.WriteString(helpStyle.Render("  " + m.actionDetails))
		}
	}

	s.WriteString("\n")

	if len(m.currentAction) > 0 {
		if !m.hasError {
			s.WriteString(m.spinner.View() + m.currentAction)
			s.WriteString(helpStyle.Render("  " + m.details))
		} else {
			s.WriteString(errorStyle.Render("ERROR: " + m.currentAction))
			s.WriteString(helpStyle.Render("  " + m.details))
		}
		s.WriteString("\n")
	}

	if m.showProgress {
		s.WriteString(m.progress.View() + "\n\n")
	}

	if !m.hasError && m.mode == "sync" {
		for _, res := range m.results {
			s.WriteString(res.String() + "\n")
		}
	}

	if m.mode == "sendMetrics" && !m.quitting {
		for i := 0; i < len(choices); i++ {
			if m.cursor == i {
				s.WriteString("(•) ")
			} else {
				s.WriteString("( ) ")
			}
			s.WriteString(choices[i])
			s.WriteString("\n")
		}
	}

	// Quitting or not?
	if !m.quitting {
		s.WriteString(helpStyle.Render("Press q to exit"))
	}
	if m.quitting {
		s.WriteString("\n")
	}

	return appStyle.Render(s.String())
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
		m.actionInProgress = "Thanks for using buchhalter.ai!"
		m.actionDetails = "HAVE A NICE DAY! :)"

		m.quitting = true
		m.showProgress = false
	}

	// Stopping the browser instance
	m.logger.Info("Stopping browser instance")
	if m.browserCtx != nil {
		err := browser.Quit(m.browserCtx)
		if err != nil {
			m.logger.Error("Error cancelling browser", "error", err)
		}
	}

	return m
}
