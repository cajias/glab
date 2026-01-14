//go:build !integration

package update

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCheckUpdateCmd(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	type args struct {
		version string
	}
	tests := []struct {
		name   string
		args   args
		stdOut string
		stdErr string
	}{
		{
			name: "same version",
			args: args{
				version: "v1.11.1",
			},
			stdErr: "You are already using the latest version of glab!\n",
		},
		{
			name: "older version",
			args: args{
				version: "v1.11.0",
			},
			stdErr: "A new version of glab has been released: v1.11.0 -> v1.11.1\nhttps://gitlab.com/gitlab-org/cli/-/releases/v1.11.1\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			// Mock ListReleases
			testClient.MockReleases.EXPECT().
				ListReleases("gitlab-org/cli", gomock.Any()).
				DoAndReturn(func(pid any, opts *gitlab.ListReleasesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Release, *gitlab.Response, error) {
					// Verify pagination options
					assert.Equal(t, int64(1), opts.Page)
					assert.Equal(t, int64(1), opts.PerPage)
					return []*gitlab.Release{
						{
							TagName:    "v1.11.1",
							Name:       "v1.11.1",
							ReleasedAt: gitlab.Ptr(time.Date(2020, 11, 3, 5, 39, 4, 0, time.UTC)),
						},
					}, nil, nil
				})

			// Override clientCreator to use the mocked client
			oldCreator := clientCreator
			clientCreator = func(userAgent string, options ...api.ClientOption) (*api.Client, error) {
				return api.NewClient(
					func(*http.Client) (gitlab.AuthSource, error) {
						return gitlab.AccessTokenAuthSource{Token: ""}, nil
					},
					api.WithGitLabClient(testClient.Client),
				)
			}
			t.Cleanup(func() {
				clientCreator = oldCreator
			})

			ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))

			factory := cmdtest.NewTestFactory(
				ios,
				cmdtest.WithBuildInfo(api.BuildInfo{Version: tc.args.version}),
			)

			cmd := NewCheckUpdateCmd(factory)

			defer config.StubWriteConfig(io.Discard, io.Discard)()
			output, err := cmdtest.ExecuteCommand(cmd, "", stdout, stderr)

			require.NoError(t, err)
			assert.Empty(t, output.String())
			assert.Equal(t, tc.stdErr, output.Stderr())
		})
	}
}

func TestNewCheckUpdateCmd_error(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	// Mock ListReleases - returns 404
	notFoundResp := &gitlab.Response{
		Response: &http.Response{StatusCode: http.StatusNotFound},
	}
	testClient.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any()).
		Return(nil, notFoundResp, errors.New("404 Not Found"))

	// Override clientCreator to use the mocked client
	oldCreator := clientCreator
	clientCreator = func(userAgent string, options ...api.ClientOption) (*api.Client, error) {
		return api.NewClient(
			func(*http.Client) (gitlab.AuthSource, error) {
				return gitlab.AccessTokenAuthSource{Token: ""}, nil
			},
			api.WithGitLabClient(testClient.Client),
		)
	}
	t.Cleanup(func() {
		clientCreator = oldCreator
	})

	ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))

	factory := cmdtest.NewTestFactory(
		ios,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.11.0"}),
	)

	cmd := NewCheckUpdateCmd(factory)

	defer config.StubWriteConfig(io.Discard, io.Discard)()
	output, err := cmdtest.ExecuteCommand(cmd, "", stdout, stderr)

	require.Error(t, err)
	assert.Equal(t, `failed checking for glab updates: 404 Not Found`, err.Error())
	assert.Empty(t, output.String())
	assert.Empty(t, output.Stderr())
}

func TestNewCheckUpdateCmd_no_release(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	// Mock ListReleases - returns empty list
	testClient.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any()).
		Return([]*gitlab.Release{}, nil, nil)

	// Override clientCreator to use the mocked client
	oldCreator := clientCreator
	clientCreator = func(userAgent string, options ...api.ClientOption) (*api.Client, error) {
		return api.NewClient(
			func(*http.Client) (gitlab.AuthSource, error) {
				return gitlab.AccessTokenAuthSource{Token: ""}, nil
			},
			api.WithGitLabClient(testClient.Client),
		)
	}
	t.Cleanup(func() {
		clientCreator = oldCreator
	})

	ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))

	factory := cmdtest.NewTestFactory(
		ios,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.11.0"}),
	)

	cmd := NewCheckUpdateCmd(factory)

	defer config.StubWriteConfig(io.Discard, io.Discard)()
	output, err := cmdtest.ExecuteCommand(cmd, "", stdout, stderr)

	require.Error(t, err)
	assert.Equal(t, "no release found for glab.", err.Error())
	assert.Empty(t, output.String())
	assert.Empty(t, output.Stderr())
}

