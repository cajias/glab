//go:build integration

package cmdtest

import (
	"bytes"
	"io"

	"github.com/google/shlex"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/test"
)

func ExecuteCommand(cmd *cobra.Command, cli string, stdout *bytes.Buffer, stderr *bytes.Buffer) (*test.CmdOut, error) {
	argv, err := shlex.Split(cli)
	if err != nil {
		return nil, err
	}

	cmd.SetArgs(argv)
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	_, err = cmd.ExecuteC()
	return &test.CmdOut{
		OutBuf: stdout,
		ErrBuf: stderr,
	}, err
}

