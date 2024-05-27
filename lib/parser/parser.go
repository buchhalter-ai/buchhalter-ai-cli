package parser

import (
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

	"buchhalter/lib/vault"

	"github.com/xeipuuv/gojsonschema"
)

type RecipeParser struct {
	logger *slog.Logger

	configDirectory  string
	storageDirectory string

	recipeSupplierByDomain map[string]string
	recipeBySupplier       map[string]Recipe

	database     Database
	OicdbVersion string
}

type Database struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Recipes []Recipe `json:"recipes"`
}

type Recipe struct {
	// TODO Rename Prodiver to Supplier
	Supplier string   `json:"supplier"`
	Domains  []string `json:"domains"`
	Version  string   `json:"version"`
	Type     string   `json:"type"`
	Steps    []Step   `json:"steps"`
}

type Step struct {
	Action       string `json:"action"`
	URL          string `json:"url,omitempty"`
	Selector     string `json:"selector,omitempty"`
	SelectorType string `json:"selectorType,omitempty"`
	Value        string `json:"value,omitempty"`
	Description  string `json:"description,omitempty"`
	When         struct {
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
	Execute                  string            `json:"execute,omitempty"`
}

func NewRecipeParser(logger *slog.Logger, buchhalterConfigDirectory, buchhalterDirectory string) *RecipeParser {
	return &RecipeParser{
		logger:           logger,
		configDirectory:  buchhalterConfigDirectory,
		storageDirectory: buchhalterDirectory,

		recipeSupplierByDomain: make(map[string]string),
		recipeBySupplier:       make(map[string]Recipe),
		database:               Database{},
	}
}

func (p *RecipeParser) LoadRecipes(developmentMode bool) (bool, error) {
	validationResult, err := validateRecipes(p.configDirectory)
	if err != nil {
		return validationResult, err
	}

	dbFile, err := os.Open(filepath.Join(p.configDirectory, "oicdb.json"))
	if err != nil {
		return false, err
	}
	defer dbFile.Close()
	byteValue, _ := io.ReadAll(dbFile)

	err = json.Unmarshal(byteValue, &p.database)
	if err != nil {
		return false, err
	}
	p.OicdbVersion = p.database.Version
	p.logger.Info("Loaded official recipes for suppliers", "num_recipes", len(p.database.Recipes), "oicdb_version", p.OicdbVersion)

	// Create local recipes directory if not exists
	if developmentMode {
		p.logger.Info("Loading local recipes for suppliers ...", "development_mode", developmentMode)
		numOfficialRecipes := len(p.database.Recipes)
		p.OicdbVersion = p.OicdbVersion + "-dev"
		err = p.loadLocalRecipes(p.storageDirectory)
		if err != nil {
			return false, err
		}

		p.logger.Info("Loaded local recipes for suppliers", "num_recipes", len(p.database.Recipes)-numOfficialRecipes, "oicdb_version", p.OicdbVersion)
	}

	for i := 0; i < len(p.database.Recipes); i++ {
		for n := 0; n < len(p.database.Recipes[i].Domains); n++ {
			p.recipeSupplierByDomain[p.database.Recipes[i].Domains[n]] = p.database.Recipes[i].Supplier
		}
		p.recipeBySupplier[p.database.Recipes[i].Supplier] = p.database.Recipes[i]
	}

	return true, nil
}

func (p *RecipeParser) GetRecipeForItem(item vault.Item, urlsByItemId map[string][]string) *Recipe {
	// Build regex pattern with all urls from the vault item
	var pattern string
	for domain := range p.recipeSupplierByDomain {
		pattern = "^(https?://)?" + regexp.QuoteMeta(domain)

		// Try to match all item urls with a recipe url (e.g. digitalocean login url) */
		for i := 0; i < len(urlsByItemId[item.ID]); i++ {
			matched, _ := regexp.MatchString(pattern, urlsByItemId[item.ID][i])
			if matched {
				// Return matching recipe
				recipe := p.recipeBySupplier[p.recipeSupplierByDomain[domain]]
				return &recipe
			}
		}
	}

	return nil
}

func validateRecipes(buchhalterConfigDirectory string) (bool, error) {
	schemaLoader := gojsonschema.NewReferenceLoader("file://" + filepath.Join(buchhalterConfigDirectory, "oicdb.schema.json"))
	documentLoader := gojsonschema.NewReferenceLoader("file://" + filepath.Join(buchhalterConfigDirectory, "oicdb.json"))

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

func (p *RecipeParser) loadLocalRecipes(buchhalterDirectory string) error {
	sf := "_local/recipes"
	recipesDir := filepath.Join(buchhalterDirectory, sf)
	if _, err := os.Stat(recipesDir); os.IsNotExist(err) {
		err := os.MkdirAll(recipesDir, 0755)
		if err != nil {
			return err
		}
	}

	// Load local recipes
	files, err := os.ReadDir(recipesDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		filename := file.Name()
		extension := filepath.Ext(filename)
		filenameWithoutExtension := filename[0 : len(filename)-len(extension)]
		recipeFile, err := os.Open(filepath.Join(buchhalterDirectory, sf, filename))
		if err != nil {
			return err
		}
		defer recipeFile.Close()
		byteValue, err := io.ReadAll(recipeFile)
		if err != nil {
			return err
		}
		n := p.getRecipeIndexBySupplier(filenameWithoutExtension)
		if n >= 0 {
			// Replace recipe if exists
			var newRecipe Recipe
			err = json.Unmarshal(byteValue, &newRecipe)
			if err != nil {
				return err
			}
			p.database.Recipes[n] = newRecipe
			p.logger.Info("Replaced official recipe with local recipes for suppliers", "supplier", newRecipe.Supplier)

		} else {
			// Add recipe if not exists
			var recipe Recipe
			err = json.Unmarshal(byteValue, &recipe)
			if err != nil {
				return err
			}
			p.database.Recipes = append(p.database.Recipes, recipe)
			p.logger.Info("Found and loaded local recipes for supplier", "supplier", recipe.Supplier)
		}
	}

	return nil
}

func (p *RecipeParser) getRecipeIndexBySupplier(supplier string) int {
	for i := 0; i < len(p.database.Recipes); i++ {
		if p.database.Recipes[i].Supplier == supplier {
			return i
		}
	}

	return -1
}

func (p *RecipeParser) GetChecksumOfLocalOICDB() (string, error) {
	oicdbFile := filepath.Join(p.configDirectory, "oicdb.json")
	p.logger.Info("Calculate checksum of local Open Invoice Collector Database ...", "database", oicdbFile)

	if _, err := os.Stat(oicdbFile); errors.Is(err, os.ErrNotExist) {
		p.logger.Info("Local Open Invoice Collector Database does not exist yet", "database", oicdbFile)
		return "", nil
	}

	f, err := os.Open(oicdbFile)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	checksum := fmt.Sprintf("%x", h.Sum(nil))
	p.logger.Info("Checksum of local Open Invoice Collector Database calculated", "database", oicdbFile, "checksum", checksum)

	return checksum, nil
}

func (p *RecipeParser) GetChecksumOfLocalOICDBSchema() (string, error) {
	oicdbFile := filepath.Join(p.configDirectory, "oicdb.schema.json")
	p.logger.Info("Calculate checksum of local Open Invoice Collector Database Schema ...", "schema", oicdbFile)

	if _, err := os.Stat(oicdbFile); errors.Is(err, os.ErrNotExist) {
		p.logger.Info("Local Open Invoice Collector Database Schema does not exist yet", "schema", oicdbFile)
		return "", nil
	}

	f, err := os.Open(oicdbFile)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	checksum := fmt.Sprintf("%x", h.Sum(nil))
	p.logger.Info("Checksum of local Open Invoice Collector Database Schema calculated", "schema", oicdbFile, "checksum", checksum)

	return checksum, nil
}
