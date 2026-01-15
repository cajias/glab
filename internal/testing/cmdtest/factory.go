package cmdtest

import (
	"bytes"
	io "io"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/shlex"
	"github.com/spf13/cobra"
	"github.com/survivorbat/huhtest"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/test"
)

type CmdExecFunc func(cli string) (*test.CmdOut, error)

type CmdFunc func(cmdutils.Factory) *cobra.Command

// FactoryOption is a function that configures a Factory
type FactoryOption func(f *Factory)

type Factory struct {
	ApiClientStub    func(repoHost string) (*api.Client, error)
	GitLabClientStub func() (*gitlab.Client, error)
	BaseRepoStub     func() (glrepo.Interface, error)
	RemotesStub      func() (glrepo.Remotes, error)
	ConfigStub       func() config.Config
	BranchStub       func() (string, error)
	IOStub           *iostreams.IOStreams
	BuildInfoStub    api.BuildInfo
	ExecutorStub     cmdutils.Executor

	repoOverride string

	// captured standard ios for assertion purposes
	stdin  safeBuffer
	stdout bytes.Buffer
	stderr bytes.Buffer

	// functions to run before and after exec a command
	// NOTE: this is only supported via SetupCmdForTest
	execSetup   []func()
	execCleanup []func()
}

// NewTestFactory creates a Factory configured for testing with the given options
func NewTestFactory(ios *iostreams.IOStreams, opts ...FactoryOption) *Factory {
	f := &Factory{
		IOStub: ios,
		ApiClientStub: func(repoHost string) (*api.Client, error) {
			return &api.Client{}, nil
		},
		GitLabClientStub: func() (*gitlab.Client, error) {
			return &gitlab.Client{}, nil
		},
		ConfigStub: func() config.Config {
			return config.NewBlankConfig()
		},
		BaseRepoStub: func() (glrepo.Interface, error) {
			return glrepo.New("OWNER", "REPO", glinstance.DefaultHostname), nil
		},
		BranchStub: func() (string, error) {
			return "main", nil
		},
		BuildInfoStub: api.BuildInfo{Version: "test", Commit: "test", Platform: runtime.GOOS, Architecture: runtime.GOARCH},
	}

	// Apply all options
	for _, opt := range opts {
		opt(f)
	}

	// Create multi writers to assert outputs
	if f.IOStub != nil {
		f.IOStub.In = newTeeReadCloser(f.IOStub.In, &f.stdin)
		f.IOStub.StdOut = io.MultiWriter(&f.stdout, f.IOStub.StdOut)
		f.IOStub.StdErr = io.MultiWriter(&f.stderr, f.IOStub.StdErr)
	}

	// run setup functions (may have been registered by FactoryOption functions)
	for _, sf := range f.execSetup {
		sf()
	}

	return f
}

// SetupCmdForTest creates a test environment with a configured Factory
func SetupCmdForTest(t *testing.T, cmdFunc CmdFunc, isTTY bool, opts ...FactoryOption) CmdExecFunc {
	t.Helper()

	ios, _, _, _ := TestIOStreams(WithTestIOStreamsAsTTY(isTTY))

	f := NewTestFactory(ios, opts...)
	return func(cli string) (*test.CmdOut, error) {
		defer f.cleanup()

		argv, err := shlex.Split(cli)
		if err != nil {
			return nil, err
		}

		cmd := cmdFunc(f)
		cmd.SetArgs(argv)

		_, err = cmd.ExecuteC()
		return &test.CmdOut{
			OutBuf: &f.stdout,
			ErrBuf: &f.stderr,
		}, err
	}
}

func (f *Factory) cleanup() {
	for _, cf := range f.execCleanup {
		cf()
	}
}

func (f *Factory) RepoOverride(repo string) error {
	f.repoOverride = repo
	return nil
}

func (f *Factory) ApiClient(repoHost string) (*api.Client, error) {
	return f.ApiClientStub(repoHost)
}

func (f *Factory) GitLabClient() (*gitlab.Client, error) {
	return f.GitLabClientStub()
}

func (f *Factory) BaseRepo() (glrepo.Interface, error) {
	if f.repoOverride != "" {
		return glrepo.FromFullName(f.repoOverride, glinstance.DefaultHostname)
	}
	return f.BaseRepoStub()
}

func (f *Factory) Remotes() (glrepo.Remotes, error) {
	return f.RemotesStub()
}

func (f *Factory) Config() config.Config {
	return f.ConfigStub()
}

