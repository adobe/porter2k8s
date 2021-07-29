package vault

import (
	"fmt"
	vaultapi "github.com/hashicorp/vault/api"
	"path"
	"strings"
)

func NewClientV2(client *vaultapi.Client, mountPoint string) *clientV2 {
	return &clientV2{Client: client, mountPoint: mountPoint}
}

// vault API for KV V2 mountpoints
type clientV2 struct {
	*vaultapi.Client
	mountPoint string
}

func (c *clientV2) Read(vaultpath string) (map[string]interface{}, error) {
	internalPath, err := serverPath(vaultpath, c.mountPoint)
	if err != nil {
		return nil, err
	}

	s, err := c.Client.Logical().Read(internalPath)
	reportWarnings(s)

	if err != nil {
		return nil, err
	}

	if s != nil {
		if data, ok := s.Data["data"].(map[string]interface{}); ok {
			return data, nil
		}
	}

	return nil, nil
}

func serverPath(vaultpath, mountPoint string) (string, error) {
	switch {
	case vaultpath == mountPoint, vaultpath == strings.TrimSuffix(mountPoint, "/"):
		return path.Join(mountPoint, "data"), nil
	default:
		subpath := strings.TrimPrefix(vaultpath, mountPoint)
		if subpath == vaultpath {
			return "", fmt.Errorf("illegal path: %s is not under mountpoint %s", vaultpath, mountPoint)
		}
		return path.Join(mountPoint, "data", subpath), nil
	}
}
