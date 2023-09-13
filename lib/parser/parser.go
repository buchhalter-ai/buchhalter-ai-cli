package parser

import (
	"buchhalter/lib/vault"
	"encoding/json"
	"fmt"
	"github.com/xeipuuv/gojsonschema"
	"io"
	"os"
	"regexp"
)

var db Database
var RecipeProviderByDomain = make(map[string]string)
var RecipeByProvider = make(map[string]Recipe)

type Database struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Recipes []Recipe `json:"recipes"`
}

type Recipe struct {
	Provider string   `json:"provider"`
	Domains  []string `json:"domains"`
	Version  string   `json:"version"`
	Type     string   `json:"type"`
	Steps    []Step   `json:"steps"`
}

type Step struct {
	Action   string `json:"action"`
	URL      string `json:"url,omitempty"`
	Selector string `json:"selector,omitempty"`
	Value    string `json:"value,omitempty"`
	When     struct {
		URL string `json:"url"`
	} `json:"when,omitempty"`
	FilterUrlsWith string `json:"filterUrlsWith,omitempty"`
	Execute        string `json:"execute,omitempty"`
}

type Urls []string

func ValidateRecipes() bool {
	schemaLoader := gojsonschema.NewReferenceLoader("file://schema/oicdb.schema.json")
	documentLoader := gojsonschema.NewReferenceLoader("file://recipes/db.json")

	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		panic(err.Error())
	}

	if result.Valid() {
		return true
	} else {
		fmt.Printf("The document is not valid. see errors :\n")
		for _, desc := range result.Errors() {
			fmt.Printf("- %s\n", desc)
		}
		return false
	}
	return true
}

func LoadRecipes() bool {
	dbFile, err := os.Open("recipes/db.json")
	// if we os.Open returns an error then handle it
	if err != nil {
		fmt.Println(err)
		return false
	}
	defer dbFile.Close()
	byteValue, _ := io.ReadAll(dbFile)

	json.Unmarshal(byteValue, &db)
	for i := 0; i < len(db.Recipes); i++ {
		for n := 0; n < len(db.Recipes[i].Domains); n++ {
			RecipeProviderByDomain[db.Recipes[i].Domains[n]] = db.Recipes[i].Provider
		}
		RecipeByProvider[db.Recipes[i].Provider] = db.Recipes[i]
	}
	return true
}

func ParseRecipe() string {
	return "test"
}

func GetRecipeForItem(item vault.Item) *Recipe {
	// Build regex pattern with all urls from the vault item
	var pattern string
	for domain, _ := range RecipeProviderByDomain {
		pattern = "^(https?://)?" + regexp.QuoteMeta(domain)

		// Try to match all item urls with a recipe url (e.g. digitalocean login url) */
		for i := 0; i < len(vault.UrlsByItemId[item.ID]); i++ {
			matched, _ := regexp.MatchString(pattern, vault.UrlsByItemId[item.ID][i])
			if matched {
				// Return matching recipe
				recipe := RecipeByProvider[RecipeProviderByDomain[domain]]
				return &recipe
			}
		}
	}
	return nil
}
