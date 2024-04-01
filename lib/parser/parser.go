package parser

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"buchhalter/lib/vault"

	"github.com/spf13/viper"
	"github.com/xeipuuv/gojsonschema"
)

var OicdbVersion string
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
	Action      string `json:"action"`
	URL         string `json:"url,omitempty"`
	Selector    string `json:"selector,omitempty"`
	Value       string `json:"value,omitempty"`
	Description string `json:"description,omitempty"`
	When        struct {
		URL string `json:"url"`
	} `json:"when,omitempty"`
	Oauth2 struct {
		AuthUrl            string `json:"authUrl"`
		TokenUrl           string `json:"tokenUrl"`
		RedirectUrl        string `json:"redirectUrl"`
		ClientId           string `json:"clientId"`
		Scope              string `json:"scope"`
		PkceMethod         string `json:"pkceMethod"`
		PkceVerifierLength int    `json:"pkceVerifierLength"`
	}
	ExtractDocumentIds       string            `json:"extractDocumentIds,omitempty"`
	ExtractDocumentFilenames string            `json:"extractDocumentFilenames,omitempty"`
	DocumentUrl              string            `json:"documentUrl,omitempty"`
	DocumentRequestMethod    string            `json:"documentRequestMethod,omitempty"`
	DocumentRequestHeaders   map[string]string `json:"documentRequestHeaders,omitempty"`
	Body                     string            `json:"body,omitempty"`
	Headers                  map[string]string `json:"headers,omitempty"`
	FilterUrlsWith           string            `json:"filterUrlsWith,omitempty"`
	Execute                  string            `json:"execute,omitempty"`
}

type Urls []string

func ValidateRecipes() (bool, error) {
	schemaLoader := gojsonschema.NewReferenceLoader("file://schema/oicdb.schema.json")
	documentLoader := gojsonschema.NewReferenceLoader("file://" + filepath.Join(viper.GetString("buchhalter_config_directory"), "oicdb.json"))

	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		return false, err
	}

	if result.Valid() {
		return true, nil
	}

	fmt.Printf("The document is not valid. see errors :\n")
	for _, desc := range result.Errors() {
		fmt.Printf("- %s\n", desc)
	}
	return false, nil
}

func LoadRecipes() (bool, error) {
	validationResult, err := ValidateRecipes()
	if err != nil {
		return validationResult, err
	}

	dbFile, err := os.Open(filepath.Join(viper.GetString("buchhalter_config_directory"), "oicdb.json"))
	if err != nil {
		return false, err
	}
	defer dbFile.Close()
	byteValue, _ := io.ReadAll(dbFile)

	err = json.Unmarshal(byteValue, &db)
	if err != nil {
		return false, err
	}
	OicdbVersion = db.Version

	/** Create local recipes directory if not exists */
	if viper.GetBool("dev") {
		OicdbVersion = OicdbVersion + "-dev"
		err = loadLocalRecipes()
		if err != nil {
			return false, err
		}
	}

	for i := 0; i < len(db.Recipes); i++ {
		for n := 0; n < len(db.Recipes[i].Domains); n++ {
			RecipeProviderByDomain[db.Recipes[i].Domains[n]] = db.Recipes[i].Provider
		}
		RecipeByProvider[db.Recipes[i].Provider] = db.Recipes[i]
	}

	return true, nil
}

func loadLocalRecipes() error {
	sf := "_local/recipes"
	bd := viper.GetString("buchhalter_directory")
	recipesDir := filepath.Join(bd, sf)
	if _, err := os.Stat(recipesDir); os.IsNotExist(err) {
		err := os.Mkdir(recipesDir, 0755)
		if err != nil {
			return err
		}
	}

	/** Load local recipes */
	files, err := os.ReadDir(recipesDir) // Replace ioutil.ReadDir with os.ReadDir
	if err != nil {
		return err
	}

	for _, file := range files {
		filename := file.Name()
		extension := filepath.Ext(filename)
		filenameWithoutExtension := filename[0 : len(filename)-len(extension)]
		recipeFile, err := os.Open(filepath.Join(bd, sf, filename))
		if err != nil {
			return err
		}
		defer recipeFile.Close()
		byteValue, err := io.ReadAll(recipeFile)
		if err != nil {
			return err
		}
		n := getRecipeIndexByProvider(filenameWithoutExtension)
		if n >= 0 {
			/** Replace recipe if exists */
			var newRecipe Recipe
			err = json.Unmarshal(byteValue, &newRecipe)
			if err != nil {
				return err
			}
			db.Recipes[n] = newRecipe
		} else {
			/** Add recipe if not exists */
			var recipe Recipe
			err = json.Unmarshal(byteValue, &recipe)
			if err != nil {
				return err
			}
			db.Recipes = append(db.Recipes, recipe)
		}
	}

	return nil
}

func getRecipeIndexByProvider(provider string) int {
	for i := 0; i < len(db.Recipes); i++ {
		if db.Recipes[i].Provider == provider {
			return i
		}
	}

	return -1
}

func GetRecipeForItem(item vault.Item, urlsByItemId map[string][]string) *Recipe {
	// Build regex pattern with all urls from the vault item
	var pattern string
	for domain := range RecipeProviderByDomain {
		pattern = "^(https?://)?" + regexp.QuoteMeta(domain)

		// Try to match all item urls with a recipe url (e.g. digitalocean login url) */
		for i := 0; i < len(urlsByItemId[item.ID]); i++ {
			matched, _ := regexp.MatchString(pattern, urlsByItemId[item.ID][i])
			if matched {
				// Return matching recipe
				recipe := RecipeByProvider[RecipeProviderByDomain[domain]]
				return &recipe
			}
		}
	}

	return nil
}
