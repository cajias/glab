//go:build !integration

package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests swap the process-wide merger, so they must not run in parallel.

func TestWrite_PassesHostEntriesToCredentialMerger(t *testing.T) {
	t.Cleanup(func() { SetCredentialMerger(nil) })

	dir := t.TempDir()
	seed := NewBlankConfigInDir(dir)
	require.NoError(t, seed.Set("gitlab.com", "api_host", "disk.example.com"))
	require.NoError(t, seed.Write())

	cfg, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	require.NoError(t, cfg.Set("gitlab.com", "api_host", "memory.example.com"))

	var calls int
	var memHost, diskHost, memValue, diskValue string
	SetCredentialMerger(func(mem, disk *HostConfig) {
		calls++
		memHost = mem.Host
		diskHost = disk.Host
		memValue, _ = mem.GetStringValue("api_host")
		diskValue, _ = disk.GetStringValue("api_host")
		require.NoError(t, mem.SetStringValue("api_host", "merged.example.com"))
	})

	require.NoError(t, cfg.Write())

	assert.Equal(t, 1, calls)
	assert.Equal(t, "gitlab.com", memHost)
	assert.Equal(t, "gitlab.com", diskHost)
	assert.Equal(t, "memory.example.com", memValue)
	assert.Equal(t, "disk.example.com", diskValue)

	reloaded, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	got, err := reloaded.Get("gitlab.com", "api_host")
	require.NoError(t, err)
	assert.Equal(t, "merged.example.com", got)
}

func TestWrite_WithoutCredentialMerger(t *testing.T) {
	t.Cleanup(func() { SetCredentialMerger(nil) })
	SetCredentialMerger(nil)

	dir := t.TempDir()
	seed := NewBlankConfigInDir(dir)
	require.NoError(t, seed.Set("gitlab.com", "api_host", "disk.example.com"))
	require.NoError(t, seed.Write())

	cfg, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	require.NoError(t, cfg.Set("gitlab.com", "api_host", "memory.example.com"))
	require.NoError(t, cfg.Write())

	reloaded, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	got, err := reloaded.Get("gitlab.com", "api_host")
	require.NoError(t, err)
	assert.Equal(t, "memory.example.com", got)
}

func TestWrite_InMemoryConfigSkipsCredentialMerger(t *testing.T) {
	t.Cleanup(func() { SetCredentialMerger(nil) })

	var calls int
	SetCredentialMerger(func(*HostConfig, *HostConfig) {
		calls++
	})

	cfg := NewBlankConfig()
	require.NoError(t, cfg.Set("gitlab.com", "api_host", "memory.example.com"))
	require.NoError(t, cfg.Write())

	assert.Equal(t, 0, calls)
}
