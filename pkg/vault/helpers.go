package vault

import (
	"fmt"
	vaultapi "github.com/hashicorp/vault/api"
	"log"
	"strings"
)

func reportWarnings(s *vaultapi.Secret) {
	if s != nil && len(s.Warnings) > 0 {
		log.Printf("vault warnings: %v", s.Warnings)
	}
}

func GetMountPoint(basePath string, client *vaultapi.Client) (string, string, error) {
	var mountpoint, version string
	s, err := client.Logical().Read("/sys/mounts")
	if err != nil {
		return mountpoint, version, fmt.Errorf("Error determining vault mountpoint for %s: %w", basePath, err)
	}

	basePath = EnsureSuffix(basePath, "/")
	for mountpoint, data := range s.Data {
		if strings.HasPrefix(basePath, mountpoint) {
			v := SafeGet(SafeGet(data, "options"), "version")
			if s, ok := v.(string); ok {
				version = s
			}
			log.Printf("Mountpoint found for %s: %s version: %s", basePath, mountpoint, version)
			return mountpoint, version, nil
		}
	}

	log.Printf("No mountpoint found for %s", basePath)
	return mountpoint, version, nil
}

func EnsureSuffix(s, suffix string) string {
	if !strings.HasSuffix(s, suffix) {
		return s + suffix
	}
	return s
}

func SafeGet(m interface{}, key string) interface{} {
	if mm, ok := m.(map[string]interface{}); ok {
		return mm[key]
	}
	return nil
}
