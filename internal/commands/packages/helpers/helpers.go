package helpers

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
)

const DefaultPackageType = "generic"

// PackageTypes mirrors the package_type enum in GitLab's Packages::Package model.
var PackageTypes = []string{
	"cargo",
	"composer",
	"conan",
	"debian",
	"generic",
	"golang",
	"helm",
	"maven",
	"ml_model",
	"npm",
	"nuget",
	"pypi",
	"rpm",
	"rubygems",
	"terraform_module",
}

// GetPackageByName returns the one package matching name, type and version.
// The server filters by name as a substring, so matches are re-checked exactly.
// It errors when several packages match rather than picking one to delete.
func GetPackageByName(ctx context.Context, client *gitlab.Client, packageName, packageType, packageVersion, repoName string) (*gitlab.Package, error) {
	packages, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Package, *gitlab.Response, error) {
		return client.Packages.ListProjectPackages(repoName, &gitlab.ListProjectPackagesOptions{
			ListOptions:    gitlab.ListOptions{Page: 1, PerPage: api.MaxPerPage},
			PackageName:    &packageName,
			PackageType:    &packageType,
			PackageVersion: &packageVersion,
		}, p, gitlab.WithContext(ctx))
	})
	if err != nil {
		return nil, fmt.Errorf("error fetching packages: %w", err)
	}

	var matches []*gitlab.Package
	for _, pkg := range packages {
		if pkg.Name == packageName && pkg.PackageType == packageType && pkg.Version == packageVersion {
			matches = append(matches, pkg)
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("couldn't locate package %q with package-type %q and version %q", packageName, packageType, packageVersion)
	case 1:
		return matches[0], nil
	}

	ids := make([]string, len(matches))
	for i, pkg := range matches {
		ids[i] = strconv.FormatInt(pkg.ID, 10)
	}
	return nil, fmt.Errorf("found %d packages named %q with package-type %q and version %q (IDs: %s)",
		len(matches), packageName, packageType, packageVersion, strings.Join(ids, ", "))
}
