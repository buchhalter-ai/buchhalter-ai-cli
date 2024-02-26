package browser

import (
	"buchhalter/lib/archive"
	"buchhalter/lib/parser"
	"buchhalter/lib/utils"
	"buchhalter/lib/vault"
	"context"
	cu "github.com/Davincible/chromedp-undetected"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"io/fs"
	"log"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	downloadsDirectory string
	documentsDirectory string
	browserCtx         context.Context
	recipeTimeout      = 60 * time.Second
	textStyleBold      = lipgloss.NewStyle().Bold(true).Render
	ChromeVersion      string
	newFilesCount      = 0
)

func RunRecipe(p *tea.Program, tsc int, scs int, bcs int, recipe *parser.Recipe, itemId string) utils.RecipeResult {
	//Load username, password, totp from vault
	credentials := vault.GetCredentialsByItemId(itemId)

	// New creates a new context for use with chromedp. With this context
	// you can use chromedp as you normally would.
	ctx, cancel, err := cu.New(cu.NewConfig(
		cu.WithContext(browserCtx),
	))
	if err != nil {
		panic(err)
	}
	defer cancel()

	// create a timeout as a safety net to prevent any infinite wait loops
	ctx, cancel = context.WithTimeout(ctx, 600*time.Second)
	defer cancel()

	// create download directories
	downloadsDirectory, documentsDirectory = utils.InitProviderDirectories(recipe.Provider)

	// get chrome version for metrics
	if ChromeVersion == "" {
		chromedp.Run(ctx, chromedp.Tasks{
			chromedp.Navigate("chrome://version"),
			chromedp.Text(`#version`, &ChromeVersion, chromedp.NodeVisible),
		})
		ChromeVersion = strings.TrimSpace(ChromeVersion)
	}

	chromedp.Run(ctx, chromedp.Tasks{
		browser.
			SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).
			WithDownloadPath(downloadsDirectory).
			WithEventsEnabled(true),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_ = waitForLoadEvent(ctx)
			return nil
		}),
	})

	// Disable downloading images for performance reasons
	chromedp.ListenTarget(ctx, DisableImages(ctx))

	enableLifeCycleEvents()

	var cs float64
	n := 1
	var result utils.RecipeResult
	for _, step := range recipe.Steps {
		sr := make(chan utils.StepResult, 1)
		p.Send(utils.ResultTitleAndDescriptionUpdate{Title: "Downloading invoices from " + recipe.Provider + " (" + strconv.Itoa(n) + "/" + strconv.Itoa(scs) + "):", Description: step.Description})
		/** Timeout recipe if something goes wrong */
		go func() {
			switch action := step.Action; action {
			case "open":
				sr <- stepOpen(ctx, step)
			case "click":
				sr <- stepClick(ctx, step)
			case "type":
				sr <- stepType(ctx, step, credentials)
			case "sleep":
				sr <- stepSleep(ctx, step)
			case "waitFor":
				sr <- stepWaitFor(ctx, step)
			case "downloadAll":
				sr <- stepDownloadAll(ctx, step)
			case "transform":
				sr <- stepTransform(ctx, step)
			case "move":
				sr <- stepMove(ctx, step)
			case "runScript":
				sr <- stepRunScript(ctx, step)
			case "runScriptDownloadUrls":
				sr <- stepRunScriptDownloadUrls(ctx, step)
			}
		}()
		select {
		case lsr := <-sr:
			newDocumentsText := strconv.Itoa(newFilesCount) + " new documents"
			if newFilesCount == 1 {
				newDocumentsText = "One new document"
			}
			if newFilesCount == 0 {
				newDocumentsText = "No new documents"
			}
			if lsr.Status == "success" {
				result = utils.RecipeResult{
					Status:              "success",
					StatusText:          recipe.Provider + ": " + newDocumentsText,
					StatusTextFormatted: "- " + textStyleBold(recipe.Provider) + ": " + newDocumentsText,
					LastStepId:          recipe.Provider + "-" + recipe.Version + "-" + strconv.Itoa(n) + "-" + step.Action,
					LastStepDescription: step.Description,
					NewFilesCount:       newFilesCount,
				}
			} else {
				result = utils.RecipeResult{
					Status:              "error",
					StatusText:          recipe.Provider + "aborted with error.",
					StatusTextFormatted: "x " + textStyleBold(recipe.Provider) + " aborted with error.",
					LastStepId:          recipe.Provider + "-" + recipe.Version + "-" + strconv.Itoa(n) + "-" + step.Action,
					LastStepDescription: step.Description,
					LastErrorMessage:    lsr.Message,
					NewFilesCount:       newFilesCount,
				}
				utils.TruncateDirectory(downloadsDirectory)
				return result
			}
		case <-time.After(recipeTimeout):
			result = utils.RecipeResult{
				Status:              "error",
				StatusText:          recipe.Provider + " aborted with timeout.",
				StatusTextFormatted: "x " + textStyleBold(recipe.Provider) + " aborted with timeout.",
				LastStepId:          recipe.Provider + "-" + recipe.Version + "-" + strconv.Itoa(n) + "-" + step.Action,
				LastStepDescription: step.Description,
				NewFilesCount:       newFilesCount,
			}
			utils.TruncateDirectory(downloadsDirectory)
			return result
		}
		cs = (float64(bcs) + float64(n)) / float64(tsc)
		p.Send(utils.ResultProgressUpdate{Percent: cs})
		n++
	}

	utils.TruncateDirectory(downloadsDirectory)
	return result
}

