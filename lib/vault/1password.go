package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

const (
	PROVIDER_1PASSWORD = "1password"

	BINARY_NAME_1PASSWORD = "op"
)

type Provider1Password struct {
	binary string
	base   string
	tag    string

	Version    string
	VaultItems Items

	// TODO Check if this is needed
	UrlsByItemId map[string][]string

	logger *slog.Logger
}

func New1PasswordProvider(binary, base, tag string, logger *slog.Logger) (*Provider1Password, error) {
	if logger == nil {
		// Fallback to a default logger if none is provided, though ideally it should always be passed.
		logger = slog.Default()
	}
	p := &Provider1Password{
		base:         base,
		tag:          tag,
		UrlsByItemId: make(map[string][]string),
		logger:       logger,
	}

	binaryPath, err := DetermineBinary(binary)
	if err != nil {
		return p, err
	}

	p.binary = binaryPath
	err = p.initializeVaultversion()

	return p, err
}

func (p *Provider1Password) initializeVaultversion() error {
	// Retrieve CLI version
	// #nosec G204
	cmdArgs := []string{"--version"}
	version, err := exec.Command(p.binary, cmdArgs...).Output()
	if err != nil {
		return ProviderNotInstalledError{
			Code: ProviderNotInstalledErrorCode,
			Cmd:  fmt.Sprintf("%s %s", p.binary, strings.Join(cmdArgs, " ")),
			Err:  err,
		}
	}
	p.Version = strings.TrimSpace(string(version))

	return nil
}

func (p *Provider1Password) LoadVaultItems() (Items, error) {
	// Build item list command
	// #nosec G204
	cmdArgs := p.buildVaultCommandArguments([]string{"item", "list"}, true, true)
	itemListResponse, err := exec.Command(p.binary, cmdArgs...).Output()
	if err != nil {
		return nil, ProviderConnectionError{
			Code: ProviderConnectionErrorCode,
			Cmd:  fmt.Sprintf("%s %s", p.binary, strings.Join(cmdArgs, " ")),
			Err:  err,
		}
	}

	var vaultItems Items
	err = json.Unmarshal(itemListResponse, &vaultItems)
	if err != nil {
		return nil, ProviderResponseParsingError{
			Code: ProviderResponseParsingErrorCode,
			Cmd:  fmt.Sprintf("%s %s", p.binary, strings.Join(cmdArgs, " ")),
			Err:  err,
		}
	}

	// Read in all urls from a vault item and build up urls per item id map
	for n := 0; n < len(vaultItems); n++ {
		var urls []string
		for i := 0; i < len(vaultItems[n].Urls); i++ {
			urls = append(urls, vaultItems[n].Urls[i].Href)
		}
		p.UrlsByItemId[vaultItems[n].ID] = urls
	}

	p.VaultItems = vaultItems

	return vaultItems, nil
}

func (p Provider1Password) GetCredentialsByItemId(itemId string) (*Credentials, error) {
	cmdArgs := p.buildVaultCommandArguments([]string{"item", "get", itemId}, true, false)

	// #nosec G204
	itemGetResponse, err := exec.Command(p.binary, cmdArgs...).Output()
	if err != nil {
		return nil, ProviderNotInstalledError{
			Code: ProviderNotInstalledErrorCode,
			Cmd:  fmt.Sprintf("%s %s", p.binary, strings.Join(cmdArgs, " ")),
			Err:  err,
		}
	}

	var item Item
	err = json.Unmarshal(itemGetResponse, &item)
	if err != nil {
		return nil, ProviderResponseParsingError{
			Code: ProviderResponseParsingErrorCode,
			Cmd:  fmt.Sprintf("%s %s", p.binary, strings.Join(cmdArgs, " ")),
			Err:  err,
		}
	}

	credentials := &Credentials{
		Id:            itemId,
		Username:      getValueByField(item, "username"),
		Password:      getValueByField(item, "password"),
		VaultProvider: p, // Store the provider instance
	}

	return credentials, nil
}

