package pm

import "gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"

// cargoManager runs cargo through the proxy, trusting the proxy CA via
// CARGO_HTTP_CAINFO. The user's ~/.cargo/config and registries are left
// untouched.
type cargoManager struct{}

// Cargo returns the cargo package manager.
func Cargo() PackageManager { return cargoManager{} }

func (cargoManager) Name() string   { return "cargo" }
func (cargoManager) Binary() string { return "cargo" }

// Environment pins CARGO_HTTP_PROXY, not just the engine's HTTP(S)_PROXY.
// cargo uses an http.proxy from its own config (including a repo-committed
// .cargo/config.toml) or from git config in preference to the proxy env vars,
// and libcurl ignores the uppercase HTTP_PROXY for plaintext URLs, so a
// sparse+http:// registry would connect directly. Either way traffic would
// skip inspection. CARGO_HTTP_PROXY outranks both config sources and covers
// every scheme (verified against cargo 1.98).
func (cargoManager) Environment(proxyURL string) []string {
	return []string{"CARGO_HTTP_PROXY=" + proxyURL}
}

func (cargoManager) CATrustEnviron(caPath string) []string {
	return []string{"CARGO_HTTP_CAINFO=" + caPath}
}

func (cargoManager) ExistingBundleVars() []string { return []string{"CARGO_HTTP_CAINFO"} }

func (cargoManager) CleanupCAFiles(string) {}

func (cargoManager) Matcher() proxy.Matcher { return proxy.CargoMatcher{} }