func Quit() {
	if browserCtx != nil {
		_ = chromedp.Cancel(browserCtx)
	}
}

func stepOpen(ctx context.Context, step parser.Step) utils.StepResult {
	if err := chromedp.Run(ctx,
		// navigate to the page
		chromedp.Navigate(step.URL),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_ = waitForLoadEvent(ctx)
			return nil
		}),
	); err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	return utils.StepResult{Status: "success"}
}

func stepClick(ctx context.Context, step parser.Step) utils.StepResult {
	if err := chromedp.Run(ctx,
		chromedp.Click(step.Selector, chromedp.NodeReady),
	); err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	return utils.StepResult{Status: "success"}
}

func stepType(ctx context.Context, step parser.Step, credentials vault.Credentials) utils.StepResult {
	step.Value = parseCredentialPlaceholders(step.Value, credentials)

	if err := chromedp.Run(ctx,
		chromedp.SendKeys(step.Selector, step.Value, chromedp.NodeReady),
	); err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	return utils.StepResult{Status: "success"}
}

func stepSleep(ctx context.Context, step parser.Step) utils.StepResult {
	seconds, _ := strconv.Atoi(step.Value)
	if err := chromedp.Run(ctx,
		chromedp.Sleep(time.Duration(seconds)*time.Second),
	); err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	return utils.StepResult{Status: "success"}
}

func stepWaitFor(ctx context.Context, step parser.Step) utils.StepResult {
	if err := chromedp.Run(ctx,
		chromedp.WaitReady(step.Selector),
	); err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	return utils.StepResult{Status: "success"}
}

func stepDownloadAll(ctx context.Context, step parser.Step) utils.StepResult {
	var nodes []*cdp.Node
	selector := step.Selector
	chromedp.Run(ctx, chromedp.Tasks{
		chromedp.WaitReady(selector),
		chromedp.Nodes(selector, &nodes),
	})

	wg := &sync.WaitGroup{}
	chromedp.ListenTarget(ctx, func(v interface{}) {
		switch ev := v.(type) {
		case *browser.EventDownloadWillBegin:
			log.Printf("Download will begin: %s - %s\n", ev.GUID, ev.URL)
		case *browser.EventDownloadProgress:
			if ev.State == browser.DownloadProgressStateCompleted {
				log.Printf("Download completed: %s\n", ev.GUID)
				go func() {
					wg.Done()
				}()
			}
		}
	})

	// Click on link (for client-side js stuff)
	// Limit nodes to 2 to prevent too many downloads at once/rate limiting
	dl := len(nodes)
	if dl > 2 {
		dl = 2
	}
	wg.Add(dl)
	x := 0
	for _, n := range nodes {
		if x >= 2 {
			break
		}
		log.Println("Download WG add")
		if err := chromedp.Run(ctx, fetch.Enable(), chromedp.Tasks{
			chromedp.MouseClickNode(n),
		}); err != nil {
			return utils.StepResult{Status: "error", Message: err.Error()}
		}
		// Delay clicks to prevent too many downloads at once/rate limiting
		time.Sleep(1500 * time.Millisecond)
		x++
	}
	wg.Wait()

	log.Println("All downloads completed")
	return utils.StepResult{Status: "success"}
}

