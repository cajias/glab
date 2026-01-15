package cmdtest

import (
	"bytes"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
)

var (
	ProjectPath    string
	GlabBinaryPath string
)

type fatalLogger interface {
	Fatal(...any)
}

func init() {
	path := &bytes.Buffer{}
	// get root dir via git
	gitCmd := git.GitCommand("rev-parse", "--show-toplevel")
	gitCmd.Stdout = path
	err := gitCmd.Run()
	if err != nil {
		log.Fatalln("Failed to get root directory: ", err)
	}
	ProjectPath = strings.TrimSuffix(path.String(), "\n")
	if !strings.HasSuffix(ProjectPath, "/") {
		ProjectPath += "/"
	}
}

func InitTest(m *testing.M, suffix string) {
	// Build a glab binary with test symbols. If the parent test binary was run
	// with coverage enabled, enable coverage on the child binary, too.
	var err error
	GlabBinaryPath, err = filepath.Abs(os.ExpandEnv(ProjectPath + "testdata/glab.test"))
	if err != nil {
		log.Fatal(err)
	}
	testCmd := []string{"test", "-c", "-o", GlabBinaryPath, ProjectPath + "cmd/glab"}
	if coverMode := testing.CoverMode(); coverMode != "" {
		testCmd = append(testCmd, "-covermode", coverMode, "-coverpkg", "./...")
	}
	if out, err := exec.Command("go", testCmd...).CombinedOutput(); err != nil {
		log.Fatalf("Error building glab test binary: %s (%s)", string(out), err)
	}

	originalWd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	var repo string = CopyTestRepo(log.New(os.Stderr, "", log.LstdFlags), suffix)

	if err := os.Chdir(repo); err != nil {
		log.Fatalf("Error chdir to test/testdata: %s", err)
	}
	code := m.Run()

	if err := os.Chdir(originalWd); err != nil {
		log.Fatalf("Error chdir to original working dir: %s", err)
	}

	testdirs, err := filepath.Glob(os.ExpandEnv(repo))
	if err != nil {
		log.Printf("Error listing glob test/testdata-*: %s", err)
	}
	for _, dir := range testdirs {
		err := os.RemoveAll(dir)
		if err != nil {
			log.Printf("Error removing dir %s: %s", dir, err)
		}
	}

	os.Exit(code)
}

func CopyTestRepo(log fatalLogger, name string) string {
	if name == "" {
		name = strconv.Itoa(int(rand.Uint64()))
	}
	dest, err := filepath.Abs(os.ExpandEnv(ProjectPath + "test/testdata-" + name))
	if err != nil {
		log.Fatal(err)
	}
	src, err := filepath.Abs(os.ExpandEnv(ProjectPath + "test/testdata"))
	if err != nil {
		log.Fatal(err)
	}
	if err := copy.Copy(src, dest); err != nil {
		log.Fatal(err)
	}
	// Move the test.git dir into the expected path at .git
	if !config.CheckPathExists(dest + "/.git") {
		if err := os.Rename(dest+"/test.git", dest+"/.git"); err != nil {
			log.Fatal(err)
		}
	}
	// Move the test.glab-cli dir into the expected path at .glab-cli
	if !config.CheckPathExists(dest + "/.glab-cli") {
		if err := os.Rename(dest+"/test.glab-cli", dest+"/.glab-cli"); err != nil {
			log.Fatal(err)
		}
	}
	return dest
}

func NewTestApiClient(t *testing.T, httpClient *http.Client, token, host string, options ...api.ClientOption) *api.Client {
	t.Helper()

	opts := []api.ClientOption{
		api.WithUserAgent("glab test client"),
		api.WithBaseURL(glinstance.APIEndpoint(host, glinstance.DefaultProtocol, "")),
		api.WithInsecureSkipVerify(true),
		api.WithHTTPClient(httpClient),
	}
	opts = append(opts, options...)
	testClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) { return gitlab.AccessTokenAuthSource{Token: token}, nil },
		opts...,
	)
	require.NoError(t, err)
	return testClient
}

func NewTestOAuth2ApiClient(t *testing.T, httpClient *http.Client, tokenSource oauth2.TokenSource, host string, options ...api.ClientOption) *api.Client {
	t.Helper()

	opts := []api.ClientOption{
		api.WithUserAgent("glab test client"),
		api.WithBaseURL(glinstance.APIEndpoint(host, glinstance.DefaultProtocol, "")),
		api.WithInsecureSkipVerify(true),
		api.WithHTTPClient(httpClient),
	}
	opts = append(opts, options...)
	testClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.OAuthTokenSource{TokenSource: tokenSource}, nil
		},
		opts...,
	)
	require.NoError(t, err)
	return testClient
}

type safeBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (sb *safeBuffer) Write(p []byte) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *safeBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

func (sb *safeBuffer) Bytes() []byte {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Bytes()
}

func (sb *safeBuffer) Reset() {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.buf.Reset()
}

func (sb *safeBuffer) Len() int {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Len()
}

type teeReadCloser struct {
	io.Reader
	closer io.Closer
}

func (t *teeReadCloser) Close() error {
	return t.closer.Close()
}

func newTeeReadCloser(r io.ReadCloser, w io.Writer) io.ReadCloser {
	return &teeReadCloser{
		Reader: io.TeeReader(r, w),
		closer: r,
	}
}
