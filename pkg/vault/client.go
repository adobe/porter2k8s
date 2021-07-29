package vault

import (
	vaultapi "github.com/hashicorp/vault/api"
	"log"
)

func NewVaultClient(address, namespace, token string) (VaultClientInterface, error) {
	client, err := vaultapi.NewClient(&vaultapi.Config{
		Address: address,
	})

	if err != nil {
		return nil, err
	}
	client.SetToken(token)
	mountpointPrefix := "secret"
	if namespace != "" {
		client.SetNamespace(namespace)
		mountpointPrefix = namespace
	}

	mountPoint, version, err := GetMountPoint(mountpointPrefix, client)

	var versionedEngineClient VaultClientInterface
	if version == "2" {
		log.Printf("Determined KV version 2, mountpoint %s", mountPoint)
		versionedEngineClient = NewClientV2(client, mountPoint)
	} else {
		log.Printf("Determined KV version 1")
		versionedEngineClient = NewClientV1(client)
	}

	return versionedEngineClient, nil
}
