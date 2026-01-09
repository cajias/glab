package cmdutils

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

type Executor interface {
	// LookPath searches for an executable named file in the
	// directories named by the PATH environment variable.
	//
	// In production code forwards to exec.LookPath.
	LookPath(file string) (string, error)
	// Exec executes the given binary with args and env and sets up IO.
	//
	// In production code it sets up IO with the configured iostreams.
	Exec(ctx context.Context, name string, args []string, env map[string]string) error
	// Exec executes the given binary with args and env and returns the combined stdout and stderr output.
	ExecWithCombinedOutput(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error)
	// Exec executes the given binary with args and env and configures the IOs with the given arguments.
	ExecWithIO(ctx context.Context, name string, args []string, env map[string]string, stdin io.Reader, stdout, stderr io.Writer) error
}

type factoryExecutor struct {
	io *iostreams.IOStreams
}

func (f *factoryExecutor) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (f *factoryExecutor) Exec(ctx context.Context, name string, args []string, env map[string]string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = envMapToSlice(env)
	cmd.Stdin = f.io.In
	cmd.Stdout = f.io.StdOut
	cmd.Stderr = f.io.StdErr
	return cmd.Run()
}

func (f *factoryExecutor) ExecWithCombinedOutput(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = envMapToSlice(env)
	return cmd.CombinedOutput()
}

func (f *factoryExecutor) ExecWithIO(ctx context.Context, name string, args []string, env map[string]string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = envMapToSlice(env)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func envMapToSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}

	s := make([]string, 0, len(env))
	for k, v := range env {
		s = append(s, fmt.Sprintf("%s=%s", k, v))
	}

	return s
}
