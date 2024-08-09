package browser

// Client to control a headless browser via selectors.
// Selectors in this context are xpath, css, etc.

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"buchhalter/lib/archive"
	"buchhalter/lib/parser"
	"buchhalter/lib/utils"
	"buchhalter/lib/vault"

	cu "github.com/Davincible/chromedp-undetected"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

type BrowserDriver struct {
	logger          *slog.Logger
	credentials     *vault.Credentials
	documentArchive *archive.DocumentArchive

	buchhalterDocumentsDirectory string

	ChromeVersion string

	// TODO Check if those are needed
	downloadsDirectory string
	documentsDirectory string

	browserCtx         context.Context
	recipeTimeout      time.Duration
	maxFilesDownloaded int

	// downloadedFilesCount is used to count the number of files that have been downloaded in the `downloadAll` step
	downloadedFilesCount int

	// newFilesCount is used to count the number of new files that have been moved to the local storage
	// Incl. a check if we had this document already
	newFilesCount int
}

func NewBrowserDriver(logger *slog.Logger, credentials *vault.Credentials, buchhalterDocumentsDirectory string, documentArchive *archive.DocumentArchive, maxFilesDownloaded int) *BrowserDriver {
	return &BrowserDriver{
		logger:          logger,
		credentials:     credentials,
		documentArchive: documentArchive,

		buchhalterDocumentsDirectory: buchhalterDocumentsDirectory,

		browserCtx:         context.Background(),
		recipeTimeout:      60 * time.Second,
		maxFilesDownloaded: maxFilesDownloaded,
		newFilesCount:      0,
	}
}

