package add

import (
	"context"
	"time"

	"golang.org/x/crypto/ssh"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

func UploadSSHKey(ctx context.Context, client *gitlab.Client, title, key, usage_type string, expiresAt *gitlab.ISOTime) error {
	sshKeyAddOptions := &gitlab.AddSSHKeyOptions{
		Title:     &title,
		Key:       &key,
		UsageType: &usage_type,
		ExpiresAt: expiresAt,
	}

	_, _, err := client.Users.AddSSHKey(sshKeyAddOptions, gitlab.WithContext(ctx))
	return err
}

func parseExpiresAt(expiresAt string) (*gitlab.ISOTime, error) {
	layout := time.RFC3339
	if len(expiresAt) < len(layout) {
		layout = layout[:len(expiresAt)]
	}
	parsed, err := time.Parse(layout, expiresAt)
	if err != nil {
		return nil, err
	}
	return (*gitlab.ISOTime)(&parsed), nil
}

// findSSHKeyByFingerprint returns nil without an error when no key on the
// account matches.
func findSSHKeyByFingerprint(ctx context.Context, client *gitlab.Client, fingerprint string) (*gitlab.SSHKey, error) {
	keys := gitlab.Scan2(func(p gitlab.PaginationOptionFunc) ([]*gitlab.SSHKey, *gitlab.Response, error) {
		return client.Users.ListSSHKeys(&gitlab.ListSSHKeysOptions{}, p, gitlab.WithContext(ctx))
	})
	for key, err := range keys {
		if err != nil {
			return nil, err
		}
		existing, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key.Key))
		if err != nil {
			dbg.Debugf("skipping SSH key %d while matching fingerprints: %v", key.ID, err)
			continue
		}
		if ssh.FingerprintSHA256(existing) == fingerprint {
			return key, nil
		}
	}
	return nil, nil
}
