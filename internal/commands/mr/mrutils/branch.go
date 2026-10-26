package mrutils

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

// maxBranchSuffix bounds the numbered names CreateUniqueBranch tries, so a
// server that keeps answering that the branch exists cannot hang the command.
const maxBranchSuffix = 100

// CreateUniqueBranch creates the branch from ref and returns its name. When an
// existing branch takes that name, it tries branch-1, branch-2 and so on instead.
func CreateUniqueBranch(ctx context.Context, client *gitlab.Client, repo, branch, ref string) (string, error) {
	name := branch
	for suffix := 1; ; suffix++ {
		_, _, err := client.Branches.CreateBranch(repo, &gitlab.CreateBranchOptions{Branch: new(name), Ref: new(ref)}, gitlab.WithContext(ctx))
		if err == nil {
			return name, nil
		}
		if suffix > maxBranchSuffix || !isBranchExistsError(err) {
			return "", fmt.Errorf("could not create branch %q: %w", name, err)
		}
		name = fmt.Sprintf("%s-%d", branch, suffix)
	}
}

// isBranchExistsError reports whether err is the API's refusal of a name that
// an existing branch takes: the same name, or a branch below it (<name>/...).
// An invalid name is refused with the same status, so only the message tells
// them apart.
func isBranchExistsError(err error) bool {
	var errResp *gitlab.ErrorResponse
	if !errors.As(err, &errResp) || !errResp.HasStatusCode(http.StatusBadRequest) {
		return false
	}
	return strings.Contains(errResp.Message, "already exists") ||
		strings.Contains(errResp.Message, "conflicts with existing branch hierarchy")
}