func (b *BrowserDriver) RunRecipe(p *tea.Program, totalStepCount int, stepCountInCurrentRecipe int, baseCountStep int, recipe *parser.Recipe) utils.RecipeResult {
	// Init browser
	b.logger.Info("Starting chrome browser driver ...", "recipe", recipe.Supplier, "recipe_version", recipe.Version)

	// Setting chrome flags
	// Docs: https://github.com/GoogleChrome/chrome-launcher/blob/main/docs/chrome-flags-for-tools.md
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("disable-search-engine-choice-screen", true),
		chromedp.Flag("headless", false),
	)

	ctx, cancel, err := cu.New(cu.NewConfig(
		cu.WithContext(b.browserCtx),
		cu.WithChromeFlags(opts...),
		// create a timeout as a safety net to prevent any infinite wait loops
		cu.WithTimeout(600*time.Second),
	))
	if err != nil {
		// TODO Implement error handling
		panic(err)
	}
	defer cancel()

	// get chrome version for metrics
	if b.ChromeVersion == "" {
		err := chromedp.Run(ctx, chromedp.Tasks{
			chromedp.Navigate("chrome://version"),
			chromedp.Text(`#version`, &b.ChromeVersion, chromedp.NodeVisible),
		})
		if err != nil {
			// TODO Implement error handling
			panic(err)
		}
		b.ChromeVersion = strings.TrimSpace(b.ChromeVersion)
	}
	b.logger.Info("Starting chrome browser driver ... completed ", "recipe", recipe.Supplier, "recipe_version", recipe.Version, "chrome_version", b.ChromeVersion)

	// create download directories
	b.downloadsDirectory, b.documentsDirectory, err = utils.InitSupplierDirectories(b.buchhalterDocumentsDirectory, recipe.Supplier)
	if err != nil {
		// TODO Implement error handling
		fmt.Println(err)
	}
	b.logger.Info("Download directories created", "downloads_directory", b.downloadsDirectory, "documents_directory", b.documentsDirectory)

	err = chromedp.Run(ctx, chromedp.Tasks{
		browser.
			SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).
			WithDownloadPath(b.downloadsDirectory).
			WithEventsEnabled(true),
		chromedp.ActionFunc(func(ctx context.Context) error {
			// TODO Implement error handling
			_ = b.waitForLoadEvent(ctx)
			return nil
		}),
	})
	if err != nil {
		// TODO Implement error handling
		panic(err)
	}

	// Disable downloading images for performance reasons
	chromedp.ListenTarget(ctx, b.disableImages(ctx))

	_ = b.enableLifeCycleEvents()

	var cs float64
	n := 1
	var result utils.RecipeResult
	for _, step := range recipe.Steps {
		p.Send(utils.ViewMsgStatusAndDescriptionUpdate{
			Title:       fmt.Sprintf("Downloading invoices from %s (%d/%d):", recipe.Supplier, n, stepCountInCurrentRecipe),
			Description: step.Description,
		})

		stepResultChan := make(chan utils.StepResult, 1)

		// Check if step should be skipped
		if step.When.URL != "" {
			var currentURL string
			if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err != nil {
				// TODO implement better error handling
				b.logger.Error("Failed to get current URL", "error", err.Error())

				// Skipping step
				continue
			}

			// Check if the current URL is not equal to step.When.URL
			if currentURL != step.When.URL {
				go func() {
					stepResultChan <- utils.StepResult{Status: "success"}
				}()
			}
		}

		// Timeout recipe if something goes wrong
		go func() {
			switch action := step.Action; action {
			case "open":
				stepResultChan <- b.stepOpen(ctx, step)
			case "removeElement":
				stepResultChan <- b.stepRemoveElement(ctx, step)
			case "click":
				stepResultChan <- b.stepClick(ctx, step)
			case "type":
				stepResultChan <- b.stepType(ctx, step, b.credentials)
			case "sleep":
				stepResultChan <- b.stepSleep(ctx, step)
			case "waitFor":
				stepResultChan <- b.stepWaitFor(ctx, step)
			case "downloadAll":
				stepResultChan <- b.stepDownloadAll(ctx, step)
			case "transform":
				stepResultChan <- b.stepTransform(step)
			case "move":
				stepResultChan <- b.stepMove(step, b.documentArchive)
			case "runScript":
				stepResultChan <- b.stepRunScript(ctx, step)
			case "runScriptDownloadUrls":
				stepResultChan <- b.stepRunScriptDownloadUrls(ctx, step)
			}
		}()

		select {
		case lastStepResult := <-stepResultChan:
			newDocumentsText := fmt.Sprintf("%d new documents", b.newFilesCount)
			if b.newFilesCount == 1 {
				newDocumentsText = "One new document"
			}
			if b.newFilesCount == 0 {
				newDocumentsText = "No new documents"
			}
			if lastStepResult.Status == "success" {
				result = utils.RecipeResult{
					Status:              "success",
					StatusText:          recipe.Supplier + ": " + newDocumentsText,
					StatusTextFormatted: "- " + textStyleBold(recipe.Supplier) + ": " + newDocumentsText,
					LastStepId:          fmt.Sprintf("%s-%s-%d-%s", recipe.Supplier, recipe.Version, n, step.Action),
					LastStepDescription: step.Description,
					NewFilesCount:       b.newFilesCount,
				}
			} else {
				result = utils.RecipeResult{
					Status:              "error",
					StatusText:          recipe.Supplier + "aborted with error.",
					StatusTextFormatted: "x " + textStyleBold(recipe.Supplier) + " aborted with error.",
					LastStepId:          fmt.Sprintf("%s-%s-%d-%s", recipe.Supplier, recipe.Version, n, step.Action),
					LastStepDescription: step.Description,
					LastErrorMessage:    lastStepResult.Message,
					NewFilesCount:       b.newFilesCount,
				}
				err = utils.TruncateDirectory(b.downloadsDirectory)
				if err != nil {
					// TODO Implement error handling
					fmt.Println(err)
				}
				return result
			}

		case <-time.After(b.recipeTimeout):
			result = utils.RecipeResult{
				Status:              "error",
				StatusText:          recipe.Supplier + " aborted with timeout.",
				StatusTextFormatted: "x " + textStyleBold(recipe.Supplier) + " aborted with timeout.",
				LastStepId:          fmt.Sprintf("%s-%s-%d-%s", recipe.Supplier, recipe.Version, n, step.Action),
				LastStepDescription: step.Description,
				NewFilesCount:       b.newFilesCount,
			}
			err = utils.TruncateDirectory(b.downloadsDirectory)
			if err != nil {
				// TODO Implement error handling
				fmt.Println(err)
			}

			// Imagine we run the `downloadALl` step, we download 2 files and then the recipe times out.
			// It is bad that the recipe timed out, however, we still want to process with the 2 new downloaded documents.
			// Process in this context means to move the files to the documents directory and add them to the document archive.
			// Thats why we don't abort if the recipe timed out in this stage.
			if !(step.Action == "downloadAll" && b.downloadedFilesCount > 0) {
				return result
			}
		}
		cs = (float64(baseCountStep) + float64(n)) / float64(totalStepCount)
		p.Send(utils.ViewMsgProgressUpdate{Percent: cs})
		n++
	}

	err = utils.TruncateDirectory(b.downloadsDirectory)
	if err != nil {
		// TODO Implement error handling
		fmt.Println(err)
	}
	return result
}

