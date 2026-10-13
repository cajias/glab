package config

import (
	"fmt"
)

func (c *fileConfig) configForHost(hostname string) (*HostConfig, error) {
	hosts, err := c.hostEntries()
	if err != nil {
		return nil, fmt.Errorf("failed to parse hosts config: %w", err)
	}

	for _, hc := range hosts {
		if hc.Host == hostname {
			return hc, nil
		}
	}
	return nil, &NotFoundError{fmt.Errorf("could not find config entry for %q", hostname)}
}

// HostUsesKeyring reports whether the host's own config entry sets use_keyring
// to "true". GetWithSource reads the keyring under the same rule, so the
// environment and the top-level config do not count.
func HostUsesKeyring(cfg Config, hostname string) bool {
	fc, ok := cfg.(*fileConfig)
	if !ok {
		return false
	}
	hostCfg, err := fc.configForHost(hostname)
	if err != nil {
		return false
	}
	useKeyring, _ := hostCfg.GetStringValue("use_keyring")
	return useKeyring == "true"
}