func stepTransform(ctx context.Context, step parser.Step) utils.StepResult {
	switch step.Value {
	case "unzip":
		for _, s := range utils.FindFiles(downloadsDirectory, ".zip") {
			utils.UnzipFile(s, downloadsDirectory)
		}
	}
	return utils.StepResult{Status: "success"}
}

func stepMove(ctx context.Context, step parser.Step) utils.StepResult {
	var a []string
	newFilesCount = 0
	filepath.WalkDir(downloadsDirectory, func(s string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		match, e := regexp.MatchString(step.Value, d.Name())
		if match {
			srcFile := filepath.Join(downloadsDirectory, d.Name())
			//check if file already exists
			if archive.FileExists(srcFile) == false {
				newFilesCount++
				utils.CopyFile(srcFile, filepath.Join(documentsDirectory, d.Name()))
			}
		}
		return nil
	})
	for _, s := range a {
		utils.UnzipFile(s, downloadsDirectory)
	}
	return utils.StepResult{Status: "success"}
}

func stepRunScript(ctx context.Context, step parser.Step) utils.StepResult {
	var res []string
	log.Println(`SCRIPT: ` + step.Value)
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(step.Value, &res),
	); err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	return utils.StepResult{Status: "success"}
}

func stepRunScriptDownloadUrls(ctx context.Context, step parser.Step) utils.StepResult {
	var res []string
	log.Println(`SCRIPT DOWNLOAD ARRAY: ` + step.Value)
	chromedp.Evaluate(`Object.values(`+step.Value+`);`, &res)
	for _, url := range res {
		log.Println(`DOWNLOAD: ` + url)
		if err := chromedp.Run(ctx,
			browser.
				SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllowAndName).
				WithDownloadPath(downloadsDirectory).
				WithEventsEnabled(true),
			chromedp.Navigate(url),
			chromedp.ActionFunc(func(ctx context.Context) error {
				_ = waitForLoadEvent(ctx)
				return nil
			}),
		); err != nil {
			return utils.StepResult{Status: "error", Message: err.Error()}
		}
	}
	return utils.StepResult{Status: "success"}
}

func parseCredentialPlaceholders(value string, credentials vault.Credentials) string {
	if strings.Contains(value, "{{ username }}") {
		value = strings.Replace(value, "{{ username }}", credentials.Username, -1)
	}
	if strings.Contains(value, "{{ password }}") {
		value = strings.Replace(value, "{{ password }}", credentials.Password, -1)
	}
	if strings.Contains(value, "{{ totp }}") {
		value = strings.Replace(value, "{{ totp }}", credentials.Totp, -1)
	}
	return value
}

func DisableImages(ctx context.Context) func(event interface{}) {
	return func(event interface{}) {
		switch ev := event.(type) {
		case *fetch.EventRequestPaused:
			go func() {
				c := chromedp.FromContext(ctx)
				ctx := cdp.WithExecutor(ctx, c.Target)
				if ev.ResourceType == network.ResourceTypeImage {
					fetch.FailRequest(ev.RequestID, network.ErrorReasonBlockedByClient).Do(ctx)
				} else {
					fetch.ContinueRequest(ev.RequestID).Do(ctx)
				}
			}()
		}
	}
}

func enableLifeCycleEvents() chromedp.ActionFunc {
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

func waitForLoadEvent(ctx context.Context) error {
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
