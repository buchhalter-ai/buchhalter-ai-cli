package vault

import (
	"encoding/json"
	"fmt"
	"github.com/spf13/viper"
	"os/exec"
	"strings"
	"time"
)

var VaultVersion string
var UrlsByItemId = make(map[string][]string)

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

func LoadVaultItems() (Items, string) {
	errorMessage := ""
	opCliCommand := viper.GetString("one_password_cli_command")
	opBase := viper.GetString("one_password_base")
	opTag := viper.GetString("one_password_tag")

	//Retrive 1password cli version
	version, err := exec.Command("bash", "-c", opCliCommand+" --version").Output()
	if err != nil {
		errorMessage = "Could not find out 1Password cli version. Install 1Password cli, first."
		return nil, errorMessage
	}
	VaultVersion = strings.TrimSpace(string(version))

	out, err := exec.Command("bash", "-c", opCliCommand+" item list --vault="+opBase+" --tags "+opTag+" --format json").Output()
	if err != nil {
		errorMessage = "Could not connect to 1Password vault. Open 1Password vault with `eval $(op signin)`, first."
		return nil, errorMessage
	}
	var vaultItems Items
	err = json.Unmarshal(out, &vaultItems)
	if err != nil {
		errorMessage = "Error while reading 1password logins with buchhalter-ai-tag." + fmt.Sprintf("%s", err.Error())
		return nil, errorMessage
	}

	/** Read in all urls from a vault item and build up urls per item id map */
	for n := 0; n < len(vaultItems); n++ {
		var urls []string
		for i := 0; i < len(vaultItems[n].Urls); i++ {
			urls = append(urls, vaultItems[n].Urls[i].Href)
		}
		UrlsByItemId[vaultItems[n].ID] = urls
	}
	return vaultItems, errorMessage
}

func GetCredentialsByItemId(itemId string) Credentials {
	opCliCommand := viper.GetString("one_password_cli_command")
	opBase := viper.GetString("one_password_base")
	out, err := exec.Command("bash", "-c", opCliCommand+" item get "+itemId+" --vault="+opBase+" --format json").Output()
	var item Item
	if err == nil {
		err = json.Unmarshal(out, &item)
	}
	var credentials Credentials
	credentials.Id = itemId
	credentials.Username = getValueByField(item, "username")
	credentials.Password = getValueByField(item, "password")
	credentials.Totp = getValueByField(item, "totp")
	return credentials
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
