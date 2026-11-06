package archive

import (
	"net/url"
	"regexp"
)

// Audited source policies (ADR 0027, "Audited source policies").
//
// The HTTP acquirer refuses every query, because cogvault cannot tell a
// credential from an ordinary parameter. A source policy is the one narrow
// exception: it names a URL shape that is known to carry only public
// identifiers, and the exact signed redirect that URL is known to produce. A
// URL that does not match a policy keeps the blanket refusal, so adding a
// source is a code change that is reviewed here, never a setting or a request
// field.
//
// A policy is matched on the raw query string with a fixed-shape expression, not
// by parsing parameters, so there is no ordering, duplication, or encoding
// slack: a different spelling of the same parameters is refused. The signed
// redirect must go to the policy's own host, must name the same identifiers as
// the first URL, and must carry one token of the observed shape and nothing
// else. The token is a short-lived signature the site issued for that exact file:
// it is never stored, logged, or sent anywhere but the CDN host it was issued
// for, and no further redirect may carry a query.
type sourcePolicy struct {
	name   string
	scheme string
	host   string
	path   string
	// query matches the whole raw query of the first URL. Its capture groups are
	// the identifiers the redirect must repeat.
	query *regexp.Regexp
	// redirectHost is the only host the signed redirect may name.
	redirectHost string
	// redirectPath and redirectQuery build the exact path and the exact raw query
	// of the signed redirect from the identifiers captured in query.
	redirectPath  func(ids []string) string
	redirectQuery func(ids []string) *regexp.Regexp
	// originalName names the stored file from the identifiers, so captures of
	// different files do not all carry the CDN's generic file name.
	originalName func(ids []string) string
}

// bokFileDownload is the Bank of Korea public file download:
//
//	https://www.bok.or.kr/portal/cmmn/file/fileDown.do?atchFileId=<32 hex>&fileSn=<n>
//
// which answers 302 to a CDN URL for the same file with a signed token:
//
//	https://file-cdn.bok.or.kr/portal/<atchFileId>/<fileSn>/tempFile.pdf?token=st=<epoch>~exp=<epoch>~acl=/portal/<atchFileId>/<fileSn>/tempFile.pdf*~hmac=<hex>
//
// observed on 2026-11-06 (see ADR 0027). The token's acl names the same file path
// the redirect URL names; the policy requires that, so a redirect cannot point at
// a different file.
var bokFileDownload = sourcePolicy{
	name:         "bok-file-download",
	scheme:       "https",
	host:         "www.bok.or.kr",
	path:         "/portal/cmmn/file/fileDown.do",
	query:        regexp.MustCompile(`^atchFileId=([0-9a-f]{32})&fileSn=([0-9]{1,3})$`),
	redirectHost: "file-cdn.bok.or.kr",
	redirectPath: func(ids []string) string { return "/portal/" + ids[0] + "/" + ids[1] + "/tempFile.pdf" },
	redirectQuery: func(ids []string) *regexp.Regexp {
		path := regexp.QuoteMeta("/portal/" + ids[0] + "/" + ids[1] + "/tempFile.pdf")
		return regexp.MustCompile(`^token=st=[0-9]{1,12}~exp=[0-9]{1,12}~acl=` + path + `\*~hmac=[0-9a-f]{32,128}$`)
	},
	originalName: func(ids []string) string { return "bok-" + ids[0][:8] + "-" + ids[1] + ".pdf" },
}

// sourcePolicies is the whole list. It is a variable only so a test in this
// package can swap in a plain-http copy for a local server; nothing else
// writes it.
var sourcePolicies = []sourcePolicy{bokFileDownload}

// policyMatch is a first URL that matched a policy.
type policyMatch struct {
	p   *sourcePolicy
	ids []string
	// first is the normalized first URL: scheme, host, path, and the matched query.
	first string
}

func defaultPort(scheme, port string) bool {
	return port == "" || (scheme == "https" && port == "443") || (scheme == "http" && port == "80")
}

// matchSource returns the policy match for a first URL, or nil. It never
// returns a match for a URL with credentials, a fragment, an encoded path, a
// different host or scheme, or a query that is not exactly the policy's shape.
func matchSource(u *url.URL) *policyMatch {
	if u.RawQuery == "" || u.ForceQuery {
		return nil
	}
	for i := range sourcePolicies {
		p := &sourcePolicies[i]
		if u.Scheme != p.scheme || u.User != nil || u.Hostname() != p.host || !defaultPort(u.Scheme, u.Port()) ||
			u.Path != p.path || u.RawPath != "" || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" {
			continue
		}
		ids := p.query.FindStringSubmatch(u.RawQuery)
		if ids == nil {
			continue
		}
		return &policyMatch{p: p, ids: ids[1:], first: p.scheme + "://" + p.host + p.path + "?" + u.RawQuery}
	}
	return nil
}

// matchRedirect reports whether u is exactly the signed redirect this policy
// allows for the matched first URL.
func (m *policyMatch) matchRedirect(u *url.URL) bool {
	if m == nil || u.RawQuery == "" || u.ForceQuery {
		return false
	}
	p := m.p
	return u.Scheme == p.scheme && u.User == nil && u.Hostname() == p.redirectHost && defaultPort(u.Scheme, u.Port()) &&
		u.Path == p.redirectPath(m.ids) && u.RawPath == "" && u.Fragment == "" && u.RawFragment == "" && u.Opaque == "" &&
		p.redirectQuery(m.ids).MatchString(u.RawQuery)
}

// redact is how a hop URL is recorded: the signed redirect keeps its scheme,
// host, and path and loses the token; every other URL is recorded as is. It
// works on a nil receiver.
func (m *policyMatch) redact(u *url.URL) string {
	if m.matchRedirect(u) {
		return u.Scheme + "://" + u.Host + u.Path + "?token=redacted"
	}
	return u.String()
}

// storedName is the file name for a captured source, or "" when the policy gives none.
func (m *policyMatch) storedName() string {
	if m == nil || m.p.originalName == nil {
		return ""
	}
	return m.p.originalName(m.ids)
}

// admitFirstURL is checkAcquireURL for the first URL of an acquisition or a
// job: a URL with no query takes the generic checks, and a URL with a query is
// admitted only when it matches a source policy.
func admitFirstURL(u *url.URL) (*policyMatch, error) {
	if m := matchSource(u); m != nil {
		return m, nil
	}
	return nil, checkAcquireURL(u)
}
