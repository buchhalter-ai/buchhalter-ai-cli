package browser

import (
	"buchhalter/lib/parser"
	"buchhalter/lib/vault"
	"context"
	"errors"
	cu "github.com/Davincible/chromedp-undetected"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var downloadsDirectory string
var scriptID page.ScriptIdentifier
var browserCtx context.Context
var recipeTimeout = 10 * time.Second

type ResultProgressUpdate struct {
	Percent float64
}

type ResultTitleAndDescriptionUpdate struct {
	Title       string
	Description string
}

type RecipeResult struct {
	Status              string
	StatusText          string
	LastStepId          string
	LastStepDescription string
}

func Init() {

}

func RunRecipe(p *tea.Program, tsc int, scs int, bcs int, recipe *parser.Recipe, itemId string) RecipeResult {
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

	// Set download directory for recipe
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	downloadsDirectory = filepath.Join(wd, ".tmp", "recipes", recipe.Provider)
	// Create directory if not exists
	if _, err := os.Stat(downloadsDirectory); errors.Is(err, os.ErrNotExist) {
		err := os.MkdirAll(downloadsDirectory, os.ModePerm)
		if err != nil {
			log.Println(err)
		}
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
	var result RecipeResult
	for _, step := range recipe.Steps {
		c1 := make(chan bool, 1)
		log.Printf("Downloading invoice from %s - step %d of %d", step.Action, n, scs)
		p.Send(ResultTitleAndDescriptionUpdate{Title: "Downloading invoices from " + recipe.Provider + " (" + strconv.Itoa(n) + "/" + strconv.Itoa(scs) + "):", Description: step.Description})
		/** Timeout recipe if something goes wrong */
		go func() {
			switch action := step.Action; action {
			case "open":
				stepOpen(ctx, step)
			case "click":
				stepClick(ctx, step)
			case "type":
				stepType(ctx, step, credentials)
			case "sleep":
				stepSleep(ctx, step)
			case "waitFor":
				stepWaitFor(ctx, step)
			case "downloadAll":
				stepDownloadAll(ctx, step)
			case "runScript":
				stepRunScript(ctx, step)
			case "runScriptDownloadUrls":
				stepRunScriptDownloadUrls(ctx, step)
			}
			c1 <- true
		}()
		select {
		case _ = <-c1:
			result = RecipeResult{
				Status:              "success",
				StatusText:          "- " + recipe.Provider + " finished successfully.",
				LastStepId:          recipe.Provider + "-" + recipe.Version + "-" + strconv.Itoa(n) + "-" + step.Action,
				LastStepDescription: step.Description,
			}
		case <-time.After(recipeTimeout):
			result = RecipeResult{
				Status:              "error",
				StatusText:          "x " + recipe.Provider + " aborted with timeout.",
				LastStepId:          recipe.Provider + "-" + recipe.Version + "-" + strconv.Itoa(n) + "-" + step.Action,
				LastStepDescription: step.Description,
			}
			return result
		}
		cs = (float64(bcs) + float64(n)) / float64(tsc)
		p.Send(ResultProgressUpdate{Percent: cs})
		n++
	}
	return result
}

func Quit() {
	if browserCtx != nil {
		_ = chromedp.Cancel(browserCtx)
	}
}

func stepOpen(ctx context.Context, step parser.Step) {
	if err := chromedp.Run(ctx,
		// navigate to the page
		chromedp.Navigate(step.URL),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_ = waitForLoadEvent(ctx)
			return nil
		}),
	); err != nil {
		log.Fatal(err)
	}
}

func stepClick(ctx context.Context, step parser.Step) {
	if err := chromedp.Run(ctx,
		chromedp.Click(step.Selector, chromedp.NodeReady),
	); err != nil {
		log.Fatal(err)
	}
}

func stepType(ctx context.Context, step parser.Step, credentials vault.Credentials) {
	step.Value = parseCredentialPlaceholders(step.Value, credentials)

	if err := chromedp.Run(ctx,
		chromedp.SendKeys(step.Selector, step.Value, chromedp.NodeReady),
	); err != nil {
		log.Fatal(err)
	}
}

func stepSleep(ctx context.Context, step parser.Step) {
	seconds, _ := strconv.Atoi(step.Value)
	if err := chromedp.Run(ctx,
		chromedp.Sleep(time.Duration(seconds)*time.Second),
	); err != nil {
		log.Fatal(err)
	}
}

func stepWaitFor(ctx context.Context, step parser.Step) {
	if err := chromedp.Run(ctx,
		chromedp.WaitReady(step.Selector),
	); err != nil {
		log.Fatal(err)
	}
}

func stepDownloadAll(ctx context.Context, step parser.Step) {
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
	wg.Add(len(nodes))
	x := 0
	for _, n := range nodes {
		if x >= 2 {
			break
		}
		log.Println("Download WG add")
		if err := chromedp.Run(ctx, fetch.Enable(), chromedp.Tasks{
			chromedp.MouseClickNode(n),
		}); err != nil {
			log.Fatal(err)
		}
		// Delay clicks to prevent too many downloads at once/rate limiting
		time.Sleep(1500 * time.Millisecond)
		x++
	}
	wg.Wait()
	log.Println("All downloads completed")
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

func stepRunScript(ctx context.Context, step parser.Step) {
	var res []string
	log.Println(`SCRIPT: ` + step.Value)
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(step.Value, &res),
	); err != nil {
		log.Fatal(err)
	} else {
		log.Println(res)
	}
}

func stepRunScriptDownloadUrls(ctx context.Context, step parser.Step) {
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
			log.Fatal(err)
		}
	}
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

func fullScreenshot(urlstr string, quality int, res *[]byte) chromedp.Tasks {
	return chromedp.Tasks{
		chromedp.Navigate(urlstr),
		chromedp.FullScreenshot(res, quality),
	}
}