func Test_isOlderVersion(t *testing.T) {
	type args struct {
		latestVersion  string
		currentVersion string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "latest is newer",
			args: args{"v1.10.0", "v1.9.1"},
			want: true,
		},
		{
			name: "latest is current",
			args: args{"v1.9.2", "v1.9.2"},
			want: false,
		},
		{
			name: "latest is older",
			args: args{"v1.9.0", "v1.9.2-pre.1"},
			want: false,
		},
		{
			name: "current is prerelease",
			args: args{"v1.9.0", "v1.9.0-pre.1"},
			want: true,
		},
		{
			name: "latest is older (against prerelease)",
			args: args{"v1.9.0", "v1.10.0-pre.1"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOlderVersion(tt.args.latestVersion, tt.args.currentVersion); got != tt.want {
				t.Errorf("isOlderVersion(%s, %s) = %v, want %v",
					tt.args.latestVersion, tt.args.currentVersion, got, tt.want)
			}
		})
	}
}

func TestShouldSkipUpdate_NoRun(t *testing.T) {
	tests := []struct {
		name            string
		previousCommand string
	}{
		{
			name:            "when previous command is check-update",
			previousCommand: "check-update",
		},
		{
			name:            "when previous command is an alias for check-update",
			previousCommand: "update",
		},
		{
			name:            "when previous command is completion",
			previousCommand: "completion",
		},
		{
			name:            "when previous command is git-credential",
			previousCommand: "git-credential",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, ShouldSkipUpdate(tt.previousCommand))
		})
	}
}

func Test_isEnvForcingUpdate(t *testing.T) {
	tests := []struct {
		name        string
		envVarKey   string
		envVarVal   string
		forceUpdate bool
	}{
		{
			name:        "when the GLAB_CHECK_UPDATE value is true",
			envVarKey:   "GLAB_CHECK_UPDATE",
			envVarVal:   "true",
			forceUpdate: true,
		},
		{
			name:        "when the GLAB_CHECK_UPDATE value is yes",
			envVarKey:   "GLAB_CHECK_UPDATE",
			envVarVal:   "yes",
			forceUpdate: true,
		},
		{
			name:        "when the GLAB_CHECK_UPDATE value is 1",
			envVarKey:   "GLAB_CHECK_UPDATE",
			envVarVal:   "1",
			forceUpdate: true,
		},
		{
			name:        "when GLAB_CHECK_UPDATE is not set",
			forceUpdate: false,
		},
		{
			name:        "when the GLAB_CHECK_UPDATE value is false",
			envVarKey:   "GLAB_CHECK_UPDATE",
			envVarVal:   "false",
			forceUpdate: false,
		},
		{
			name:        "when the GLAB_CHECK_UPDATE value is not a valid option",
			envVarKey:   "GLAB_CHECK_UPDATE",
			envVarVal:   "value-not-supported",
			forceUpdate: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVarKey != "" {
				t.Setenv(tt.envVarKey, tt.envVarVal)
			}

			assert.Equal(t, tt.forceUpdate, isEnvForcingUpdate())
		})
	}
}

