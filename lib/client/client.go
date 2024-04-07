package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chromedp/cdproto/cdp"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"buchhalter/lib/archive"
	"buchhalter/lib/parser"
	"buchhalter/lib/secrets"
	"buchhalter/lib/utils"
	"buchhalter/lib/vault"

	cu "github.com/Davincible/chromedp-undetected"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

var (
	textStyleBold = lipgloss.NewStyle().Bold(true).Render
)

type HiddenInputFields struct {
	Fields map[string]string
}

type ClientAuthBrowserDriver struct {
	logger          *slog.Logger
	credentials     *vault.Credentials
	documentArchive *archive.DocumentArchive

	buchhalterConfigDirectory string
	buchhalterDirectory       string

	ChromeVersion string

	downloadsDirectory string
	documentsDirectory string

	recipeTimeout time.Duration
	browserCtx    context.Context
	newFilesCount int

	oauth2AuthToken          string
	oauth2AuthUrl            string
	oauth2TokenUrl           string
	oauth2RedirectUrl        string
	oauth2ClientId           string
	oauth2Scope              string
	oauth2PkceMethod         string
	oauth2PkceVerifierLength int
}

func NewClientAuthBrowserDriver(logger *slog.Logger, credentials *vault.Credentials, buchhalterConfigDirectory, buchhalterDirectory string, documentArchive *archive.DocumentArchive) *ClientAuthBrowserDriver {
	return &ClientAuthBrowserDriver{
		logger:          logger,
		credentials:     credentials,
		documentArchive: documentArchive,

		buchhalterConfigDirectory: buchhalterConfigDirectory,
		buchhalterDirectory:       buchhalterDirectory,

		recipeTimeout: 120 * time.Second,
		browserCtx:    context.Background(),
		newFilesCount: 0,
	}
}

func (b *ClientAuthBrowserDriver) RunRecipe(p *tea.Program, tsc int, scs int, bcs int, recipe *parser.Recipe) utils.RecipeResult {
	// Init directories
	var err error
	b.downloadsDirectory, b.documentsDirectory, err = utils.InitProviderDirectories(b.buchhalterDirectory, recipe.Provider)
	if err != nil {
		// TODO Implement error handling
		fmt.Println(err)
	}

	// Init browser
	ctx, cancel, err := cu.New(cu.NewConfig(
		cu.WithContext(b.browserCtx),
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
			log.Fatal(err)
		}
		b.ChromeVersion = strings.TrimSpace(b.ChromeVersion)
	}

	var cs float64
	n := 1
	var result utils.RecipeResult
	for _, step := range recipe.Steps {
		sr := make(chan utils.StepResult, 1)
		p.Send(utils.ResultTitleAndDescriptionUpdate{Title: "Downloading invoices from " + recipe.Provider + " (" + strconv.Itoa(n) + "/" + strconv.Itoa(scs) + "):", Description: step.Description})
		// Timeout recipe if something goes wrong
		go func() {
			switch step.Action {
			case "oauth2-setup":
				sr <- b.stepOauth2Setup(step)
			case "oauth2-check-tokens":
				sr <- b.stepOauth2CheckTokens(ctx, recipe, step, b.credentials, b.buchhalterConfigDirectory)
			case "oauth2-authenticate":
				sr <- b.stepOauth2Authenticate(ctx, recipe, step, b.credentials, b.buchhalterConfigDirectory)
			case "oauth2-post-and-get-items":
				sr <- b.stepOauth2PostAndGetItems(ctx, step, b.documentArchive)
			}
		}()

		select {
		case lsr := <-sr:
			newDocumentsText := strconv.Itoa(b.newFilesCount) + " new documents"
			if b.newFilesCount == 1 {
				newDocumentsText = "One new document"
			}
			if b.newFilesCount == 0 {
				newDocumentsText = "No new documents"
			}
			if lsr.Status == "success" {
				result = utils.RecipeResult{
					Status:              "success",
					StatusText:          recipe.Provider + ": " + newDocumentsText,
					StatusTextFormatted: "- " + textStyleBold(recipe.Provider) + ": " + newDocumentsText,
					LastStepId:          recipe.Provider + "-" + recipe.Version + "-" + strconv.Itoa(n) + "-" + step.Action,
					LastStepDescription: step.Description,
					NewFilesCount:       b.newFilesCount,
				}
			} else {
				result = utils.RecipeResult{
					Status:              "error",
					StatusText:          recipe.Provider + " aborted with error.",
					StatusTextFormatted: "x " + textStyleBold(recipe.Provider) + " aborted with error.",
					LastStepId:          recipe.Provider + "-" + recipe.Version + "-" + strconv.Itoa(n) + "-" + step.Action,
					LastStepDescription: step.Description,
					LastErrorMessage:    lsr.Message,
					NewFilesCount:       b.newFilesCount,
				}
				if lsr.Break {
					return result
				}
			}

		case <-time.After(b.recipeTimeout):
			result = utils.RecipeResult{
				Status:              "error",
				StatusText:          recipe.Provider + " aborted with timeout.",
				StatusTextFormatted: "x " + textStyleBold(recipe.Provider) + " aborted with timeout.",
				LastStepId:          recipe.Provider + "-" + recipe.Version + "-" + strconv.Itoa(n) + "-" + step.Action,
				LastStepDescription: step.Description,
				NewFilesCount:       b.newFilesCount,
			}
			return result
		}

		cs = (float64(bcs) + float64(n)) / float64(tsc)
		p.Send(utils.ResultProgressUpdate{Percent: cs})
		n++
	}

	return result
}

