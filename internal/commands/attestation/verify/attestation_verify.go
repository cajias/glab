package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

const (
	installationUrl = "https://docs.sigstore.dev/cosign/system_config/installation/"
	cosign          = "cosign"
	tempFilePrefix  = "glabattestationverify"
	oidcIssuer      = "https://gitlab.com"
)

type options struct {
	gitlabClient    func() (*gitlab.Client, error)
	defaultHostname string
	io              *iostreams.IOStreams

	project  string
	filename string
}

func NewCmdVerify(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		gitlabClient:    f.GitLabClient,
		defaultHostname: glinstance.DefaultHostname,
		io:              f.IO(),
	}

	attestationVerifyCmd := &cobra.Command{
		Use:   "verify <project_id> <artifact_path>",
		Short: `Verify the provenance of a specific artifact or file. (EXPERIMENTAL)`,
		Long: heredoc.Doc(`
		This command is experimental.

		For more information about attestations, see:

		- [Attestations API](https://docs.gitlab.com/api/attestations/)
		- [SLSA provenance specification](https://docs.gitlab.com/ci/pipeline_security/slsa/provenance_v1/)
		- [SLSA Software attestations](https://slsa.dev/attestation-model)

		This command requires the cosign binary. To install it, see, [Cosign installation](https://docs.sigstore.dev/cosign/system_config/installation/).

		This command works with GitLab.com only.
		`),
		Args: cobra.ExactArgs(2),
		Example: heredoc.Doc(`
			# Verify attestation for the filename.txt file in the gitlab-org/gitlab project.
			$ glab attestation verify gitlab-org/gitlab filename.txt

			# Verify attestation for the filename.txt file in the project with ID 123.
			$ glab attestation verify 123 filename.txt
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.project = args[0]
			opts.filename = args[1]

			return opts.run()
		},
	}

	return attestationVerifyCmd
}

func (o *options) run() error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	project, err := api.GetProject(client, o.project)
	if err != nil {
		return err
	}

	subjectDigest, err := o.sha256(o.filename)
	if err != nil {
		return err
	}

	provenance, err := o.retrieveProvenanceMetadata(client, subjectDigest)
	if err != nil {
		return err
	}

	bundle, err := o.downloadBundle(client, provenance.IID)
	if err != nil {
		return err
	}

	err = o.verify(o.filename, project.PathWithNamespace, bundle)
	if err != nil {
		return err
	}

	o.success()

	return nil
}

func (o *options) sha256(filename string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func (o *options) retrieveProvenanceMetadata(client *gitlab.Client, subjectDigest string) (*gitlab.Attestation, error) {
	attestations, _, err := client.Attestations.ListAttestations(o.project, subjectDigest)
	if err != nil {
		return nil, err
	}

	for _, attestation := range attestations {
		if attestation.PredicateKind == "provenance" {
			return attestation, nil
		}
	}

	return nil, fmt.Errorf("Unable to find a provenance statement for %s", subjectDigest)
}

func (o *options) downloadBundle(client *gitlab.Client, attestationIID int64) ([]byte, error) {
	provenanceStatement, _, err := client.Attestations.DownloadAttestation(o.project, attestationIID)
	if err != nil {
		return nil, err
	}

	return provenanceStatement, nil
}

func (o *options) bundleTempFile(bundleBytes []byte) (filename string, err error) { //nolint:nonamedreturns
	f, err := os.CreateTemp("", tempFilePrefix)
	filename = f.Name()

	if err != nil {
		return
	}

	if _, err = f.Write(bundleBytes); err != nil {
		return
	}

	defer func() {
		cerr := f.Close()
		if err == nil {
			err = cerr
		}
	}()

	return
}

type commandExecutor interface {
	CombinedOutput() ([]byte, error)
}

var execCommand = func(name string, arg ...string) commandExecutor {
	return exec.Command(name, arg...)
}

var lookPath = func(path string) (string, error) {
	return exec.LookPath(path)
}

func (o *options) verify(filename string, repoPath string, bundleBytes []byte) (err error) {
	cosignPath, err := lookPath(cosign)
	if err != nil {
		return fmt.Errorf("Unable to locate the `%s` binary. Please install following these instructions: %s", cosign, installationUrl)
	}

	bundlePath, err := o.bundleTempFile(bundleBytes)
	defer func() {
		rerr := os.Remove(bundlePath)
		if err == nil {
			err = rerr
		}
	}()

	if err != nil {
		return
	}

	expectedIssuer := fmt.Sprintf("https://%s", o.defaultHostname)
	expectedSanRegex := fmt.Sprintf("^https://%s/%s/", o.defaultHostname, repoPath)
	args := []string{
		"verify-blob-attestation",
		"--new-bundle-format",
		"--bundle",
		bundlePath,
		"--type",
		"slsaprovenance1",
		filename,
		"--certificate-identity-regexp",
		expectedSanRegex,
		"--certificate-oidc-issuer",
		expectedIssuer,
	}

	cmd := execCommand(cosignPath, args...)

	stdoutStderr, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s\n", err, stdoutStderr)
	}

	return
}

func (o *options) success() {
	c := o.io.Color()
	out := o.io.StdOut

	fmt.Fprint(out, c.Green("VERIFIED"))
	fmt.Fprintf(out, " • Artifact provenance successfully verified. Signatures confirm %s was attested by %s\n", o.filename, o.project)
	fmt.Fprintln(out)
}
