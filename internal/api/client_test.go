package api

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func TestNewClientFromConfig(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITLAB_ACCESS_TOKEN", "")
	t.Setenv("OAUTH_TOKEN", "")
	t.Setenv("GLAB_IS_OAUTH2", "")
	t.Setenv("JOB_TOKEN", "")
	t.Setenv("CI_JOB_TOKEN", "")
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "")
	t.Setenv("CI_SERVER_FQDN", "")
	t.Setenv("CI_SERVER_PROTOCOL", "")
	t.Setenv("GITLAB_HOST", "")
	t.Setenv("GITLAB_API_HOST", "")
	t.Setenv("API_PROTOCOL", "")

	tests := []struct {
		name            string
		envVars         map[string]string
		repoHost        string
		expectedAuthKey string
		expectedAuthVal string
		expectedBaseURL string
	}{
		{
			name: "OAuth2 access token",
			envVars: map[string]string{
				"GITLAB_TOKEN":   "oauth2-access-token",
				"GLAB_IS_OAUTH2": "true",
			},
			repoHost:        "example.com",
			expectedAuthKey: "Authorization",
			expectedAuthVal: "Bearer oauth2-access-token",
			expectedBaseURL: "https://example.com/api/v4/",
		},
		{
			name: "PAT auth",
			envVars: map[string]string{
				"GITLAB_TOKEN": "some-pat",
			},
			repoHost:        "example.com",
			expectedAuthKey: gitlab.AccessTokenHeaderName,
			expectedAuthVal: "some-pat",
			expectedBaseURL: "https://example.com/api/v4/",
		},
		{
			name: "job token from env without CI",
			envVars: map[string]string{
				"JOB_TOKEN": "my-job-token",
			},
			repoHost:        "example.com",
			expectedAuthKey: gitlab.JobTokenHeaderName,
			expectedAuthVal: "my-job-token",
			expectedBaseURL: "https://example.com/api/v4/",
		},
		{
			name: "custom protocol from env",
			envVars: map[string]string{
				"GITLAB_TOKEN": "my-pat",
				"API_PROTOCOL": "http",
			},
			repoHost:        "example.com",
			expectedAuthKey: gitlab.AccessTokenHeaderName,
			expectedAuthVal: "my-pat",
			expectedBaseURL: "http://example.com/api/v4/",
		},
		{
			name: "CI auto-login uses CI variables",
			envVars: map[string]string{
				"GLAB_ENABLE_CI_AUTOLOGIN": "true",
				"GITLAB_CI":                "true",
				"CI_JOB_TOKEN":             "ci-tok",
				"CI_SERVER_FQDN":           "ci.example.com",
			},
			repoHost:        "example.com",
			expectedAuthKey: gitlab.JobTokenHeaderName,
			expectedAuthVal: "ci-tok",
			expectedBaseURL: "https://ci.example.com/api/v4/",
		},
		{
			name: "CI auto-login with custom protocol",
			envVars: map[string]string{
				"GLAB_ENABLE_CI_AUTOLOGIN": "true",
				"GITLAB_CI":                "true",
				"CI_JOB_TOKEN":             "ci-tok",
				"CI_SERVER_FQDN":           "ci.example.com",
				"CI_SERVER_PROTOCOL":       "http",
			},
			repoHost:        "example.com",
			expectedAuthKey: gitlab.JobTokenHeaderName,
			expectedAuthVal: "ci-tok",
			expectedBaseURL: "http://ci.example.com/api/v4/",
		},
		{
			name: "CI auto-login PAT takes precedence over job token",
			envVars: map[string]string{
				"GLAB_ENABLE_CI_AUTOLOGIN": "true",
				"GITLAB_CI":                "true",
				"GITLAB_TOKEN":             "my-pat",
				"CI_JOB_TOKEN":             "ci-tok",
				"CI_SERVER_FQDN":           "ci.example.com",
			},
			repoHost:        "example.com",
			expectedAuthKey: gitlab.AccessTokenHeaderName,
			expectedAuthVal: "my-pat",
			expectedBaseURL: "https://ci.example.com/api/v4/",
		},
		{
			name: "CI auto-login disabled falls back to PAT and passed-in host",
			envVars: map[string]string{
				"GITLAB_CI":                "true",
				"GLAB_ENABLE_CI_AUTOLOGIN": "false",
				"GITLAB_TOKEN":             "my-pat",
				"CI_JOB_TOKEN":             "ci-tok",
				"CI_SERVER_FQDN":           "ci.example.com",
			},
			repoHost:        "manual.example.com",
			expectedAuthKey: gitlab.AccessTokenHeaderName,
			expectedAuthVal: "my-pat",
			expectedBaseURL: "https://manual.example.com/api/v4/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			client, err := NewClientFromConfig(
				tt.repoHost,
				config.NewBlankConfig(),
				false,
				"test-agent",
			)
			require.NoError(t, err)

			key, value, err := client.AuthSource().Header(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tt.expectedAuthKey, key)
			assert.Equal(t, tt.expectedAuthVal, value)
			assert.Equal(t, tt.expectedBaseURL, client.BaseURL())
		})
	}
}

func TestNewHTTPRequest_EmptyTokenSetsEmptyAuthHeader(t *testing.T) {
	t.Parallel()

	// Create a client with empty token (cookie-only SSO scenario)
	client, err := NewClient(
		func(c *http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: ""}, nil
		},
		WithBaseURL("https://gitlab.example.com/api/v4/"),
	)
	require.NoError(t, err)

	baseURL, _ := url.Parse("https://gitlab.example.com/api/v4/projects")
	req, err := NewHTTPRequest(t.Context(), client, http.MethodGet, baseURL, nil, nil, false)
	require.NoError(t, err)

	// Empty token should set an empty Private-Token header
	assert.Empty(t, req.Header.Get(gitlab.JobTokenHeaderName))
	assert.Empty(t, req.Header.Get("Authorization"))
}

func TestNewHTTPRequest_TokenSetsAuthHeader(t *testing.T) {
	t.Parallel()

	client, err := NewClient(
		func(c *http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-pat"}, nil
		},
		WithBaseURL("https://gitlab.example.com/api/v4/"),
	)
	require.NoError(t, err)

	baseURL, _ := url.Parse("https://gitlab.example.com/api/v4/projects")
	req, err := NewHTTPRequest(t.Context(), client, http.MethodGet, baseURL, nil, nil, false)
	require.NoError(t, err)

	assert.Equal(t, "test-pat", req.Header.Get(gitlab.AccessTokenHeaderName))
}

func TestNewClientFromConfig_OAuth2NoTokenReturnsError(t *testing.T) {
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
