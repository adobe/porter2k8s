package vault

import vaultapi "github.com/hashicorp/vault/api"

func NewClientV1(client *vaultapi.Client) *clientV1 {
	return &clientV1{client}
}

// vault API for KV V1 and cubbyhole mountpoints
type clientV1 struct {
	*vaultapi.Client
}

func (c *clientV1) Read(path string) (map[string]interface{}, error) {
	s, err := c.Client.Logical().Read(path)
	reportWarnings(s)
	if err != nil {
		return nil, err
	}
	if s != nil {
		return s.Data, nil
	}
	return nil, nil
}