func (b *BrowserDriver) Quit() error {
	if b.browserCtx != nil {
		return chromedp.Cancel(b.browserCtx)
	}

	return nil
}

func (b *BrowserDriver) stepOpen(ctx context.Context, step parser.Step) utils.StepResult {
	b.logger.Debug("Executing recipe step", "action", step.Action, "url", step.URL)

	if err := chromedp.Run(ctx,
		// navigate to the page
		chromedp.Navigate(step.URL),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_ = b.waitForLoadEvent(ctx)
			return nil
		}),
	); err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	return utils.StepResult{Status: "success"}
}

func (b *BrowserDriver) stepRemoveElement(ctx context.Context, step parser.Step) utils.StepResult {
	b.logger.Debug("Executing recipe step", "action", step.Action, "selector", step.Selector)

	nodeName := "node" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := chromedp.Run(ctx,
		chromedp.Evaluate("let "+nodeName+" = document.querySelector('"+step.Selector+"'); "+nodeName+".parentNode.removeChild("+nodeName+")", nil),
	); err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	return utils.StepResult{Status: "success"}
}

func (b *BrowserDriver) stepClick(ctx context.Context, step parser.Step) utils.StepResult {
	b.logger.Debug("Executing recipe step", "action", step.Action, "selector", step.Selector)

	opts := []chromedp.QueryOption{
		chromedp.NodeReady,
	}
	opts = b.getSelectorTypeQueryOptions(step.SelectorType, opts)

	if err := chromedp.Run(ctx,
		chromedp.Click(step.Selector, opts...),
	); err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	return utils.StepResult{Status: "success"}
}

func (b *BrowserDriver) stepType(ctx context.Context, step parser.Step, credentials *vault.Credentials) utils.StepResult {
	b.logger.Debug("Executing recipe step", "action", step.Action, "selector", step.Selector, "value", step.Value)

	step.Value = b.parseCredentialPlaceholders(step.Value, credentials)

	opts := []chromedp.QueryOption{
		chromedp.NodeReady,
	}
	opts = b.getSelectorTypeQueryOptions(step.SelectorType, opts)

	if err := chromedp.Run(ctx,
		chromedp.SendKeys(step.Selector, step.Value, opts...),
	); err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	return utils.StepResult{Status: "success"}
}

func (b *BrowserDriver) stepSleep(ctx context.Context, step parser.Step) utils.StepResult {
	b.logger.Debug("Executing recipe step", "action", step.Action, "length", step.Value)

	seconds, _ := strconv.Atoi(step.Value)
	if err := chromedp.Run(ctx,
		chromedp.Sleep(time.Duration(seconds)*time.Second),
	); err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	return utils.StepResult{Status: "success"}
}

func (b *BrowserDriver) stepWaitFor(ctx context.Context, step parser.Step) utils.StepResult {
	b.logger.Debug("Executing recipe step", "action", step.Action, "selector", step.Selector)

	opts := []chromedp.QueryOption{}
	opts = b.getSelectorTypeQueryOptions(step.SelectorType, opts)
	if err := chromedp.Run(ctx,
		chromedp.WaitReady(step.Selector, opts...),
	); err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	return utils.StepResult{Status: "success"}
}

