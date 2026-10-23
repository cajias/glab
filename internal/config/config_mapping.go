package config

import (
	"os"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

func ConfigKeyEquivalence(key string) string {
	return resolveAlias(key)
}

// When CI auto-login is on (GLAB_ENABLE_CI_AUTOLOGIN=true and
// GITLAB_CI=true), these vars REPLACE the schema's EnvVars for the key.
var ciAutologinEnvOverrides = map[string][]string{
	"api_host":     {"CI_SERVER_FQDN"},
	"subfolder":    {"GITLAB_SUBFOLDER", "CI_SERVER_URL"},
	"ssh_host":     {"GITLAB_SSH_HOST", "CI_SERVER_SHELL_SSH_HOST"},
	"api_protocol": {"CI_SERVER_PROTOCOL"},
	"host":         {"CI_SERVER_FQDN"},
	"job_token":    {"CI_JOB_TOKEN"},
	"ca_cert":      {"CI_SERVER_TLS_CA_FILE"},
	"client_cert":  {"CI_SERVER_TLS_CERT_FILE"},
	"client_key":   {"CI_SERVER_TLS_KEY_FILE"},
}

// schemaEnvVars returns the environment variables a key is read from,
// ignoring the CI auto-login overrides so that help text and generated docs
// do not vary with the ambient environment.
func schemaEnvVars(key string) []string {
	canonical := resolveAlias(key)
	if kd := findKeyDef(canonical); kd != nil && len(kd.EnvVars) > 0 {
		return kd.EnvVars
	}
	return []string{strings.ToUpper(canonical)}
}

// CIAutologinEnabled reports whether CI auto-login is on, which is when
// ciAutologinEnvOverrides decide where each key is read from.
func CIAutologinEnabled() bool {
	return os.Getenv("GLAB_ENABLE_CI_AUTOLOGIN") == "true" && os.Getenv("GITLAB_CI") == "true"
}

func EnvKeyEquivalence(key string) []string {
	canonical := resolveAlias(key)

	if CIAutologinEnabled() {
		dbg.Debug("CI auto-login is enabled because GLAB_ENABLE_CI_AUTOLOGIN and GITLAB_CI are both true. This enables auto-login using GitLab's predefined CI/CD variables and potentially authenticates with the CI_JOB_TOKEN")
		if overrides, ok := ciAutologinEnvOverrides[canonical]; ok {
			return overrides
		}
	}

	return schemaEnvVars(canonical)
}

// Only KeyDefs marked Fallback contribute a value here; the rest return
// "" so an absent key surfaces as "not set" rather than the seed default.
func defaultFor(key string) string {
	canonical := resolveAlias(key)
	if kd := findKeyDef(canonical); kd != nil && kd.Fallback {
		return kd.Default
	}
	return ""
}
