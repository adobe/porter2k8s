package vault

import vaultapi "github.com/hashicorp/vault/api"

// Client - interface for reading / writing to vault
type VaultClientInterface interface {
	Read(path string) (map[string]interface{}, error)

	Auth() *vaultapi.Auth
}
