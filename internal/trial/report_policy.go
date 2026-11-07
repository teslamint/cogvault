package trial

import (
	"encoding/json"
	"net/url"

	"github.com/teslamint/cogvault/internal/archive"
)

func parseReportPolicy(env map[string]string, c *Config) error {
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

func (s *Service) reportURLAllowed(raw string) bool {
	return s.checkURL(raw) == nil && containsURL(s.Cfg.AllowedURLs, raw)
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
