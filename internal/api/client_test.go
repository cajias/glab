package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func TestClient_OAuth2AccessTokenOnly(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "oauth2-access-token")
	t.Setenv("GLAB_IS_OAUTH2", "true")

	client, err := NewClientFromConfig(
		"example.gitlab.com",
		config.NewBlankConfig(),
		false,
		"dummy user agent",
	)
	require.NoError(t, err)

	key, value, err := client.AuthSource().Header(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "Authorization", key)
	assert.Equal(t, "Bearer oauth2-access-token", value)
}

func TestClient_OAuth2AccessTokenOnly_NoToken(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GLAB_IS_OAUTH2", "true")

	_, err := NewClientFromConfig(
		"example.gitlab.com",
		config.NewBlankConfig(),
		false,
		"dummy user agent",
	)
	require.Error(t, err)
}

func TestClient_PATAuth(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "some-pat")
	t.Setenv("GLAB_IS_OAUTH2", "")

	client, err := NewClientFromConfig(
		"example.gitlab.com",
		config.NewBlankConfig(),
		false,
		"dummy user agent",
	)
	require.NoError(t, err)

	key, value, err := client.AuthSource().Header(t.Context())
	require.NoError(t, err)
	assert.Equal(t, gitlab.AccessTokenHeaderName, key)
	assert.Equal(t, "some-pat", value)
}
