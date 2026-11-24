package trial

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/teslamint/cogvault/internal/archive"
)

// ReportURLPolicySelected is the opt-in mode where report-manifest-linked
// sources are admitted by exact URL equality against the manifest acquire_url,
// with HTTPS-only, no credentials, default port, no query/fragment,
// no IP-literal or special-use-local hostnames. Legacy exact allowlist mode
// is the default ("" or absent).
const ReportURLPolicySelected = "selected_public_https"

// selectedPublicHTTPS is a presentation-layer check. The archive transport
// independently rejects every nonpublic DNS answer and pins the checked dial.
func selectedPublicHTTPS(raw string) bool {
	if !safeManifestURL(raw) || strings.ContainsAny(raw, "?#") || archive.CheckHTTPSourceURL(raw) != nil {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	for _, suffix := range []string{".localhost", ".local", ".localdomain", ".internal", ".example", ".test", ".invalid", ".onion", ".home.arpa", ".lan", ".corp", ".alt"} {
		if host == suffix[1:] || strings.HasSuffix(host, suffix) {
			return false
		}
	}
	return !isKCI(host)
}

func parseReportPolicy(env map[string]string, c *Config) error {
	switch env[EnvReportURLPolicy] {
	case "", ReportURLPolicySelected:
		c.ReportURLPolicy = env[EnvReportURLPolicy]
	default:
		return configErr(EnvReportURLPolicy, "must be empty or selected_public_https")
	}
	switch env[EnvMediaTypes] {
	case "", "pdf":
	case "pdf,html":
		c.AllowHTML = true
	default:
		return configErr(EnvMediaTypes, "must be pdf or pdf,html")
	}
	n, err := parseInt(env, EnvMaxRedirects, 5, 0, 5)
	if err != nil {
		return err
	}
	c.MaxRedirects = n
	for _, item := range []struct {
		key string
		dst *[]string
	}{{EnvAllowedURLs, &c.AllowedURLs}, {EnvRedirectURLs, &c.RedirectURLs}} {
		raw := env[item.key]
		if raw == "" {
			continue
		}
		if len(raw) > 16<<10 || json.Unmarshal([]byte(raw), item.dst) != nil || len(*item.dst) == 0 || len(*item.dst) > 10 {
			return configErr(item.key, "must be a JSON array of 1 to 10 exact HTTPS URLs, at most 16 KiB")
		}
		seen := map[string]bool{}
		for _, rawURL := range *item.dst {
			u, e := url.Parse(rawURL)
			if e != nil || len(rawURL) > maxURLLength || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.Fragment != "" || u.RawFragment != "" || u.ForceQuery || u.RawPath != "" || (u.Port() != "" && u.Port() != "443") || !c.HostAllowed(u.Hostname()) || seen[rawURL] || archive.CheckHTTPSourceURL(rawURL) != nil {
				return configErr(item.key, "contains a duplicate or refused URL")
			}
			if item.key == EnvRedirectURLs && u.RawQuery != "" {
				return configErr(item.key, "redirect destinations must omit queries and temporary signatures")
			}
			seen[rawURL] = true
		}
	}
	if len(c.RedirectURLs) > 0 && len(c.AllowedURLs) == 0 {
		return configErr(EnvRedirectURLs, "requires an exact initial URL policy")
	}
	return nil
}

func containsURL(urls []string, raw string) bool {
	for _, allowed := range urls {
		if allowed == raw {
			return true
		}
	}
	return false
}

func (s *Service) reportExactURLAllowed(raw string) bool {
	return s.checkURL(raw) == nil && containsURL(s.Cfg.AllowedURLs, raw)
}

func (s *Service) reportSelectedURLAllowed(raw string) bool {
	return s.Cfg.ReportURLPolicy == ReportURLPolicySelected && selectedPublicHTTPS(raw)
}

func (s *Service) reportURLAllowed(raw string) bool {
	return s.reportExactURLAllowed(raw) || s.reportSelectedURLAllowed(raw)
}

func (s *Service) fetchURLAllowed(u *url.URL) bool {
	if s.checkURL(u.String()) != nil {
		return false
	}
	if len(s.Cfg.AllowedURLs) == 0 {
		return true // unchanged legacy host-only policy
	}
	if containsURL(s.Cfg.AllowedURLs, u.String()) {
		return true
	}
	// This admits only an operator-selected destination. The archive separately
	// validates every redirect's full query, including its single audited signed
	// redirect policy. No signature is accepted as an initial submission here.
	v := *u
	v.RawQuery = ""
	v.ForceQuery = false
	return containsURL(s.Cfg.RedirectURLs, v.String())
}

func (c Config) mediaDescription() string {
	if c.AllowHTML {
		return "pdf,html"
	}
	return "pdf"
}
