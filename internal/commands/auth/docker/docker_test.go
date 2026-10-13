//go:build !integration

package docker

import (
	"encoding/json"
	"testing"

	"github.com/docker/docker-credential-helpers/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestCredentialHelper_DefaultHostMissingKeyringToken(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITLAB_ACCESS_TOKEN", "")
	t.Setenv("OAUTH_TOKEN", "")
	t.Setenv("GLAB_IS_OAUTH2", "")
	t.Setenv("JOB_TOKEN", "")
	t.Setenv("CI_JOB_TOKEN", "")
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "")

	cfg := config.NewFromString(`
---
hosts:
  gitlab.com:
    use_keyring: "true"
    user: default-user
    container_registry_domains: registry.gitlab.com
    artifact_registry_domains: artifacts.gitlab.com
  gitlab.example.com:
    user: example-user
    token: example-token
    container_registry_domains: registry.gitlab.example.com
`)

	run := func(t *testing.T, registry string) (string, error) {
		t.Helper()

		exec := cmdtest.SetupCmdForTest(t, NewCmdCredentialHelper, false,
			cmdtest.WithConfig(cfg),
			cmdtest.WithStdin(registry),
			// Same as the real factory, so the default host's missing token
			// fails here if the helper builds its client through f.ApiClient.
			cmdtest.WithApiClientFunc(func(repoHost string) (*api.Client, error) {
				if repoHost == "" {
					repoHost = glinstance.DefaultHostname
				}
				return api.NewClientFromConfig(repoHost, cfg, false, "test-agent")
			}),
		)

		out, err := exec("get")
		return out.String(), err
	}

	t.Run("a registry on another host still gets its credentials", func(t *testing.T) {
		out, err := run(t, "registry.gitlab.example.com")
		require.NoError(t, err)

		var creds credentials.Credentials
		require.NoError(t, json.Unmarshal([]byte(out), &creds))
		assert.Equal(t, "example-user", creds.Username)
		assert.Equal(t, "example-token", creds.Secret)
	})

	t.Run("the default host's container registry still fails", func(t *testing.T) {
		_, err := run(t, "registry.gitlab.com")
		require.ErrorContains(t, err, "glab token for this registryURL (hostname) is empty")
	})

	t.Run("the default host's artifact registry names the missing keyring token", func(t *testing.T) {
		_, err := run(t, "artifacts.gitlab.com")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "operating system keyring")
		assert.Contains(t, err.Error(), "glab auth login --hostname gitlab.com")
	})
}