func (f *Factory) Branch() (string, error) {
	return f.BranchStub()
}

func (f *Factory) IO() *iostreams.IOStreams {
	return f.IOStub
}

func (f *Factory) DefaultHostname() string {
	return glinstance.DefaultHostname
}

func (f *Factory) BuildInfo() api.BuildInfo {
	return f.BuildInfoStub
}

func (f *Factory) Executor() cmdutils.Executor {
	return f.ExecutorStub
}

// WithApiClient configures the Factory with a specific API client
func WithApiClient(client *api.Client) FactoryOption {
	return func(f *Factory) {
		f.ApiClientStub = func(repoHost string) (*api.Client, error) {
			return client, nil
		}
	}
}

// WithGitLabClient configures the Factory with a specific GitLab client
func WithGitLabClient(client *gitlab.Client) FactoryOption {
	return func(f *Factory) {
		f.GitLabClientStub = func() (*gitlab.Client, error) {
			return client, nil
		}
	}
}

// WithConfig configures the Factory with a specific config
func WithConfig(cfg config.Config) FactoryOption {
	return func(f *Factory) {
		f.ConfigStub = func() config.Config {
			return cfg
		}
	}
}

// WithGitLabClientError configures the Factory to return an error when creating GitLab client
func WithGitLabClientError(err error) FactoryOption {
	return func(f *Factory) {
		f.GitLabClientStub = func() (*gitlab.Client, error) {
			return nil, err
		}
	}
}

// WithBaseRepoError configures the Factory to return an error when getting base repo
func WithBaseRepoError(err error) FactoryOption {
	return func(f *Factory) {
		f.BaseRepoStub = func() (glrepo.Interface, error) {
			return nil, err
		}
	}
}

// WithBranchError configures the Factory to return an error when getting branch
func WithBranchError(err error) FactoryOption {
	return func(f *Factory) {
		f.BranchStub = func() (string, error) {
			return "", err
		}
	}
}

// WithBaseRepo configures the Factory with a specific base repository
func WithBaseRepo(owner, repo string, hostname string) FactoryOption {
	return func(f *Factory) {
		f.BaseRepoStub = func() (glrepo.Interface, error) {
			repoHostname := glinstance.DefaultHostname
			if hostname != "" {
				repoHostname = hostname
			}
			return glrepo.New(owner, repo, repoHostname), nil
		}
	}
}

// WithBranch configures the Factory with a specific branch
func WithBranch(branch string) FactoryOption {
	return func(f *Factory) {
		f.BranchStub = func() (string, error) {
			return branch, nil
		}
	}
}

// WithBuildInfo configures the Factory build information
func WithBuildInfo(buildInfo api.BuildInfo) FactoryOption {
	return func(f *Factory) {
		f.BuildInfoStub = buildInfo
	}
}

// WithStdin configures the Factory with specific stdin content
func WithStdin(stdin string) FactoryOption {
	return func(f *Factory) {
		f.IOStub.In = io.NopCloser(bytes.NewBufferString(stdin))
	}
}

// WithIOStreamsOverride configures an IOStreams instance for testing
//
// Attention: only use as a last resort and prefer functions like WithStdin etc to configure the IOStreams that's already created.
func WithIOStreamsOverride(ios *iostreams.IOStreams) FactoryOption {
	return func(f *Factory) {
		f.IOStub = ios
	}
}

func WithExecutor(exec cmdutils.Executor) FactoryOption {
	return func(f *Factory) {
		f.ExecutorStub = exec
	}
}

func WithResponder(t *testing.T, responder *huhtest.Responder) FactoryOption {
	t.Helper()

	return func(f *Factory) {
		rIn, wIn := io.Pipe()
		rOut, wOut := io.Pipe()

		closer := func() {
			rIn.Close()
			wIn.Close()
			rOut.Close()
			wOut.Close()
		}

		f.IOStub.In = rIn
		f.IOStub.StdOut = wOut

		// register setup functions to redirect ios to responder
		startResponder := func() {
			rstdin, rstdout, cancel := responder.Start(t, 1*time.Hour)

			var wg sync.WaitGroup

			wg.Go(func() {
				_, _ = io.Copy(wIn, rstdin)
			})

			wg.Go(func() {
				_, _ = io.Copy(rstdout, rOut)
			})

			f.execCleanup = append(f.execCleanup, func() {
				cancel()
				closer()

				wg.Wait()
			})
		}

		f.execSetup = append(f.execSetup, startResponder)
	}
}
