package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var VaultVersion string

type Provider1Password struct {
	binary string
	base   string
	tag    string

	Version    string
	VaultItems Items

	// TODO Check if this is needed
	UrlsByItemId map[string][]string
}

func GetProvider(provider, binary, base, tag string) (*Provider1Password, error) {
	switch provider {
	case PROVIDER_1PASSWORD:
		return New1PasswordProvider(binary, base, tag)
	}

	return nil, fmt.Errorf("provider %s not supported", provider)
}

func New1PasswordProvider(binary, base, tag string) (*Provider1Password, error) {
	binaryPath, err := DetermineBinary(binary)
	if err != nil {
		return nil, err
	}

	p := &Provider1Password{
		binary: binaryPath,
		base:   base,
		tag:    tag,

		UrlsByItemId: make(map[string][]string),
	}
	err = p.initializeVaultversion()

	return p, err
}

func (p *Provider1Password) initializeVaultversion() error {
	// Retrieve CLI version
	// #nosec G204
	version, err := exec.Command(p.binary, "--version").Output()
	if err != nil {
		// TODO Create custome error
		// "Could not find out 1Password cli version. Install 1Password cli, first."
		err = fmt.Errorf("error while executing %s --version: %+v", p.binary, err)
		return err
	}
	p.Version = strings.TrimSpace(string(version))

	// TODO Remove global variable VaultVersion
	// Kept for legacy reasons (sendMetrics)
	VaultVersion = p.Version

	return nil
}

func (p *Provider1Password) LoadVaultItems() (Items, error) {
	// Build item list command
	// #nosec G204
	cmdArgs := p.buildVaultCommandArguments([]string{"item", "list"}, true)
	itemListResponse, err := exec.Command(p.binary, cmdArgs...).Output()
	if err != nil {
		// TODO set correct error message
		//  "Could not connect to 1Password vault. Open 1Password vault with `eval $(op signin)`, first."
		err = errors.New("could not connect to password vault")
		return nil, err
	}

	var vaultItems Items
	err = json.Unmarshal(itemListResponse, &vaultItems)
	if err != nil {
		// TODO set correct error message: "Error while reading 1password logins with buchhalter-ai-tag. " + err.Error()
		err := fmt.Errorf("error while reading 1Password items: %+v", err)
		return nil, err
	}

	// Read in all urls from a vault item and build up urls per item id map
	// TODO Check if this is really needed
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
	cmdArgs := p.buildVaultCommandArguments([]string{"item", "get", itemId}, false)

	// #nosec G204
	itemGetResponse, err := exec.Command(p.binary, cmdArgs...).Output()
	if err != nil {
		return nil, err
	}

	var item Item
	err = json.Unmarshal(itemGetResponse, &item)
	if err != nil {
		// TODO set correct error message: Error while reading 1password logins with buchhalter-ai-tag. " + err.Error())
		err = fmt.Errorf("reading 1password login with buchhalter-ai-tag failed: %+v", err)
		return nil, err
	}

	credentials := &Credentials{
		Id:       itemId,
		Username: getValueByField(item, "username"),
		Password: getValueByField(item, "password"),
		Totp:     getValueByField(item, "totp"),
	}

	return credentials, nil
}

func (p Provider1Password) buildVaultCommandArguments(baseCmd []string, includeTag bool) []string {
	cmdArgs := baseCmd
	if len(p.base) > 0 {
		cmdArgs = append(cmdArgs, "--vault", p.base)
	}
	if includeTag && len(p.tag) > 0 {
		cmdArgs = append(cmdArgs, "--tags", p.tag)
	}
	cmdArgs = append(cmdArgs, "--format", "json")

	return cmdArgs
}

// DetermineBinary determines the binary to use for the 1Password CLI.
// If the binaryPath is set, it will check if the binary exists and is executable.
// If the binaryPath is empty, it will try to find the binary using the which command.
func DetermineBinary(binaryPath string) (string, error) {
	var err error
	// Configured binary
	fullBinaryPath := strings.TrimSpace(binaryPath)
	if len(fullBinaryPath) > 0 {
		fullBinaryPath, err = filepath.Abs(binaryPath)
		if err != nil {
			return "", err
		}

		if _, err := os.Stat(fullBinaryPath); errors.Is(err, os.ErrNotExist) {
			err = fmt.Errorf("could not find executable \"%s\"", fullBinaryPath)
			return "", err
		}

		// TODO Check if fullBinaryPath is executable

		return fullBinaryPath, nil
	}

	// Find binary
	// TODO Check if this works on Windows or if we need to limit it to Linux and macOS
	whichOutput, err := exec.Command("which", BINARY_NAME_1PASSWORD).Output()
	if err != nil {
		return "", err
	}

	foundBinary := strings.TrimSpace(string(whichOutput))
	if len(foundBinary) == 0 {
		err = fmt.Errorf("could not find executable \"%s\" for 1Password CLI", BINARY_NAME_1PASSWORD)
		return "", err
	}

	return foundBinary, nil
}

func getValueByField(item Item, fieldName string) string {
	for n := 0; n < len(item.Fields); n++ {
		if item.Fields[n].Type == "OTP" && fieldName == "totp" {
			return item.Fields[n].Totp
		}

		if item.Fields[n].ID == fieldName {
			return item.Fields[n].Value
		}
	}

	return ""
}