func (b *ClientAuthBrowserDriver) stepOauth2Setup(step parser.Step) utils.StepResult {
	b.oauth2AuthUrl = step.Oauth2.AuthUrl
	b.oauth2TokenUrl = step.Oauth2.TokenUrl
	b.oauth2RedirectUrl = step.Oauth2.RedirectUrl
	b.oauth2ClientId = step.Oauth2.ClientId
	b.oauth2Scope = step.Oauth2.Scope
	b.oauth2PkceMethod = step.Oauth2.PkceMethod
	b.oauth2PkceVerifierLength = step.Oauth2.PkceVerifierLength

	return utils.StepResult{Status: "success", Message: "Successfully set up OAuth2 settings."}
}

func (b *ClientAuthBrowserDriver) stepOauth2CheckTokens(ctx context.Context, recipe *parser.Recipe, step parser.Step, credentials *vault.Credentials, buchhalterConfigDirectory string) utils.StepResult {
	b.logger.Info("Checking OAuth2 tokens ...")
	// Try to get secrets from cache
	pii := recipe.Provider + "|" + credentials.Id
	tokens, err := secrets.GetOauthAccessTokenFromCache(pii, buchhalterConfigDirectory)
	if err == nil {
		if b.validOauth2AuthToken(tokens) {
			b.logger.Info("Found valid oauth2 access token in cache")
			b.oauth2AuthToken = tokens.AccessToken
			return utils.StepResult{Status: "success", Message: "Found valid oauth2 access token in cache"}
		} else {
			b.logger.Info("No valid oauth2 access token found in cache. Trying to get one with refresh token")
			payload := []byte(`{
"grant_type": "refresh_token",
"client_id": "` + b.oauth2ClientId + `",
"refresh_token": "` + tokens.RefreshToken + `",
"scope": "` + b.oauth2Scope + `"
}`)
			nt, err := b.getOauth2Tokens(ctx, payload, pii, buchhalterConfigDirectory)
			if err == nil {
				b.oauth2AuthToken = nt.AccessToken
				b.logger.Error("Error getting oauth2 access token with refresh token")
				return utils.StepResult{Status: "error", Message: "Error getting oauth2 access token with refresh token", Break: true}
			}
		}
	}

	return utils.StepResult{Status: "error", Message: "No access token found. New OAuth2 login needed."}
}