func Test_checkLastUpdate(t *testing.T) {
	tests := []struct {
		name           string
		lastUpdate     string
		expectedResult bool
		expectError    bool
		envVarKey      string
		envVarVal      string
	}{
		{
			name:           "first time check",
			lastUpdate:     "",
			expectedResult: true,
			expectError:    false,
		},
		{
			name:           "should skip if we checked within the last 24h",
			lastUpdate:     time.Now().Add(-12 * time.Hour).Format(time.RFC3339), // 12h ago
			expectedResult: false,
			expectError:    false,
		},
		{
			name:           "should not skip if we checked more than 24h ago",
			lastUpdate:     time.Now().Add(-48 * time.Hour).Format(time.RFC3339), // 2 days ago
			expectedResult: true,
			expectError:    false,
		},
		{
			name:           "should return error due to invalid timestamp format",
			lastUpdate:     "invalid-timestamp",
			expectedResult: false,
			expectError:    true,
		},
		{
			name:           "should not skip because of GLAB_CHECK_UPDATE=true and last check was 12h ago",
			lastUpdate:     time.Now().Add(-12 * time.Hour).Format(time.RFC3339), // 12h ago
			envVarKey:      "GLAB_CHECK_UPDATE",
			envVarVal:      "true",
			expectedResult: true,
			expectError:    false,
		},
		{
			name:           "should not skip because of GLAB_CHECK_UPDATE=true and last check was 2 days ago",
			lastUpdate:     time.Now().Add(-48 * time.Hour).Format(time.RFC3339), // 2 days ago
			envVarKey:      "GLAB_CHECK_UPDATE",
			envVarVal:      "true",
			expectedResult: true,
			expectError:    false,
		},
		{
			name:           "should not skip because of GLAB_CHECK_UPDATE=false and last check was 12h ago",
			lastUpdate:     time.Now().Add(-12 * time.Hour).Format(time.RFC3339), // 12h ago
			envVarKey:      "GLAB_CHECK_UPDATE",
			envVarVal:      "false",
			expectedResult: false,
			expectError:    false,
		},
		{
			name:           "should not skip because of GLAB_CHECK_UPDATE=false and last check was 2 days ago",
			lastUpdate:     time.Now().Add(-48 * time.Hour).Format(time.RFC3339), // 2 days ago
			envVarKey:      "GLAB_CHECK_UPDATE",
			envVarVal:      "false",
			expectedResult: true,
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVarKey != "" {
				t.Setenv(tt.envVarKey, tt.envVarVal)
			}

			mainBuf := bytes.Buffer{}
			defer config.StubWriteConfig(&mainBuf, io.Discard)()

			f := cmdtest.NewTestFactory(nil,
				func(f *cmdtest.Factory) {
					f.ConfigStub = func() config.Config {
						if tt.lastUpdate != "" {
							return config.NewFromString(fmt.Sprintf("last_update_check_timestamp: %s", tt.lastUpdate))
						}
						return config.NewBlankConfig()
					}
				},
			)

			result, err := checkLastUpdate(f, false)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}

			// For first time check, verify timestamp was set
			if tt.name == "first time check" {
				cfg := config.NewFromString(mainBuf.String())
				timestamp, err := cfg.Get("", "last_update_check_timestamp")

				assert.NoError(t, err)
				assert.NotEmpty(t, timestamp)

				// Verify the timestamp is in correct format
				_, err = time.Parse(time.RFC3339, timestamp)
				assert.NoError(t, err)
			}
		})
	}
}

func TestPrintUpdateError(t *testing.T) {
	cmd := &cobra.Command{}

	type args struct {
		err   error
		cmd   *cobra.Command
		debug bool
	}
	tests := []struct {
		name    string
		args    args
		wantOut string
	}{
		{
			name: "generic error",
			args: args{
				err:   errors.New("the app exploded"),
				cmd:   cmd,
				debug: false,
			},
			wantOut: "ERROR: the app exploded\n",
		},
		{
			name: "DNS error",
			args: args{
				err: fmt.Errorf("DNS error: %w", &net.DNSError{
					Name: "https://gitlab.com/api/v4",
				}),
				cmd:   cmd,
				debug: false,
			},
			wantOut: "x error connecting to https://gitlab.com/api/v4\n",
		},
		{
			name: "DNS error with debug",
			args: args{
				err: fmt.Errorf("DNS error: %w", &net.DNSError{
					Name: "https://gitlab.com/api/v4",
				}),
				cmd:   cmd,
				debug: true,
			},
			wantOut: "x error connecting to https://gitlab.com/api/v4\nx lookup https://gitlab.com/api/v4: \n",
		},
		{
			name: "Cobra flag error",
			args: args{
				err:   &cmdutils.FlagError{Err: errors.New("unknown flag --foo")},
				cmd:   cmd,
				debug: false,
			},
			wantOut: "ERROR: unknown flag --foo\n",
		},
		{
			name: "unknown Cobra command error",
			args: args{
				err:   errors.New("unknown command foo"),
				cmd:   cmd,
				debug: false,
			},
			wantOut: "ERROR: unknown command foo\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streams, _, _, out := cmdtest.TestIOStreams()
			PrintUpdateError(streams, tt.args.err, tt.args.cmd, tt.args.debug)
			if gotOut := out.String(); gotOut != tt.wantOut {
				t.Errorf("PrintUpdateError() = %q, want %q", gotOut, tt.wantOut)
			}
		})
	}
}
