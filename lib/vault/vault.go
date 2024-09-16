package vault

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func GetProvider(provider, binary, base, tag string) (*Provider1Password, error) {
	switch provider {
	case PROVIDER_1PASSWORD:
		return New1PasswordProvider(binary, base, tag)
	}

	return nil, fmt.Errorf("provider %s not supported", provider)
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
			return "", ProviderNotInstalledError{
				Code: ProviderNotInstalledErrorCode,
				Cmd:  fullBinaryPath,
				Err:  err,
			}
		}

		if _, err := os.Stat(fullBinaryPath); errors.Is(err, os.ErrNotExist) {
			return "", ProviderNotInstalledError{
				Code: ProviderNotInstalledErrorCode,
				Cmd:  fullBinaryPath,
				Err:  err,
			}
		}

		// At this point in time, we assume that the binary is executable

		return fullBinaryPath, nil
	}

	// Find binary
	// TODO Check if this works on Windows or if we need to limit it to Linux and macOS
	whichOutput, err := exec.Command("which", BINARY_NAME_1PASSWORD).Output()
	if err != nil {
		return "", CommandExecutionError{
			Code: CommandExecutionErrorCode,
			Cmd:  fmt.Sprintf("which %s", BINARY_NAME_1PASSWORD),
			Err:  err,
		}
	}

	foundBinary := strings.TrimSpace(string(whichOutput))
	if len(foundBinary) == 0 {
		return "", ProviderNotInstalledError{
			Code: ProviderNotInstalledErrorCode,
			Cmd:  BINARY_NAME_1PASSWORD,
			Err:  fmt.Errorf("could not find executable \"%s\"", BINARY_NAME_1PASSWORD),
		}
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
