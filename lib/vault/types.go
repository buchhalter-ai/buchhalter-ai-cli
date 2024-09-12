package vault

import (
	"fmt"
	"time"
)

type Items []Item

type Vault struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Item struct {
	ID                    string    `json:"id"`
	Title                 string    `json:"title"`
	Tags                  []string  `json:"tags"`
	Version               int       `json:"version"`
	Vault                 Vault     `json:"vault"`
	Category              string    `json:"category"`
	LastEditedBy          string    `json:"last_edited_by"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
	AdditionalInformation string    `json:"additional_information"`
	Urls                  []struct {
		Label   string `json:"label"`
		Primary bool   `json:"primary,omitempty"`
		Href    string `json:"href"`
	} `json:"urls"`
	Sections []struct {
		ID    string `json:"id"`
		Label string `json:"label,omitempty"`
	} `json:"sections"`
	Fields []struct {
		ID              string  `json:"id"`
		Type            string  `json:"type"`
		Purpose         string  `json:"purpose,omitempty"`
		Label           string  `json:"label"`
		Value           string  `json:"value"`
		Reference       string  `json:"reference"`
		Entropy         float64 `json:"entropy,omitempty"`
		PasswordDetails struct {
			Entropy   int    `json:"entropy"`
			Generated bool   `json:"generated"`
			Strength  string `json:"strength"`
		} `json:"password_details,omitempty"`
		Section struct {
			ID string `json:"id"`
		} `json:"section,omitempty"`
		Totp string `json:"totp,omitempty"`
	} `json:"fields"`
}

type Credentials struct {
	Id       string
	Username string
	Password string
	Totp     string
}

const (
	ProviderNotInstalledErrorCode    int = 9001
	ProviderConnectionErrorCode      int = 9002
	ProviderResponseParsingErrorCode int = 9003
	CommandExecutionErrorCode        int = 9004
)

type ProviderNotInstalledError struct {
	Code int
	Cmd  string
	Err  error
}

func (e ProviderNotInstalledError) Error() string {
	return fmt.Sprintf("Error %d provider could not be found \"%s\": %s", e.Code, e.Cmd, e.Err.Error())
}

type ProviderConnectionError struct {
	Code int
	Cmd  string
	Err  error
}

func (e ProviderConnectionError) Error() string {
	return fmt.Sprintf("Error %d could not connect to password vault \"%s\": %s", e.Code, e.Cmd, e.Err.Error())
}

type ProviderResponseParsingError struct {
	Code int
	Cmd  string
	Err  error
}

func (e ProviderResponseParsingError) Error() string {
	return fmt.Sprintf("Error %d reading password vault response\"%s\": %s", e.Code, e.Cmd, e.Err.Error())
}

type CommandExecutionError struct {
	Code int
	Cmd  string
	Err  error
}

func (e CommandExecutionError) Error() string {
	return fmt.Sprintf("Error %d executing command \"%s\": %s", e.Code, e.Cmd, e.Err.Error())
}