func (b *ClientAuthBrowserDriver) stepOauth2Authenticate(ctx context.Context, recipe *parser.Recipe, step parser.Step, credentials *vault.Credentials, buchhalterConfigDirectory string) utils.StepResult {
	b.logger.Info("Authenticating with OAuth2 ...")
	if len(b.oauth2AuthToken) > 0 {
		return utils.StepResult{Status: "success"}
	}

	verifier, challenge, err := utils.Oauth2Pkce(b.oauth2PkceVerifierLength)
	if err != nil {
		// TODO implement error handling
		fmt.Println(err)
	}

	state := utils.RandomString(20)
	params := url.Values{}
	params.Add("client_id", b.oauth2ClientId)
	params.Add("prompt", "login")
	params.Add("redirect_uri", b.oauth2RedirectUrl)
	params.Add("scope", b.oauth2Scope)
	params.Add("response_type", "code")
	params.Add("state", state)
	params.Add("code_challenge", challenge)
	params.Add("code_challenge_method", b.oauth2PkceMethod)
	loginUrl := b.oauth2AuthUrl + "?" + params.Encode()

	b.listenForNetworkEvent(ctx)
	err = chromedp.Run(ctx,
		b.run(5*time.Second, chromedp.Navigate(loginUrl)),
		chromedp.WaitReady(`#form-input-identity`, chromedp.ByID),
		chromedp.Sleep(1*time.Second),
		chromedp.Click(`#form-input-identity`, chromedp.ByID),
		chromedp.SendKeys("#form-input-identity", credentials.Username, chromedp.ByID),
		chromedp.Sleep(1*time.Second),
		chromedp.Click("#form-submit-continue", chromedp.ByID),
		chromedp.WaitVisible(`#form-input-credential`, chromedp.ByID),
		chromedp.Sleep(3*time.Second),
		chromedp.SendKeys("#form-input-credential", credentials.Password, chromedp.ByID),
		chromedp.Sleep(2*time.Second),
		chromedp.Click("#form-submit-continue", chromedp.ByID),
		chromedp.Sleep(2*time.Second),
	)

	if err != nil {
		b.logger.Error("Error while logging in", "error", err.Error())
		return utils.StepResult{Status: "error", Message: "error while logging in: " + err.Error()}
	}
	
	/** Check for 2FA authentication */
	var faNodes []*cdp.Node
	err = chromedp.Run(ctx,
		b.run(5*time.Second, chromedp.WaitVisible(`#form-input-passcode`, chromedp.ByID)),
		chromedp.Nodes("#form-input-passcode", &faNodes, chromedp.AtLeast(0)),
	)

	if err != nil {
		b.logger.Error("Error while logging in", "error", err.Error())
		return utils.StepResult{Status: "error", Message: "error while logging in: " + err.Error()}
	}

	/** Insert 2FA code */
	if len(faNodes) > 0 {
		err = chromedp.Run(ctx,
			chromedp.SendKeys("#form-input-passcode", credentials.Totp, chromedp.ByID),
			chromedp.Click("#form-submit", chromedp.ByID),
		)
	}

	if err != nil {
		b.logger.Error("Error while logging in", "error", err.Error())
		return utils.StepResult{Status: "error", Message: "error while logging in: " + err.Error()}
	}

	/** Request access token */
	var u string
	err = chromedp.Run(ctx,
		chromedp.Sleep(2*time.Second),
		chromedp.Location(&u),
	)

	if err != nil {
		b.logger.Error("Error while requesting access token", "error", err.Error())
		return utils.StepResult{Status: "error", Message: "error while logging in: " + err.Error()}
	}

	parsedURL, _ := url.Parse(u)
	values := parsedURL.Query()
	code := values.Get("code")

	payload := []byte(`{
"grant_type": "authorization_code",
"client_id": "` + b.oauth2ClientId + `",
"code_verifier": "` + verifier + `",
"code": "` + code + `",
"redirect_uri": "` + b.oauth2RedirectUrl + `"
}`)

	pii := recipe.Provider + "|" + credentials.Id
	tokens, err := b.getOauth2Tokens(ctx, payload, pii, buchhalterConfigDirectory)
	if err != nil {
		b.logger.Error("Error while getting fresh OAuth2 access token", "error", err.Error())
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	b.logger.Info("Successfully retrieved new OAuth2 access tokens.")
	b.oauth2AuthToken = tokens.AccessToken
	return utils.StepResult{Status: "success", Message: "Successfully retrieved OAuth2 tokens."}
}

func (b *ClientAuthBrowserDriver) stepOauth2PostAndGetItems(ctx context.Context, step parser.Step, documentArchive *archive.DocumentArchive) utils.StepResult {
	payload := []byte(step.Body)
	req, err := http.NewRequestWithContext(ctx, "POST", step.URL, bytes.NewBuffer(payload))
	if err != nil {
		return utils.StepResult{Status: "error", Message: "error creating post request", Break: true}
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	for n, h := range step.Headers {
		if n == "Authorization" {
			h = strings.Replace(h, "{{ token }}", b.oauth2AuthToken, -1)
		}
		req.Header.Set(n, h)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return utils.StepResult{Status: "error", Message: "error sending post request: " + err.Error(), Break: true}
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return utils.StepResult{Status: "error", Message: ""}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		b.newFilesCount = 0
		var jsr interface{}
		err := json.Unmarshal(body, &jsr)
		if err != nil {
			// TODO Implement better error handling
			panic(err)
		}

		ids := extractJsonValue(jsr, step.ExtractDocumentIds)
		if len(ids) == 0 {
			return utils.StepResult{Status: "error", Message: "No content ids found", Break: true}
		}

		var filenames []string
		if step.ExtractDocumentFilenames != "" {
			filenames = extractJsonValue(jsr, step.ExtractDocumentFilenames)
		}

		// Get document
		n := 0
		var f string
		var filename string
		for _, id := range ids {
			url := step.DocumentUrl
			url = strings.Replace(url, "{{ id }}", id, -1)
			if len(filenames) > 0 {
				f = filepath.Join(b.downloadsDirectory, filenames[n])
				filename = filenames[n]
			} else {
				f = filepath.Join(b.downloadsDirectory, id, ".pdf")
				filename = filepath.Join(id, ".pdf")

			}
			downloadSuccessful, err := b.doRequest(ctx, url, step.DocumentRequestMethod, step.DocumentRequestHeaders, f, nil)
			if err != nil {
				// TODO implement error handling
				fmt.Println(err)
			}
			if !downloadSuccessful {
				return utils.StepResult{Status: "error", Message: "Error while downloading invoices"}
			}
			if !documentArchive.FileExists(f) {
				b.newFilesCount++
				_, err := utils.CopyFile(f, filepath.Join(b.documentsDirectory, filename))
				if err != nil {
					return utils.StepResult{Status: "error", Message: "Error while copying file: " + err.Error()}
				}
			}
			n++
		}

		return utils.StepResult{Status: "success"}
	} else if resp.StatusCode == 400 {
		return utils.StepResult{Status: "error"}
	}

	return utils.StepResult{Status: "error"}
}

func (b *ClientAuthBrowserDriver) doRequest(ctx context.Context, url string, method string, headers map[string]string, filename string, payload []byte) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(payload))
	if err != nil {
		return false, err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	for n, h := range headers {
		if n == "Authorization" {
			h = strings.Replace(h, "{{ token }}", b.oauth2AuthToken, -1)
		}
		req.Header.Set(n, h)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		out, err := os.Create(filename)
		if err != nil {
			return false, err
		}
		defer out.Close()

		_, err = io.Copy(out, resp.Body)
		return err == nil, err
	}

	return false, nil
}

func (b *ClientAuthBrowserDriver) getOauth2Tokens(ctx context.Context, payload []byte, pii, buchhalterConfigDirectory string) (secrets.Oauth2Tokens, error) {
	var tj secrets.Oauth2Tokens
	req, err := http.NewRequestWithContext(ctx, "POST", b.oauth2TokenUrl, bytes.NewBuffer(payload))
	if err != nil {
		return tj, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return tj, fmt.Errorf("failed to send oauth2 token request: %w", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return tj, fmt.Errorf("error reading oauth2 token response body: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		err := json.Unmarshal(body, &tj)
		if err != nil {
			return tj, fmt.Errorf("error unmarshalling JSON: %w", err)
		}

		err = secrets.SaveOauth2TokensToFile(pii, tj, buchhalterConfigDirectory)
		if err != nil {
			return tj, fmt.Errorf("error storing Oauth2 token ti file: %w", err)
		}

		return tj, nil
	} else if resp.StatusCode == 400 {
		return tj, errors.New("unauthorized error while trying to get oauth2 access token with refresh token")
	}

	return tj, errors.New("unknown error getting oauth2 token")
}

func (b *ClientAuthBrowserDriver) validOauth2AuthToken(tokens secrets.Oauth2Tokens) bool {
	n := int(time.Now().Unix())
	vu := tokens.CreatedAt + tokens.ExpiresIn
	return vu > n
}

func (b *ClientAuthBrowserDriver) run(timeout time.Duration, task chromedp.Action) chromedp.ActionFunc {
	return b.runFunc(timeout, task.Do)
}

func (b *ClientAuthBrowserDriver) runFunc(timeout time.Duration, task chromedp.ActionFunc) chromedp.ActionFunc {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		return task.Do(ctx)
	}
}

func (b *ClientAuthBrowserDriver) listenForNetworkEvent(ctx context.Context) {
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch ev := ev.(type) {

		case *network.EventResponseReceived:
			resp := ev.Response
			if len(resp.Headers) != 0 {
				if resp.Headers["Location"] != nil && resp.Headers["Location"] != "" {
					fmt.Printf("LOCATION: %s", resp.Headers["Location"])
				}
			}
		}
	})
}

