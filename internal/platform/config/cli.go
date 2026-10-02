package config

import "os"

// CLIEnvironment belongs to the existing environment boundary. Values are
// consumed as credentials, never serialized as diagnostic configuration.
type CLIEnvironment struct{ ConfigDirectory, Token, CredentialOrigin, Password, ClientSecret string }

func LoadCLIEnvironment() CLIEnvironment {
	return CLIEnvironment{ConfigDirectory: os.Getenv("AICRM_CLI_CONFIG_DIR"), Token: os.Getenv("AICRM_CLI_TOKEN"), CredentialOrigin: os.Getenv("AICRM_CLI_CREDENTIAL_ORIGIN"), Password: os.Getenv("AICRM_CLI_PASSWORD"), ClientSecret: os.Getenv("AICRM_CLI_CLIENT_SECRET")}
}
