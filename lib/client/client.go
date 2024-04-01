package client

import (
	"buchhalter/lib/archive"
	"buchhalter/lib/parser"
	"buchhalter/lib/secrets"
	"buchhalter/lib/utils"
	"buchhalter/lib/vault"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	cu "github.com/Davincible/chromedp-undetected"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

var (
	downloadsDirectory       string
	documentsDirectory       string
	recipeTimeout            = 120 * time.Second
	textStyleBold            = lipgloss.NewStyle().Bold(true).Render
	browserCtx               context.Context
	oauth2AuthToken          string
	oauth2AuthUrl            string
	oauth2TokenUrl           string
	oauth2RedirectUrl        string
	oauth2ClientId           string
	oauth2Scope              string
	oauth2PkceMethod         string
	oauth2PkceVerifierLength int
	ChromeVersion            string
	newFilesCount            = 0
)

type HiddenInputFields struct {
	Fields map[string]string
}

func RunRecipe(p *tea.Program, tsc int, scs int, bcs int, recipe *parser.Recipe, credentials *vault.Credentials) utils.RecipeResult {
	//Init directories
	downloadsDirectory, documentsDirectory = utils.InitProviderDirectories(recipe.Provider)

	//Init browser
	ctx, cancel, err := cu.New(cu.NewConfig(
		cu.WithContext(browserCtx),
	))

	if err != nil {
		panic(err)
	}
	defer cancel()

	// get chrome version for metrics
	if ChromeVersion == "" {
		err := chromedp.Run(ctx, chromedp.Tasks{
			chromedp.Navigate("chrome://version"),
			chromedp.Text(`#version`, &ChromeVersion, chromedp.NodeVisible),
		})
		if err != nil {
			log.Fatal(err)
		}
		ChromeVersion = strings.TrimSpace(ChromeVersion)
	}

	var cs float64
	n := 1
	var result utils.RecipeResult
	for _, step := range recipe.Steps {
		sr := make(chan utils.StepResult, 1)
		p.Send(utils.ResultTitleAndDescriptionUpdate{Title: "Downloading invoices from " + recipe.Provider + " (" + strconv.Itoa(n) + "/" + strconv.Itoa(scs) + "):", Description: step.Description})
		/** Timeout recipe if something goes wrong */
		go func() {
			switch step.Action {
			case "oauth2-setup":
				sr <- stepOauth2Setup(step)
			case "oauth2-check-tokens":
				sr <- stepOauth2CheckTokens(ctx, recipe, step, credentials)
			case "oauth2-authenticate":
				sr <- stepOauth2Authenticate(ctx, recipe, step, credentials)
			case "oauth2-post-and-get-items":
				sr <- stepOauth2PostAndGetItems(ctx, step)
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
					StatusText:          recipe.Provider + " aborted with error.",
					StatusTextFormatted: "x " + textStyleBold(recipe.Provider) + " aborted with error.",
					LastStepId:          recipe.Provider + "-" + recipe.Version + "-" + strconv.Itoa(n) + "-" + step.Action,
					LastStepDescription: step.Description,
					LastErrorMessage:    lsr.Message,
					NewFilesCount:       newFilesCount,
				}
				if lsr.Break {
					return result
				}
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
			return result
		}
		cs = (float64(bcs) + float64(n)) / float64(tsc)
		p.Send(utils.ResultProgressUpdate{Percent: cs})
		n++
	}

	return result
}

func stepOauth2Setup(step parser.Step) utils.StepResult {
	oauth2AuthUrl = step.Oauth2.AuthUrl
	oauth2TokenUrl = step.Oauth2.TokenUrl
	oauth2RedirectUrl = step.Oauth2.RedirectUrl
	oauth2ClientId = step.Oauth2.ClientId
	oauth2Scope = step.Oauth2.Scope
	oauth2PkceMethod = step.Oauth2.PkceMethod
	oauth2PkceVerifierLength = step.Oauth2.PkceVerifierLength
	return utils.StepResult{Status: "success", Message: "Successfully set up OAuth2 settings."}
}

func stepOauth2CheckTokens(ctx context.Context, recipe *parser.Recipe, step parser.Step, credentials *vault.Credentials) utils.StepResult {
	// Try to get secrets from cache
	pii := recipe.Provider + "|" + credentials.Id
	tokens, err := secrets.GetOauthAccessTokenFromCache(pii)

	if err == nil {
		if validOauth2AuthToken(tokens) {
			oauth2AuthToken = tokens.AccessToken
			return utils.StepResult{Status: "success", Message: "Found valid oauth2 access token in cache"}
		} else {
			payload := []byte(`{
"grant_type": "refresh_token",
"client_id": "` + oauth2ClientId + `",
"refresh_token": "` + tokens.RefreshToken + `",
"scope": "` + oauth2Scope + `"
}`)
			nt, err := getOauth2Tokens(ctx, payload, step, pii)
			if err == nil {
				oauth2AuthToken = nt.AccessToken
				return utils.StepResult{Status: "error", Message: "Error getting oauth2 access token with refresh token", Break: true}
			}
		}
	}
	return utils.StepResult{Status: "error", Message: "No access token found. New OAuth2 login needed."}
}

func stepOauth2Authenticate(ctx context.Context, recipe *parser.Recipe, step parser.Step, credentials *vault.Credentials) utils.StepResult {
	if len(oauth2AuthToken) > 0 {
		return utils.StepResult{Status: "success"}
	}

	verifier, challenge, _ := utils.Oauth2Pkce(oauth2PkceVerifierLength)
	state := utils.RandomString(20)
	params := url.Values{}
	params.Add("client_id", oauth2ClientId)
	params.Add("prompt", "login")
	params.Add("redirect_uri", oauth2RedirectUrl)
	params.Add("scope", oauth2Scope)
	params.Add("response_type", "code")
	params.Add("state", state)
	params.Add("code_challenge", challenge)
	params.Add("code_challenge_method", oauth2PkceMethod)
	loginUrl := oauth2AuthUrl + "?" + params.Encode()

	var u string
	listenForNetworkEvent(ctx)
	err := chromedp.Run(ctx,
		run(5*time.Second, chromedp.Navigate(loginUrl)),
		chromedp.SendKeys("#form-input-identity", credentials.Username, chromedp.ByID),
		chromedp.Sleep(1*time.Second),
		chromedp.Click("#form-submit-continue", chromedp.ByID),
		chromedp.WaitVisible(`#form-input-credential`, chromedp.ByID),
		chromedp.Sleep(3*time.Second),
		chromedp.SendKeys("#form-input-credential", credentials.Password, chromedp.ByID),
		chromedp.Sleep(2*time.Second),
		chromedp.Click("#form-submit-continue", chromedp.ByID),
		chromedp.Sleep(5*time.Second),
		chromedp.Location(&u),
	)
	if err != nil {
		return utils.StepResult{Status: "error", Message: "error while logging in: " + err.Error()}
	}

	parsedURL, _ := url.Parse(u)
	values := parsedURL.Query()
	code := values.Get("code")

	payload := []byte(`{
"grant_type": "authorization_code",
"client_id": "` + oauth2ClientId + `",
"code_verifier": "` + verifier + `",
"code": "` + code + `",
"redirect_uri": "` + oauth2RedirectUrl + `"
}`)

	pii := recipe.Provider + "|" + credentials.Id
	tokens, err := getOauth2Tokens(ctx, payload, step, pii)
	if err != nil {
		return utils.StepResult{Status: "error", Message: err.Error()}
	}
	oauth2AuthToken = tokens.AccessToken
	return utils.StepResult{Status: "success", Message: "Successfully retrieved OAuth2 tokens."}
}

func stepOauth2PostAndGetItems(ctx context.Context, step parser.Step) utils.StepResult {
	payload := []byte(step.Body)
	req, err := http.NewRequestWithContext(ctx, "POST", step.URL, bytes.NewBuffer(payload))
	if err != nil {
		return utils.StepResult{Status: "error", Message: "error creating post request", Break: true}
	}

	//Set headers
	req.Header.Set("Content-Type", "application/json")
	for n, h := range step.Headers {
		if n == "Authorization" {
			h = strings.Replace(h, "{{ token }}", oauth2AuthToken, -1)
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
		newFilesCount = 0
		var jsr interface{}
		err := json.Unmarshal(body, &jsr)
		if err != nil {
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
				f = filepath.Join(downloadsDirectory, filenames[n])
				filename = filenames[n]
			} else {
				f = filepath.Join(downloadsDirectory, id, ".pdf")
				filename = filepath.Join(id, ".pdf")

			}
			downloadSuccessful := doRequest(ctx, url, step.DocumentRequestMethod, step.DocumentRequestHeaders, f, nil)
			if !downloadSuccessful {
				return utils.StepResult{Status: "error", Message: "Error while downloading invoices"}
			}
			if !archive.FileExists(f) {
				newFilesCount++
				_, err := utils.CopyFile(f, filepath.Join(documentsDirectory, filename))
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

func doRequest(ctx context.Context, url string, method string, headers map[string]string, filename string, payload []byte) bool {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(payload))
	if err != nil {
		return false
	}

	//Set headers
	req.Header.Set("Content-Type", "application/json")
	for n, h := range headers {
		if n == "Authorization" {
			h = strings.Replace(h, "{{ token }}", oauth2AuthToken, -1)
		}
		req.Header.Set(n, h)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		out, err := os.Create(filename)
		if err != nil {
			return false
		}
		defer out.Close()

		_, err = io.Copy(out, resp.Body)
		return err == nil
	}
	return false
}

func getOauth2Tokens(ctx context.Context, payload []byte, step parser.Step, pii string) (secrets.Oauth2Tokens, error) {
	var tj secrets.Oauth2Tokens
	req, err := http.NewRequestWithContext(ctx, "POST", oauth2TokenUrl, bytes.NewBuffer(payload))
	if err != nil {
		return tj, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return tj, errors.New("failed to send oauth2 token request")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return tj, errors.New("error reading oauth2 token response body")
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		err := json.Unmarshal(body, &tj)
		if err != nil {
			return tj, fmt.Errorf("error unmarshalling JSON: %w", err)
		}
		err = secrets.SaveOauth2TokensToFile(pii, tj)
		if err != nil {
			return tj, fmt.Errorf("error storing Oauth2 token ti file: %w", err)
		}
		return tj, nil
	} else if resp.StatusCode == 400 {
		return tj, errors.New("unauthorized error while trying to get oauth2 access token with refresh token")
	}
	return tj, errors.New("unknown error getting oauth2 token")
}

func validOauth2AuthToken(tokens secrets.Oauth2Tokens) bool {
	n := int(time.Now().Unix())
	vu := tokens.CreatedAt + tokens.ExpiresIn
	return vu > n
}

func run(timeout time.Duration, task chromedp.Action) chromedp.ActionFunc {
	return runFunc(timeout, task.Do)
}

func runFunc(timeout time.Duration, task chromedp.ActionFunc) chromedp.ActionFunc {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		return task.Do(ctx)
	}
}

func listenForNetworkEvent(ctx context.Context) {
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

func Quit() {
	if browserCtx != nil {
		_ = chromedp.Cancel(browserCtx)
	}
}