/**
 * Extracts a value from a json object by a given path (see extractDocumentIds property in OICDB recipes)
 */
func extractJsonValue(data interface{}, path string) []string {
	keys := strings.Split(path, ".")
	return extractJsonRecursive(data, keys)
}

/**
 * Child method to execute recursive value parsing for a given path provided by dot notation
 */
func extractJsonRecursive(data interface{}, keys []string) []string {
	var results []string

	if len(keys) == 0 {
		switch v := data.(type) {
		case string:
			results = append(results, v)
		case []interface{}:
			for _, item := range v {
				if str, ok := item.(string); ok {
					results = append(results, str)
				}
			}
		}
		return results
	}

	key := keys[0]
	remainingKeys := keys[1:]

	switch v := data.(type) {
	case map[string]interface{}:
		if value, ok := v[key]; ok {
			results = append(results, extractJsonRecursive(value, remainingKeys)...)
		} else {
			// If key doesn't match any in the current map, check all values
			for _, val := range v {
				results = append(results, extractJsonRecursive(val, keys)...)
			}
		}
	case []interface{}:
		for _, item := range v {
			results = append(results, extractJsonRecursive(item, keys)...)
		}
	}

	return results
}

func (b *ClientAuthBrowserDriver) Quit() error {
	if b.browserCtx != nil {
		return chromedp.Cancel(b.browserCtx)
	}

	return nil
}