func (b *BrowserDriver) stepDownloadAll(ctx context.Context, step parser.Step) utils.StepResult {
	b.logger.Debug("Executing recipe step", "action", step.Action, "selector", step.Selector, "buchhalter_max_download_files_per_receipt", b.maxFilesDownloaded)

	opts := []chromedp.QueryOption{}
	opts = b.getSelectorTypeQueryOptions(step.SelectorType, opts)
	var nodes []*cdp.Node
	err := chromedp.Run(ctx, chromedp.Tasks{
		chromedp.WaitReady(step.Selector, opts...),
		chromedp.Nodes(step.Selector, &nodes),
	})
	if err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}

	b.downloadedFilesCount = 0

	// Limit nodes to 2 to prevent too many downloads at once/rate limiting
	concurrentDownloadsPool := make(chan struct{}, 2)
	wg := &sync.WaitGroup{}
	chromedp.ListenTarget(ctx, func(v interface{}) {
		switch ev := v.(type) {
		case *browser.EventDownloadWillBegin:
			b.logger.Debug("Executing recipe step ... download begins", "action", step.Action, "guid", ev.GUID, "url", ev.URL)
		case *browser.EventDownloadProgress:
			switch ev.State {
			case browser.DownloadProgressStateCompleted:
				b.logger.Debug("Executing recipe step ... download completed", "action", step.Action, "guid", ev.GUID, "received_bytes", ev.ReceivedBytes)
				b.downloadedFilesCount++
				<-concurrentDownloadsPool
				wg.Done()
			case browser.DownloadProgressStateCanceled:
				b.logger.Debug("Executing recipe step ... download cancelled", "action", step.Action, "guid", ev.GUID, "received_bytes", ev.ReceivedBytes)
				<-concurrentDownloadsPool
				wg.Done()
			}
		}
	})

	// Click on download link (for client-side js stuff)
	x := 0
	sleepTime := 1500 * time.Millisecond
	if step.SleepDuration > 0 {
		sleepTime = time.Duration(step.SleepDuration) * time.Millisecond
	}
	for _, n := range nodes {
		// Only download maxFilesDownloaded files
		if b.maxFilesDownloaded > 0 && x >= b.maxFilesDownloaded {
			b.logger.Debug("Breaking download loop, because max_files_downloaded is reached", "action", step.Action, "max_files_downloaded", b.maxFilesDownloaded, "loop", x)
			break
		}

		b.logger.Debug("Executing recipe step ... trigger download click", "action", step.Action, "selector", n.FullXPath()+step.Value, "loop", x, "max_files_downloaded", b.maxFilesDownloaded, "len(nodes)", len(nodes))
		wg.Add(1)
		concurrentDownloadsPool <- struct{}{}
		if err := chromedp.Run(ctx, fetch.Enable(), chromedp.Tasks{
			chromedp.MouseClickNode(n),
		}); err != nil {
			// If we get an "Node does not have a layout object (-32000)" error here,
			// this could mean that the node selector is not good enough.
			// Standard selectors do a text search, which might hit more nodes than we need (or elements that are not a node at all)
			// Possible solutions:
			// - Use a more specific selector
			// - Use a different selector type
			// See https://pkg.go.dev/github.com/chromedp/chromedp#hdr-Query_Options for more information
			return utils.StepResult{Status: "error", Message: err.Error()}
		}

		if step.Value != "" {
			if err := chromedp.Run(ctx, fetch.Enable(), chromedp.Tasks{
				chromedp.WaitVisible(n.FullXPath() + step.Value),
				chromedp.Click(n.FullXPath() + step.Value),
			}); err != nil {
				return utils.StepResult{Status: "error", Message: err.Error()}
			}
		}

		// Delay clicks to prevent too many downloads at once/rate limiting
		b.logger.Debug("Executing recipe step ... sleeping a bit before we trigger the next download", "action", step.Action, "loop", x)
		time.Sleep(sleepTime)
		x++
	}
	b.logger.Debug("Executing recipe step ... waiting for downloads to complete", "action", step.Action)
	wg.Wait()
	close(concurrentDownloadsPool)

	b.logger.Debug("Executing recipe step ... downloads completed", "action", step.Action)
	b.logger.Info("All downloads completed")

	return utils.StepResult{Status: "success"}
}

func (b *BrowserDriver) stepTransform(step parser.Step) utils.StepResult {
	b.logger.Debug("Executing recipe step", "action", step.Action, "value", step.Value)

	switch step.Value {
	case "unzip":
		zipFiles, err := utils.FindFiles(b.downloadsDirectory, ".zip")
		if err != nil {
			// TODO improve error handling
			fmt.Println(err)
		}
		for _, s := range zipFiles {
			b.logger.Debug("Executing recipe step ... unzipping file", "action", step.Action, "source", s, "destination", b.downloadsDirectory)
			b.logger.Info("Unzipping file", "source", s, "destination", b.downloadsDirectory)
			err := utils.UnzipFile(s, b.downloadsDirectory)
			if err != nil {
				return utils.StepResult{Status: "error", Message: err.Error()}
			}
		}
	}

	return utils.StepResult{Status: "success"}
}

