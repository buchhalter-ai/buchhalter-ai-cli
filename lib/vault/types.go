package vault

import (
	"time"
)

const (
	PROVIDER_1PASSWORD = "1password"

	BINARY_NAME_1PASSWORD = "op"
)

type Items []Item

type Item struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Tags    []string `json:"tags"`
	Version int      `json:"version"`
	Vault   struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"vault"`
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
