package config

import (
	"fmt"
	"net/url"
	"strings"
)

// NormalizeClientURL validates and canonicalizes a daemon origin. An empty URL
// is left empty so destination resolution can distinguish an explicit client
// origin from the server-derived compatibility default.
func NormalizeClientURL(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("client.url must be an absolute HTTP or HTTPS origin: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("client.url scheme must be http or https")
	}
	if !u.IsAbs() || u.Opaque != "" || u.Host == "" || u.Hostname() == "" {
		return "", fmt.Errorf("client.url must be an absolute HTTP or HTTPS origin with a host")
	}
	if u.User != nil {
		return "", fmt.Errorf("client.url must not include userinfo")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", fmt.Errorf("client.url must not include a query")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return "", fmt.Errorf("client.url must not include a fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("client.url must not include a path prefix")
	}

	u.Scheme = scheme
	u.Path = ""
	u.RawPath = ""
	return u.String(), nil
}