func (b *BrowserDriver) stepMove(step parser.Step, documentArchive *archive.DocumentArchive) utils.StepResult {
	b.logger.Debug("Executing recipe step", "action", step.Action, "value", step.Value)

	b.newFilesCount = 0
	err := filepath.WalkDir(b.downloadsDirectory, func(s string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		b.logger.Debug("Matching filenames", "action", step.Action, "value", step.Value, "filename", d.Name())
		match, e := regexp.MatchString(step.Value, d.Name())
		if e != nil {
			return e
		}
		if match {
			srcFile := filepath.Join(b.downloadsDirectory, d.Name())
			// Check if file already exists
			if !documentArchive.FileExists(srcFile) {
				b.logger.Debug("Executing recipe step ... moving file", "action", step.Action, "source", srcFile, "destination", filepath.Join(b.documentsDirectory, d.Name()))
				b.logger.Info("Moving file", "source", srcFile, "destination", filepath.Join(b.documentsDirectory, d.Name()))
				b.newFilesCount++
				dstFile := filepath.Join(b.documentsDirectory, d.Name())
				_, err := utils.CopyFile(srcFile, dstFile)
				if err != nil {
					return err
				}
				err = documentArchive.AddFile(dstFile)
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}

	return utils.StepResult{Status: "success"}
}

func (b *BrowserDriver) stepRunScript(ctx context.Context, step parser.Step) utils.StepResult {
	b.logger.Debug("Executing recipe step", "action", step.Action, "value", step.Value)

	var res []string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(step.Value, &res),
	); err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	return utils.StepResult{Status: "success"}
}

func (b *BrowserDriver) stepRunScriptDownloadUrls(ctx context.Context, step parser.Step) utils.StepResult {
	b.logger.Debug("Executing recipe step", "action", step.Action, "value", step.Value)

	var res []string
	chromedp.Evaluate(`Object.values(`+step.Value+`);`, &res)
	for _, url := range res {
		b.logger.Debug("Executing recipe step ... download", "action", step.Action, "url", url)
		if err := chromedp.Run(ctx,
			browser.
				SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllowAndName).
				WithDownloadPath(b.downloadsDirectory).
				WithEventsEnabled(true),
			chromedp.Navigate(url),
			chromedp.ActionFunc(func(ctx context.Context) error {
				_ = b.waitForLoadEvent(ctx)
				return nil
			}),
		); err != nil {
			return utils.StepResult{Status: "error", Message: err.Error()}
		}
	}

	return utils.StepResult{Status: "success"}
}

func (b *BrowserDriver) parseCredentialPlaceholders(value string, credentials *vault.Credentials) string {
	value = strings.Replace(value, "{{ username }}", credentials.Username, -1)
	value = strings.Replace(value, "{{ password }}", credentials.Password, -1)
	value = strings.Replace(value, "{{ totp }}", credentials.Totp, -1)
	return value
}

func (b *BrowserDriver) disableImages(ctx context.Context) func(event interface{}) {
	return func(event interface{}) {
		switch ev := event.(type) {
		case *fetch.EventRequestPaused:
			go func() {
				c := chromedp.FromContext(ctx)
				ctx := cdp.WithExecutor(ctx, c.Target)
				if ev.ResourceType == network.ResourceTypeImage {
					err := fetch.FailRequest(ev.RequestID, network.ErrorReasonBlockedByClient).Do(ctx)
					if err != nil {
						b.logger.Debug("Failed to block image request", "error", err.Error())
						return
					}
				} else {
					err := fetch.ContinueRequest(ev.RequestID).Do(ctx)
					if err != nil {
						b.logger.Debug("Failed to continue request", "error", err.Error())
						return
					}
				}
			}()
		}
	}
}

func (b *BrowserDriver) enableLifeCycleEvents() chromedp.ActionFunc {
	return func(ctx context.Context) error {
		err := page.Enable().Do(ctx)
		if err != nil {
			return err
		}
		err = page.SetLifecycleEventsEnabled(true).Do(ctx)
		if err != nil {
			return err
		}
		return nil
	}
}

func (b *BrowserDriver) waitForLoadEvent(ctx context.Context) error {
	ch := make(chan struct{})
	cctx, cancel := context.WithCancel(ctx)

	chromedp.ListenTarget(cctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *page.EventLifecycleEvent:
			if e.Name == "networkIdle" {
				cancel()
				close(ch)
			}
		}
	})

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *BrowserDriver) getSelectorTypeQueryOptions(selectorType string, opts []chromedp.QueryOption) []chromedp.QueryOption {
	switch selectorType {
	case "JSPath":
		opts = append(opts, chromedp.ByJSPath)
	case "Search":
		opts = append(opts, chromedp.BySearch)
	case "Query":
		opts = append(opts, chromedp.ByQuery)
	// Possible future options - Not implemented right now, as they are not needed
	// case "Func":
	// 	opts = append(opts, chromedp.ByFunc)
	case "ID":
		opts = append(opts, chromedp.ByID)
	case "NodeID":
		opts = append(opts, chromedp.ByNodeID)
	case "QueryAll":
		opts = append(opts, chromedp.ByQueryAll)
	}

	return opts
}
