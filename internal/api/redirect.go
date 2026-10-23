package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"gitlab.com/gitlab-org/cli/internal/config"
)

// refuseMethodChangingRedirect is the redirect policy of the HTTP client.
//
// net/http follows a 301, 302 or 303 answer to a POST, PUT, PATCH or DELETE
// by sending a GET without the body. Such an answer usually comes from a
// proxy in front of the instance that moves http to https, and the GET then
// reaches a different endpoint: a create fails to decode the list it gets
// back, and an update reports success without changing anything. Redirects
// that keep the method, such as a 307 or 308, are still followed.
func refuseMethodChangingRedirect(req *http.Request, via []*http.Request) error {
	// Setting CheckRedirect replaces the default policy, loop limit included.
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}

	original := via[0]
	if req.Method == original.Method {
		return nil
	}

	err := fmt.Errorf("refusing to follow the %d redirect from %s, which would resend this %s request as a %s without its body",
		req.Response.StatusCode, via[len(via)-1].URL.Redacted(), original.Method, req.Method)
	if original.URL.Scheme == "http" && req.URL.Scheme == "https" && original.URL.Hostname() == req.URL.Hostname() && httpsPort(original.URL) == httpsPort(req.URL) {
		return fmt.Errorf("%w; the server redirects http to https, %s", err, apiProtocolHint(original.URL.Host))
	}
	return err
}

// httpsPort is the port a request to u reaches over https. Setting api_protocol
// to https keeps the host's port, so it only helps when the redirect lands on
// the same one.
func httpsPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	return "443"
}

// apiProtocolHint names the setting glab reads api_protocol from. It takes the
// host with its port, because that is how a per-host key is stored for a host
// with one. Under CI auto-login neither that key nor GLAB_API_PROTOCOL is read.
func apiProtocolHint(host string) string {
	protocolVar := config.EnvKeyEquivalence("api_protocol")[0]
	if config.CIAutologinEnabled() {
		return fmt.Sprintf("and CI auto-login reads api_protocol from %s, so give the instance an https external_url, or set %s to https in the job", protocolVar, protocolVar)
	}
	return fmt.Sprintf("so set api_protocol to https with `glab config set api_protocol https --host %s` or %s=https", host, protocolVar)
}
