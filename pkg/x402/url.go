package x402

import (
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// ServiceHomeURL normalizes a service or endpoint URL to its public
// registrable domain for human-facing catalog links.
func ServiceHomeURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return ""
	}
	if domain, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil && domain != "" {
		host = domain
	}
	return (&url.URL{Scheme: u.Scheme, Host: host}).String()
}
