package attestation

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	attestationVerifyCmd "gitlab.com/gitlab-org/cli/internal/commands/attestation/verify"
)

func NewCmdAttestation(f cmdutils.Factory) *cobra.Command {
	attestationCmd := &cobra.Command{
		Use:   "attestation <command> [flags]",
		Short: `Manage software attestations. (EXPERIMENTAL)`,
		Long:  ``,
		Example: heredoc.Doc(`
			# Verify attestation for the filename.txt file in the gitlab-org/gitlab project.
			$ glab attestation verify gitlab-org/gitlab filename.txt

			# Verify attestation for the filename.txt file in the project with ID 123.
			$ glab attestation verify 123 filename.txt
		`),
		Annotations: map[string]string{
			"help:arguments": heredoc.Doc(`
			A project can be supplied as an argument in the following formats:
			- By number: "123"
			- By path: "gitlab-org/cli"
			`),
		},
	}

	attestationCmd.AddCommand(attestationVerifyCmd.NewCmdVerify(f))

	return attestationCmd
}
