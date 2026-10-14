package graph

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestServer_RunReturnsWhenContextCancelled(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // make browser.OpenURL fail instead of opening a real browser

	ios, _, _, _ := cmdtest.TestIOStreams()
	s := &server{
		io:         ios,
		listenNet:  "tcp",
		listenAddr: "127.0.0.1:0",
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
