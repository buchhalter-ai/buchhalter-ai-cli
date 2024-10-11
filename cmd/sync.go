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

type recipeToExecute struct {
	recipe      *parser.Recipe
	vaultItemId string
}

type buchhalterMetricsRecord struct {
	CliVersion    string `json:"cliVersion,omitempty"`
	OicdbVersion  string `json:"oicdbVersion,omitempty"`
	VaultVersion  string `json:"vaultVersion,omitempty"`
	ChromeVersion string `json:"chromeVersion,omitempty"`
}

type syncCommandConfig struct {
	// Buchhalter
	buchhalterDirectory          string
	buchhalterConfigDirectory    string
	buchhalterDocumentsDirectory string

	// Vault
	vaultConfigBinary string
	vaultConfig       vaultConfiguration
	vaultConfigTag    string
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

	// Init vaults from configuration
	credentialProviderVaults := []vaultConfiguration{}
	if err := viper.UnmarshalKey("credential_provider_vaults", &credentialProviderVaults); err != nil {
		exitMessage := fmt.Sprintf("Error reading configuration field `credential_provider_vaults`: %s", err)
		exitWithLogo(exitMessage)
	}
	selectedVault := getSelectedVaultConfiguration(credentialProviderVaults)
	if selectedVault == nil {
		selectedVault = &vaultConfiguration{}
	}

	config := &syncCommandConfig{
		buchhalterDirectory:          viper.GetString("buchhalter_directory"),
		buchhalterConfigDirectory:    viper.GetString("buchhalter_config_directory"),
		buchhalterDocumentsDirectory: viper.GetString("buchhalter_documents_directory"),
		vaultConfigBinary:            viper.GetString("credential_provider_cli_command"),
		vaultConfig:                  *selectedVault,
		vaultConfigTag:               viper.GetString("credential_provider_item_tag"),
	}

	// Init logging
	developmentMode := viper.GetBool("dev")
	logSetting, err := cmd.Flags().GetBool("log")
	if err != nil {
		exitMessage := fmt.Sprintf("Error reading log flag: %s", err)
		exitWithLogo(exitMessage)
	}
	logger, err := initializeLogger(logSetting, developmentMode, config.buchhalterDirectory)
	if err != nil {
		exitMessage := fmt.Sprintf("Error on initializing logging: %s", err)
		exitWithLogo(exitMessage)
	}
	logger.Info("Booting up", "development_mode", developmentMode)
	defer logger.Info("Shutting down")

	// Init Buchhalter API client
	apiHost := viper.GetString("buchhalter_api_host")
	apiToken := viper.GetString("buchhalter_api_token")
	buchhalterAPIClient, err := repository.NewBuchhalterAPIClient(logger, apiHost, config.buchhalterConfigDirectory, apiToken, cliVersion)
	if err != nil {
		logger.Error("Error initializing Buchhalter API client", "error", err)
		exitMessage := fmt.Sprintf("Error initializing Buchhalter API client: %s", err)
		exitWithLogo(exitMessage)
	}

	// Init the bubbletea program
	viewModelSync := initviewModelSync(logger, buchhalterAPIClient)
	p := tea.NewProgram(viewModelSync)

	// Run the primary logic
	go runSyncCommandLogic(p, logger, config, supplier, buchhalterAPIClient)

	// Run the bubbletea program
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

func runSyncCommandLogic(p *tea.Program, logger *slog.Logger, config *syncCommandConfig, supplier string, buchhalterAPIClient *repository.BuchhalterAPIClient) {
	// Checking if we have a vault configuration
	// This can happen if the user has not selected a vault configuration yet or starts it for the first time
	if len(config.vaultConfig.Name) == 0 || len(config.vaultConfig.ID) == 0 {
		logger.Error("No vault configuration found")
		p.Send(utils.ViewStatusUpdateMsg{
			Err:        errors.New("no vault configuration found. Please run `buchhalter vault add` or `buchhalter vault select` first to add a new vault to buchhalter or select one as default."),
			Completed:  true,
			ShouldQuit: true,
		})
		return
	}

	// Init vault provider
	logger.Info("Initializing credential provider", "provider", "1Password", "cli_command", config.vaultConfigBinary, "vault", config.vaultConfig.Name, "tag", config.vaultConfigTag)
	statusUpdateMessage := fmt.Sprintf("Initializing credential provider 1Password with vault '%s' and tag '%s'", config.vaultConfig.Name, config.vaultConfigTag)
	p.Send(utils.ViewStatusUpdateMsg{Message: statusUpdateMessage})
	vaultProvider, err := vault.GetProvider(vault.PROVIDER_1PASSWORD, config.vaultConfigBinary, config.vaultConfig.Name, config.vaultConfigTag)
	if err != nil {
		logger.Error("error initializing credential provider 1Password: %s", "error", err)
		p.Send(utils.ViewStatusUpdateMsg{
			Err:        fmt.Errorf("error initializing credential provider 1Password: %s", vaultProvider.GetHumanReadableErrorMessage(err)),
			Completed:  true,
			ShouldQuit: true,
		})
		return
	}

	// Load vault items/try to connect to vault
	vaultItems, err := vaultProvider.LoadVaultItems()
	if err != nil {
		logger.Error("error initializing credential provider 1Password", "error", err)
		p.Send(utils.ViewStatusUpdateMsg{
			Err:        fmt.Errorf("error initializing credential provider 1Password: %s", vaultProvider.GetHumanReadableErrorMessage(err)),
			Completed:  true,
			ShouldQuit: true,
		})
		return
	}
	p.Send(utils.ViewStatusUpdateMsg{
		Message:   statusUpdateMessage,
		Completed: true,
	})

	// Check if vault items are available
	if len(vaultItems) == 0 {
		logger.Error("No credential items loaded from vault", "provider", "1Password", "cli_command", config.vaultConfigBinary, "vault", config.vaultConfig.Name, "tag", config.vaultConfigTag)
		exitMessage := fmt.Sprintf("No credential items found in vault '%s' with tag '%s'. Please check your 1password vault items.", config.vaultConfig.Name, config.vaultConfigTag)
		p.Send(utils.ViewStatusUpdateMsg{
			Err:        fmt.Errorf("error initializing credential provider 1Password: %s", exitMessage),
			Completed:  true,
			ShouldQuit: true,
		})
		return
	}
	logger.Info("Credential items loaded from vault", "num_items", len(vaultItems), "provider", "1Password", "cli_command", config.vaultConfigBinary, "vault", config.vaultConfig.Name, "tag", config.vaultConfigTag)
	p.Send(utils.ViewStatusUpdateMsg{
		Message:   fmt.Sprintf("Loaded %d credential items from vault '%s' with tag '%s'", len(vaultItems), config.vaultConfig.Name, config.vaultConfigTag),
		Completed: true,
	})

	// Init recipe parser
	p.Send(utils.ViewStatusUpdateMsg{Message: "Initializing recipe parser to read local Open Invoice Collector Database"})
	recipeParser := parser.NewRecipeParser(logger, config.buchhalterConfigDirectory, config.buchhalterDirectory)
	localOICDBChecksum, err := recipeParser.GetChecksumOfLocalOICDB()
	if err != nil {
		logger.Error("Error calculating checksum of local Open Invoice Collector Database", "error", err)
		p.Send(utils.ViewStatusUpdateMsg{
			Err:        fmt.Errorf("error calculating checksum of local Open Invoice Collector Database: %w", err),
			Completed:  true,
			ShouldQuit: true,
		})
		return
	}

	localOICDBSchemaChecksum, err := recipeParser.GetChecksumOfLocalOICDBSchema()
	if err != nil {
		logger.Error("Error calculating checksum of local Open Invoice Collector Database Schema", "error", err)
		p.Send(utils.ViewStatusUpdateMsg{
			Err:        fmt.Errorf("error calculating checksum of local Open Invoice Collector Database Schema: %w", err),
			Completed:  true,
			ShouldQuit: true,
		})
		return
	}
	p.Send(utils.ViewStatusUpdateMsg{
		Message:   "Initializing recipe parser to read local Open Invoice Collector Database",
		Completed: true,
	})

	p.Send(utils.ViewStatusUpdateMsg{Message: "Building archive index"})
	logger.Info("Building document archive index ...")

	// Init document archive
	documentArchive := archive.NewDocumentArchive(logger, config.buchhalterDocumentsDirectory)
	err = documentArchive.BuildArchiveIndex()
	if err != nil {
		logger.Error("Error building document archive index", "error", err)
		p.Send(utils.ViewStatusUpdateMsg{
			Err:       fmt.Errorf("error building document archive index: %w", err),
			Completed: true,
		})
	} else {
		p.Send(utils.ViewStatusUpdateMsg{
			Message:   "Building archive index",
			Completed: true,
		})
	}

	// Check for OICDB schema updates
	p.Send(utils.ViewStatusUpdateMsg{Message: "Checking for OICDB schema updates"})
	logger.Info("Checking for OICDB schema updates ...", "local_checksum", localOICDBSchemaChecksum)

	err = buchhalterAPIClient.UpdateOpenInvoiceCollectorDBSchemaIfAvailable(localOICDBSchemaChecksum)
	if err != nil {
		logger.Error("Error checking for OICDB schema updates", "error", err)
		p.Send(utils.ViewStatusUpdateMsg{
			Err:       fmt.Errorf("error checking for OICDB schema updates: %w", err),
			Completed: true,
		})
	} else {
		p.Send(utils.ViewStatusUpdateMsg{
			Message:   "Checking for OICDB schema updates",
			Completed: true,
		})
	}

	developmentMode := viper.GetBool("dev")
	if !developmentMode {
		// Check for OICDB repository updates
		p.Send(utils.ViewStatusUpdateMsg{Message: "Checking for OICDB repository updates"})
		logger.Info("Checking for OICDB repository updates ...", "local_checksum", localOICDBChecksum)

		err = buchhalterAPIClient.UpdateOpenInvoiceCollectorDBIfAvailable(localOICDBChecksum)
		if err != nil {
			logger.Error("Error checking for OICDB repository updates", "error", err)
			p.Send(utils.ViewStatusUpdateMsg{
				Err:       fmt.Errorf("error for OICDB repository updates: %w", err),
				Completed: true,
			})
		} else {
			p.Send(utils.ViewStatusUpdateMsg{
				Message:   "Checking for OICDB repository updates",
				Completed: true,
			})
		}
	}

	statusUpdateMessage = "Loading recipes and credentials for suppliers"
	if len(supplier) > 0 {
		statusUpdateMessage = fmt.Sprintf("Loading recipe and credentials for supplier `%s`", supplier)
	}
	p.Send(utils.ViewStatusUpdateMsg{
		Message: statusUpdateMessage,
	})
	recipesToExecute, err := loadRecipesAndMatchingVaultItems(logger, supplier, vaultProvider, recipeParser)
	if err != nil {
		// No error logging needed. This is done in `loadRecipesAndMatchingVaultItems`
		// If an error occurs, this means the recipes could not be loaded.
		p.Send(utils.ViewStatusUpdateMsg{
			Err:        fmt.Errorf("error loading recipes: %w", err),
			ShouldQuit: true,
		})
		return
	}

	// At this point in time, we have all the information we need to send metrics
	p.Send(buchhalterMetricsRecord{
		CliVersion:   cliVersion,
		VaultVersion: vaultProvider.Version,
		OicdbVersion: recipeParser.OicdbVersion,
	})

	// No pair of credentials found for supplier/recipes
	if len(recipesToExecute) == 0 {
		loggingErrorMessage := "No matching pair of recipes <--> credentials found for suppliers"
		if len(supplier) > 0 {
			loggingErrorMessage = fmt.Sprintf("No matching pair of recipes <--> credentials found for supplier `%s`", supplier)
		}
		logger.Error(loggingErrorMessage, "supplier", supplier, "error", err)
		p.Send(utils.ViewStatusUpdateMsg{
			Err:        errors.New(loggingErrorMessage),
			ShouldQuit: true,
		})
		return
	}
	statusUpdateMessage = fmt.Sprintf("%s (OICDB %s)", statusUpdateMessage, recipeParser.OicdbVersion)
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
	chromeVersion := ""
	recipeRunData := make(repository.RunData, 0)
	recipeResult := utils.RecipeResult{}
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
			logger.Error("error while requesting credentials from vault", "supplier", recipesToExecute[i].recipe.Supplier, "error", err)
			p.Send(utils.ViewStatusUpdateMsg{
				Err:       vaultProvider.GetHumanReadableErrorMessage(err),
				Completed: true,
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
				logger.Error("Error initializing a new browser driver", "error", err, "supplier", recipesToExecute[i].recipe.Supplier)
				p.Send(utils.ViewStatusUpdateMsg{
					Err:       fmt.Errorf("error initializing a new browser driver for supplier `%s`: %w", recipesToExecute[i].recipe.Supplier, err),
					Completed: true,
				})
				// We skip this supplier and continue with the next one
				continue
			}

			// Send the browser context to the view layer
			// This is needed in case of an external abort signal (e.g. CTRL+C).
			p.Send(updateBrowserContext{ctx: browserDriver.GetContext()})

			recipeResult, err = browserDriver.RunRecipe(p, totalStepCount, stepCountInCurrentRecipe, baseCountStep, recipesToExecute[i].recipe)
			if err != nil {
				logger.Error("Error running browser recipe", "error", err, "supplier", recipesToExecute[i].recipe.Supplier)
				p.Send(utils.ViewStatusUpdateMsg{
					Err:       fmt.Errorf("error running browser recipe for supplier `%s`: %w", recipesToExecute[i].recipe.Supplier, err),
					Completed: true,
				})
				// We skip this supplier and continue with the next one
				continue
			}
			chromeVersion = browserDriver.ChromeVersion

			// We don't need to call `chromedp.Cancel()` here.
			// The browserDriver will be closed gracefully when the recipe is finished.
			// In case of an external abort signal (e.g. CTRL+C), bubbletea will call `chromedp.Cancel()`.

		case "client":
			clientDriver, err := browser.NewClientAuthBrowserDriver(logger, recipeCredentials, buchhalterConfigDirectory, buchhalterDocumentsDirectory, documentArchive)
			if err != nil {

				logger.Error("Error initializing a new client auth browser driver", "error", err, "supplier", recipesToExecute[i].recipe.Supplier)
				p.Send(utils.ViewStatusUpdateMsg{
					Err:       fmt.Errorf("error initializing a new client auth browser for supplier `%s`: %w", recipesToExecute[i].recipe.Supplier, err),
					Completed: true,
				})
				// We skip this supplier and continue with the next one
				continue
			}

			// Send the browser context to the view layer
			// This is needed in case of an external abort signal (e.g. CTRL+C).
			p.Send(updateBrowserContext{ctx: clientDriver.GetContext()})

			recipeResult, err = clientDriver.RunRecipe(p, totalStepCount, stepCountInCurrentRecipe, baseCountStep, recipesToExecute[i].recipe)
			if err != nil {
				logger.Error("Error running browser recipe", "error", err, "supplier", recipesToExecute[i].recipe.Supplier)
				p.Send(utils.ViewStatusUpdateMsg{
					Err:       fmt.Errorf("error running browser recipe for supplier `%s`: %w", recipesToExecute[i].recipe.Supplier, err),
					Completed: true,
				})
				// We skip this supplier and continue with the next one
				continue
			}
			chromeVersion = clientDriver.ChromeVersion

			// We don't need to call `chromedp.Cancel()` here.
			// The browserDriver will be closed gracefully when the recipe is finished.
			// In case of an external abort signal (e.g. CTRL+C), bubbletea will call `chromedp.Cancel()`.
		}

		// Send Chrome Version into metrics
		if len(chromeVersion) > 0 {
			p.Send(buchhalterMetricsRecord{ChromeVersion: chromeVersion})
		}

		runDataSupplierRecord := repository.RunDataSupplier{
			// Recipe
			Supplier: recipesToExecute[i].recipe.Supplier,
			Version:  recipesToExecute[i].recipe.Version,

			// Run result
			Status:           recipeResult.StatusText,
			LastErrorMessage: recipeResult.LastErrorMessage,
			NewFilesCount:    recipeResult.NewFilesCount,
			Duration:         time.Since(startTime).Seconds(),
		}

		p.Send(newRecipeRunDataRecordMsg{record: runDataSupplierRecord})
		recipeRunData = append(recipeRunData, runDataSupplierRecord)

		// We send the recipeResult in a separate message to the view layer
		// This could be optimized (and bundled together with newRecipeRunDataRecordMsg),
		// but for now this is good enough.
		p.Send(viewMsgRecipeDownloadResultMsg{
			duration:      time.Since(startTime),
			newFilesCount: recipeResult.NewFilesCount,
			step:          recipeResult.StatusTextFormatted,
			errorMessage:  recipeResult.LastErrorMessage,
		})

		logger.Info("Downloading invoices ... completed",
			"supplier", recipesToExecute[i].recipe.Supplier,
			"supplier_type", recipesToExecute[i].recipe.Type,
			"duration", time.Since(startTime),
			"new_files", recipeResult.NewFilesCount,
		)
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
		p.Send(utils.ViewStatusUpdateMsg{
			Err:       fmt.Errorf("error retrieving a premium subscription to Buchhalter API: %w", err),
			Completed: true,
		})
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
				// Skip the file if we can't check the existence of the document in the API
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
				p.Send(utils.ViewStatusUpdateMsg{
					Err:       fmt.Errorf("error uploading document `%s` from `%s` to Buchhalter API: %w", fileInfo.Path, fileInfo.Supplier, err),
					Completed: true,
				})
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

	// Send metrics to Buchhalter API
	alwaysSendMetrics := viper.GetBool("buchhalter_always_send_metrics")
	if !developmentMode && alwaysSendMetrics {
		logger.Info("Sending usage metrics to Buchhalter API", "always_send_metrics", alwaysSendMetrics, "development_mode", developmentMode)
		p.Send(utils.ViewStatusUpdateMsg{Message: "Sending usage metrics to Buchhalter API"})
		err = buchhalterAPIClient.SendMetrics(recipeRunData, cliVersion, chromeVersion, vaultProvider.Version, recipeParser.OicdbVersion)
		if err != nil {
			logger.Error("Error sending usage metrics to Buchhalter API", "error", err)
			p.Send(utils.ViewStatusUpdateMsg{
				Err:        fmt.Errorf("error sending usage metrics to Buchhalter API: %w", err),
				ShouldQuit: true,
			})
			return
		}

		p.Send(utils.ViewStatusUpdateMsg{
			Message:    "Sent usage metrics to Buchhalter API",
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

// loadRecipesAndMatchingVaultItems loads all recipes (or only the one for a specific supplier if `supplier` is set)
// and tries to find matching pairs of credentials in the vault.
func loadRecipesAndMatchingVaultItems(logger *slog.Logger, supplier string, vaultProvider *vault.Provider1Password, recipeParser *parser.RecipeParser) ([]recipeToExecute, error) {
	var recipeVaultItemPairs []recipeToExecute

	// Load recipes
	developmentMode := viper.GetBool("dev")
	logger.Info("Loading recipes for suppliers ...", "development_mode", developmentMode)
	loadRecipeResult, err := recipeParser.LoadRecipes(developmentMode)
	if err != nil {
		logger.Error("Error loading recipes for suppliers", "error", err, "load_recipe_result", loadRecipeResult)
		return recipeVaultItemPairs, err
	}

	// Search for credential pairs matching the recipe(s)
	stepCount := 0
	vaultItems := vaultProvider.VaultItems
	if len(supplier) > 0 {
		logger.Info("Search for credentials for suppliers recipe ...", "supplier", supplier)
		for i := range vaultItems {
			// Check if a recipe exists for the item
			recipe := recipeParser.GetRecipeForItem(vaultItems[i], vaultProvider.UrlsByItemId)
			if recipe != nil && supplier == recipe.Supplier {
				recipeVaultItemPairs = append(recipeVaultItemPairs, recipeToExecute{recipe, vaultItems[i].ID})
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
				recipeVaultItemPairs = append(recipeVaultItemPairs, recipeToExecute{recipe, vaultItems[i].ID})
				logger.Info("Search for matching pairs of recipes for supplier recipes and credentials ... found", "supplier", recipe.Supplier, "credentials_id", vaultItems[i].ID)
			}
		}
	}

	return recipeVaultItemPairs, nil
}

func sendMetrics(buchhalterAPIClient *repository.BuchhalterAPIClient, a bool, runData repository.RunData, cliVersion, chromeVersion, vaultVersion, oicdbVersion string) error {
	err := buchhalterAPIClient.SendMetrics(runData, cliVersion, chromeVersion, vaultVersion, oicdbVersion)
	if err != nil {
		return fmt.Errorf("error sending usage metrics to Buchhalter API: %w", err)
	}
	if a {
		viper.Set("buchhalter_always_send_metrics", true)
		err = viper.WriteConfig()
		if err != nil {
			return fmt.Errorf("error writing config file with value buchhalter_always_send_metrics=true: %w", err)
		}
	}

	return nil
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
)

// viewModelSync is the bubbletea application main view model
type viewModelSync struct {
	mode string

	// UI
	actionsCompleted []utils.UIAction
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

	// Recipe runs
	recipeRunData repository.RunData

	// sendMetrics selection
	selectionCursor  int
	selectionChoice  string
	selectionChoices []string
	metricsRecord    *buchhalterMetricsRecord

	// Buchhalter
	buchhalterAPIClient *repository.BuchhalterAPIClient
	logger              *slog.Logger

	// Browser
	browserCtx context.Context
}

// updateBrowserContext is a message type to update the browser context in the bubbletea application.
type updateBrowserContext struct {
	ctx context.Context
}

type newRecipeRunDataRecordMsg struct {
	record repository.RunDataSupplier
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

// initviewModelSync returns the model for the bubbletea application.
func initviewModelSync(logger *slog.Logger, buchhalterAPIClient *repository.BuchhalterAPIClient) viewModelSync {
	const numLastResults = 5

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	m := viewModelSync{
		actionsCompleted: []utils.UIAction{},

		mode:         "sync",
		showProgress: true,
		progress:     progress.New(progress.WithGradient("#9FC131", "#DBF227")),
		spinner:      s,
		results:      make([]viewMsgRecipeDownloadResultMsg, numLastResults),
		hasError:     false,

		// Recipe runs
		recipeRunData: make(repository.RunData, 0),

		// sendMetrics selection
		selectionChoices: []string{"Yes", "No", "Always yes (don't ask again)"},
		metricsRecord:    &buchhalterMetricsRecord{},

		buchhalterAPIClient: buchhalterAPIClient,
		logger:              logger,

		// Browser
		browserCtx: nil,
	}

	return m
}

// Init initializes the bubbletea application.
// Returns an initial command for the application to run.
func (m viewModelSync) Init() tea.Cmd {
	return m.spinner.Tick
}

// Update updates the bubbletea application model.
// Handles incoming events and updates the model accordingly.
func (m viewModelSync) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.logger.Info("Initiating shutdown sequence", "key_hit", msg.String())

			mn := quit(m)
			return mn, tea.Quit

		case "enter":
			// Send the choice on the channel and exit.
			m.selectionChoice = m.selectionChoices[m.selectionCursor]
			m.mode = "sync"
			switch m.selectionChoice {
			case "Yes":
				return m, func() tea.Msg {
					metrics := m.metricsRecord
					err := sendMetrics(m.buchhalterAPIClient, false, m.recipeRunData, metrics.CliVersion, metrics.ChromeVersion, metrics.VaultVersion, metrics.OicdbVersion)
					return utils.ViewStatusUpdateMsg{
						Message:    "Sent usage metrics to Buchhalter API",
						Err:        err,
						Completed:  true,
						ShouldQuit: true,
					}
				}

			case "No":
				return m, func() tea.Msg {
					return utils.ViewStatusUpdateMsg{
						Message:    "No usage metrics sent to Buchhalter API",
						Completed:  true,
						ShouldQuit: true,
					}
				}

			case "Always yes (don't ask again)":
				return m, func() tea.Msg {
					metrics := m.metricsRecord
					err := sendMetrics(m.buchhalterAPIClient, true, m.recipeRunData, metrics.CliVersion, metrics.ChromeVersion, metrics.VaultVersion, metrics.OicdbVersion)
					return utils.ViewStatusUpdateMsg{
						Message:    "Sent usage metrics to Buchhalter API",
						Err:        err,
						Completed:  true,
						ShouldQuit: true,
					}
				}
			}

		case "down", "j":
			m.selectionCursor++
			if m.selectionCursor >= len(m.selectionChoices) {
				m.selectionCursor = 0
			}

		case "up", "k":
			m.selectionCursor--
			if m.selectionCursor < 0 {
				m.selectionCursor = len(m.selectionChoices) - 1
			}
		}

		return m, nil

	case utils.ViewStatusUpdateMsg:
		m.actionInProgress = msg.Message
		m.actionDetails = msg.Details

		if msg.Completed && msg.Err == nil {
			m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
				Message: msg.Message,
				Style:   utils.UIActionStyleSuccess,
			})
			m.actionInProgress = ""
		}

		if msg.Completed && msg.Err != nil && !msg.ShouldQuit {
			m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
				Message: msg.Err.Error(),
				Style:   utils.UIActionStyleError,
			})
		} else if msg.Err != nil {
			m.actionError = msg.Err.Error()
		}

		if msg.ShouldQuit {
			return m, func() tea.Msg {
				return viewQuitMsg{}
			}
		}

		return m, nil

	case buchhalterMetricsRecord:
		if len(msg.CliVersion) > 0 {
			m.metricsRecord.CliVersion = msg.CliVersion
		}
		if len(msg.ChromeVersion) > 0 {
			m.metricsRecord.ChromeVersion = msg.ChromeVersion
		}
		if len(msg.OicdbVersion) > 0 {
			m.metricsRecord.OicdbVersion = msg.OicdbVersion
		}
		if len(msg.VaultVersion) > 0 {
			m.metricsRecord.VaultVersion = msg.VaultVersion
		}
		return m, nil

	case newRecipeRunDataRecordMsg:
		m.recipeRunData = append(m.recipeRunData, msg.record)
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
func (m viewModelSync) View() string {
	s := strings.Builder{}
	s.WriteString(fmt.Sprintf(
		"%s\n%s\n%s%s\n%s\n",
		headerStyle(LogoText),
		textStyle("Automatically sync all your incoming invoices from your suppliers. "),
		textStyle("More information at: "),
		textStyleBold("https://buchhalter.ai"),
		textStyleGrayBold(fmt.Sprintf("Using CLI %s", cliVersion)),
	) + "\n")

	for _, actionCompleted := range m.actionsCompleted {
		switch actionCompleted.Style {
		case utils.UIActionStyleSuccess:
			s.WriteString(checkMark.Render() + " " + textStyleBold(actionCompleted.Message) + "\n")
		case utils.UIActionStyleError:
			s.WriteString(errorMark.Render() + " " + errorStyle.Render(capitalizeFirstLetter(actionCompleted.Message)) + "\n")
		case utils.UIActionStyleThanks:
			s.WriteString(thanksMark.Render() + " " + textStyleBold(actionCompleted.Message) + "\n")
		}
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
		s.WriteString(errorMark.Render() + " " + errorStyle.Render(capitalizeFirstLetter(m.actionError)) + "\n")
		if len(m.actionDetails) > 0 {
			s.WriteString(helpStyle.Render("  " + m.actionDetails))
		}
	}

	s.WriteString("\n")

	if len(m.currentAction) > 0 && m.hasError {
		s.WriteString(errorStyle.Render("ERROR: " + m.currentAction))
		s.WriteString(helpStyle.Render("  " + m.details))
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
		for i := 0; i < len(m.selectionChoices); i++ {
			if m.selectionCursor == i {
				s.WriteString("(•) ")
			} else {
				s.WriteString("( ) ")
			}
			s.WriteString(m.selectionChoices[i])
			s.WriteString("\n")
		}
	}

	s.WriteString("\n")

	// Quitting or not?
	if !m.quitting {
		s.WriteString(helpStyle.Render("Press q to exit"))
	}

	return appStyle.Render(s.String())
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second*1, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func quit(m viewModelSync) viewModelSync {
	m.quitting = true
	m.showProgress = false

	if m.hasError {
		m.currentAction = "While running recipes!"

	} else {
		m.actionsCompleted = append(m.actionsCompleted, utils.UIAction{
			Message: "Thanks for using buchhalter.ai!",
			Style:   utils.UIActionStyleThanks,
		})
		m.actionDetails = "HAVE A NICE DAY! 😎"
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
