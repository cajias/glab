//go:build !integration

package api

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

func TestNewSSOPrompt_NonTTY(t *testing.T) {
	stdin := io.NopCloser(strings.NewReader("y\n"))
	stderr := &bytes.Buffer{}

	// Create iostreams with stdin NOT as TTY
	ios := iostreams.New(
		iostreams.WithStdin(stdin, false), // false = not a TTY
		iostreams.WithStderr(stderr, false),
	)

	prompt := NewSSOPrompt(ios)
	allowed, err := prompt("okta.example.com")

	if allowed {
		t.Error("expected allowed to be false for non-TTY")
	}
	if err == nil {
		t.Fatal("expected error for non-TTY, got nil")
	}
	if !strings.Contains(err.Error(), "stdin is not a terminal") {
		t.Errorf("expected error about stdin not being terminal, got: %v", err)
	}
}

func TestNewSSOPrompt_UserApproves(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"lowercase y", "y\n"},
		{"uppercase Y", "Y\n"},
		{"lowercase yes", "yes\n"},
		{"uppercase YES", "YES\n"},
		{"mixed case Yes", "Yes\n"},
		{"with leading space", " y\n"},
		{"with trailing space", "y \n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdin := io.NopCloser(strings.NewReader(tt.input))
			stderr := &bytes.Buffer{}

			// IsInputTTY requires all three: IsInTTY && IsaTTY && IsErrTTY
			ios := iostreams.New(
				iostreams.WithStdin(stdin, true),
				iostreams.WithStdout(io.Discard, true),
				iostreams.WithStderr(stderr, true),
			)

			prompt := NewSSOPrompt(ios)
			allowed, err := prompt("okta.example.com")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !allowed {
				t.Error("expected allowed to be true")
			}

			// Verify prompt was shown
			output := stderr.String()
			if !strings.Contains(output, "okta.example.com") {
				t.Errorf("expected stderr to contain domain, got: %s", output)
			}
			if !strings.Contains(output, "[y/N]") {
				t.Errorf("expected stderr to contain [y/N] prompt, got: %s", output)
			}
		})
	}
}

func TestNewSSOPrompt_UserDenies(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"lowercase n", "n\n"},
		{"uppercase N", "N\n"},
		{"lowercase no", "no\n"},
		{"empty input", "\n"},
		{"random text", "maybe\n"},
		{"whitespace only", "   \n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdin := io.NopCloser(strings.NewReader(tt.input))
			stderr := &bytes.Buffer{}

			// IsInputTTY requires all three: IsInTTY && IsaTTY && IsErrTTY
			ios := iostreams.New(
				iostreams.WithStdin(stdin, true),
				iostreams.WithStdout(io.Discard, true),
				iostreams.WithStderr(stderr, true),
			)

			prompt := NewSSOPrompt(ios)
			allowed, err := prompt("okta.example.com")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if allowed {
				t.Error("expected allowed to be false")
			}
		})
	}
}

func TestNewSSOPrompt_ReadError(t *testing.T) {
	// Create a reader that returns an error
	stdin := io.NopCloser(&errorReader{})
	stderr := &bytes.Buffer{}

	// IsInputTTY requires all three: IsInTTY && IsaTTY && IsErrTTY
	ios := iostreams.New(
		iostreams.WithStdin(stdin, true),
		iostreams.WithStdout(io.Discard, true),
		iostreams.WithStderr(stderr, true),
	)

	prompt := NewSSOPrompt(ios)
	allowed, err := prompt("okta.example.com")

	if allowed {
		t.Error("expected allowed to be false on read error")
	}
	if err == nil {
		t.Fatal("expected error on read failure, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read response") {
		t.Errorf("expected error about failed read, got: %v", err)
	}
}

// errorReader is a reader that always returns an error
type errorReader struct{}

func (r *errorReader) Read(_ []byte) (int, error) {
	return 0, bytes.ErrTooLarge // arbitrary error
}
