package parser

import (
	"buchhalter/lib/vault"
	"encoding/json"
	"fmt"
	"github.com/spf13/viper"
	"github.com/xeipuuv/gojsonschema"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
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

func ValidateRecipes() bool {
	schemaLoader := gojsonschema.NewReferenceLoader("file://schema/oicdb.schema.json")
	documentLoader := gojsonschema.NewReferenceLoader("file://" + filepath.Join(viper.GetString("buchhalter_config_directory"), "oicdb.json"))

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
	ValidateRecipes()
	dbFile, err := os.Open(filepath.Join(viper.GetString("buchhalter_config_directory"), "oicdb.json"))
	if err != nil {
		fmt.Println(err)
		return false
	}
	defer dbFile.Close()
	byteValue, _ := io.ReadAll(dbFile)

	json.Unmarshal(byteValue, &db)
	OicdbVersion = db.Version

	/** Create local recipes directory if not exists */
	if viper.GetBool("dev") == true {
		OicdbVersion = OicdbVersion + "-dev"
		loadLocalRecipes()
	}

	for i := 0; i < len(db.Recipes); i++ {
		for n := 0; n < len(db.Recipes[i].Domains); n++ {
			RecipeProviderByDomain[db.Recipes[i].Domains[n]] = db.Recipes[i].Provider
		}
		RecipeByProvider[db.Recipes[i].Provider] = db.Recipes[i]
	}
	return true
}

func loadLocalRecipes() {
	sf := "_local/recipes"
	bd := viper.GetString("buchhalter_directory")
	recipesDir := filepath.Join(bd, sf)
	if _, err := os.Stat(recipesDir); os.IsNotExist(err) {
		err := os.Mkdir(recipesDir, 0755)
		if err != nil {
			panic(err)
		}
	}

	/** Load local recipes */
	files, err := ioutil.ReadDir(recipesDir)
	if err != nil {
		log.Fatal(err)
	}

	for _, file := range files {
		filename := file.Name()
		extension := filepath.Ext(filename)
		filenameWithoutExtension := filename[0 : len(filename)-len(extension)]
		recipeFile, err := os.Open(filepath.Join(bd, sf, filename))
		if err != nil {
			fmt.Println(err)
		}
		defer recipeFile.Close()
		byteValue, _ := io.ReadAll(recipeFile)
		n := getRecipeIndexByProvider(filenameWithoutExtension)
		if n >= 0 {
			/** Replace recipe if exists */
			var newRecipe Recipe
			err = json.Unmarshal(byteValue, &newRecipe)
			if err != nil {
				log.Fatal(err)
			}
			db.Recipes[n] = newRecipe
		} else {
			/** Add recipe if not exists */
			var recipe Recipe
			json.Unmarshal(byteValue, &recipe)
			db.Recipes = append(db.Recipes, recipe)
		}
	}
}

func getRecipeIndexByProvider(provider string) int {
	for i := 0; i < len(db.Recipes); i++ {
		if db.Recipes[i].Provider == provider {
			return i
		}
	}
	return -1
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
