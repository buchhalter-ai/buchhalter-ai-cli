package secrets

import (
	"encoding/json"
	"fmt"
	"github.com/spf13/viper"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

var secretsFilename string = ".secrets.json"

type Oauth2Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	State        string `json:"state"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	CreatedAt    int    `json:"created_at"`
}

type secretFile struct {
	Secrets []secretFileEntry `json:"secrets"`
}

type secretFileEntry struct {
	Id     string                `json:"id"`
	Tokens secretFileEntryTokens `json:"accessTokens"`
}

type secretFileEntryTokens struct {
	AccessTokenEncrypted  string `json:"accessTokenEncrypted"`
	RefreshTokenEncrypted string `json:"refreshTokenEncrypted"`
	TokenType             string `json:"tokenType"`
	State                 string `json:"state"`
	ExpiresIn             int    `json:"expiresIn"`
	CreatedAt             int    `json:"createdAt"`
}

func SaveOauth2TokensToFile(id string, tokens Oauth2Tokens) {
	sfe := readSecretsFile()
	ca := int(time.Now().Unix())
	t := secretFileEntryTokens{
		AccessTokenEncrypted:  tokens.AccessToken,
		RefreshTokenEncrypted: tokens.RefreshToken,
		TokenType:             tokens.TokenType,
		State:                 tokens.State,
		ExpiresIn:             tokens.ExpiresIn,
		CreatedAt:             ca,
	}

	// Update secret
	f := false
	for i, e := range sfe.Secrets {
		if e.Id == id {
			f = true
			sfe.Secrets[i].Tokens = t
		}
	}

	// Add secret
	if !f {
		sfn := secretFileEntry{
			Id:     id,
			Tokens: t,
		}
		sfe.Secrets = append(sfe.Secrets, sfn)
	}

	writeSecretsFile(sfe)
}

func GetOauthAccessTokenFromCache(id string) (Oauth2Tokens, error) {
	var tokens Oauth2Tokens
	sfe := readSecretsFile()
	for _, e := range sfe.Secrets {
		if e.Id == id {
			tokens = Oauth2Tokens{
				AccessToken:  e.Tokens.AccessTokenEncrypted,
				RefreshToken: e.Tokens.RefreshTokenEncrypted,
				ExpiresIn:    e.Tokens.ExpiresIn,
				State:        e.Tokens.State,
				TokenType:    e.Tokens.TokenType,
				CreatedAt:    e.Tokens.CreatedAt,
			}
			return tokens, nil
		}
	}
	return tokens, fmt.Errorf("no tokens found for id %s", id)
}

func readSecretsFile() secretFile {
	var sfe secretFile
	bd := viper.GetString("buchhalter_config_directory")
	sef := filepath.Join(bd, secretsFilename)
	if _, err := os.Stat(sef); os.IsNotExist(err) {
		err = os.WriteFile(filepath.Join(bd, secretsFilename), nil, 0600)
		if err != nil {
			fmt.Println(err)
		}
		return sfe
	} else {
		sf, err := os.Open(sef)
		if err != nil {
			fmt.Println(err)
		}
		defer sf.Close()

		byteValue, _ := io.ReadAll(sf)
		err = json.Unmarshal(byteValue, &sfe)
		if err != nil {
			log.Fatal(err)
		}
		return sfe
	}
}

func writeSecretsFile(sfe secretFile) {
	sfj, err := json.MarshalIndent(sfe, "", "    ")
	if err != nil {
		fmt.Println(err)
	}

	bd := viper.GetString("buchhalter_config_directory")
	err = os.WriteFile(filepath.Join(bd, secretsFilename), sfj, 0600)
	if err != nil {
		fmt.Println(err)
	}
}