// GetTotpForItem fetches only the TOTP for a given item ID.
func (p Provider1Password) GetTotpForItem(itemId string) (string, error) {
	const totpWindowSeconds = 30
	const minValidityThresholdSeconds = 5
	const waitBufferSeconds = 1 // Wait 1 second into the new window

	now := time.Now()
	currentWindowConsumedSeconds := now.Unix() % totpWindowSeconds
	remainingSecondsInWindow := totpWindowSeconds - currentWindowConsumedSeconds

	if remainingSecondsInWindow < minValidityThresholdSeconds {
		waitDuration := time.Duration(remainingSecondsInWindow+waitBufferSeconds) * time.Second
		p.logger.Info("Current TOTP window is about to expire", "remaining_seconds", remainingSecondsInWindow, "wait_duration", waitDuration.String())
		time.Sleep(waitDuration)
		p.logger.Info("Waited for new TOTP window, proceeding to fetch code.")
	}

	cmdArgs := p.buildVaultCommandArguments([]string{"item", "get", itemId}, true, false)

	// #nosec G204
	itemGetResponse, err := exec.Command(p.binary, cmdArgs...).Output()
	if err != nil {
		return "", ProviderNotInstalledError{
			Code: ProviderNotInstalledErrorCode,
			Cmd:  fmt.Sprintf("%s %s", p.binary, strings.Join(cmdArgs, " ")),
			Err:  err,
		}
	}

	var item Item
	err = json.Unmarshal(itemGetResponse, &item)
	if err != nil {
		return "", ProviderResponseParsingError{
			Code: ProviderResponseParsingErrorCode,
			Cmd:  fmt.Sprintf("%s %s", p.binary, strings.Join(cmdArgs, " ")),
			Err:  err,
		}
	}

	return getValueByField(item, "totp"), nil
}

func (p Provider1Password) buildVaultCommandArguments(baseCmd []string, limitVault, includeTag bool) []string {
	cmdArgs := baseCmd
	if limitVault && len(p.base) > 0 {
		cmdArgs = append(cmdArgs, "--vault", p.base)
	}
	if includeTag && len(p.tag) > 0 {
		cmdArgs = append(cmdArgs, "--tags", p.tag)
	}
	cmdArgs = append(cmdArgs, "--format", "json")

	return cmdArgs
}

func (p *Provider1Password) GetVaults() ([]Vault, error) {
	cmdArgs := p.buildVaultCommandArguments([]string{"vault", "list"}, false, false)
	vaultListResponse, err := exec.Command(p.binary, cmdArgs...).Output()
	if err != nil {
		return nil, ProviderConnectionError{
			Code: ProviderConnectionErrorCode,
			Cmd:  fmt.Sprintf("%s %s", p.binary, strings.Join(cmdArgs, " ")),
			Err:  err,
		}
	}

	var vaultList []Vault
	err = json.Unmarshal(vaultListResponse, &vaultList)
	if err != nil {
		return nil, ProviderResponseParsingError{
			Code: ProviderResponseParsingErrorCode,
			Cmd:  fmt.Sprintf("%s %s", p.binary, strings.Join(cmdArgs, " ")),
			Err:  err,
		}
	}

	return vaultList, nil
}

func (p *Provider1Password) GetHumanReadableErrorMessage(err error) error {
	var readableError error

	// The concrete (developer oriented) error message is available in err
	switch err.(type) {
	case ProviderNotInstalledError:
		readableError = errors.New(`could not find out 1Password cli version. Install 1Password cli, first.
Please read "Get started with 1Password CLI" at https://developer.1password.com/docs/cli/get-started/`)

	case ProviderConnectionError:
		readableError = errors.New(`could not connect to 1Password vault. Open 1Password vault with "eval $(op signin)", first.
Please read "Sign in to 1Password CLI" at https://developer.1password.com/docs/cli/reference/commands/signin/`)

	case ProviderResponseParsingError:
		readableError = errors.New(`could not read response data from 1Password vault`)

	case CommandExecutionError:
		var cmdExecError *CommandExecutionError
		if errors.As(err, &cmdExecError) {
			readableError = fmt.Errorf("an error occurred while executing a command '%s': %w", cmdExecError.Cmd, cmdExecError.Err)
		} else {
			readableError = fmt.Errorf("%w", err)
		}
	}

	return readableError
}
