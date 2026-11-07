package trial

import (
	"errors"
	"net/url"
	"testing"
)

func TestReportPolicyDefaultsAndStrictConfig(t *testing.T) {
	c := mustConfig(t, newRoot(t), nil)
	if c.AllowHTML || c.MaxRedirects != 5 || len(c.AllowedURLs) != 0 || len(c.RedirectURLs) != 0 {
		t.Fatalf("defaults widened: %+v", c)
	}
	for _, over := range []map[string]string{
		{EnvMediaTypes: "all"}, {EnvMaxRedirects: "6"}, {EnvMaxRedirects: "-1"},
		{EnvAllowedURLs: `["https://pdf.test/a.pdf", "https://pdf.test/a.pdf"]`},
		{EnvAllowedURLs: `["https://other.test/a.pdf"]`},
		{EnvAllowedURLs: `["http://pdf.test/a.pdf"]`},
		{EnvAllowedURLs: `["https://pdf.test/a.pdf?token=secret"]`},
		{EnvAllowedURLs: `["https://pdf.test/a.pdf#frag"]`},
		{EnvAllowedURLs: `["https://u:p@pdf.test/a.pdf"]`},
		{EnvAllowedURLs: `["https://pdf.test/a.pdf"] {}`},
		{EnvRedirectURLs: `["https://pdf.test/a.pdf?token=secret"]`},
		{EnvRedirectURLs: `["https://pdf.test/a.pdf"]`},
	} {
		if _, err := FromEnviron(env(newRoot(t), over)); !errors.Is(err, ErrConfig) {
			t.Fatalf("expected refused config for keys %v: %v", over, err)
		}
	}
	c = mustConfig(t, newRoot(t), map[string]string{EnvMediaTypes: "pdf,html", EnvMaxRedirects: "1", EnvAllowedURLs: `["https://pdf.test/page"]`, EnvRedirectURLs: `["https://pdf.test/final"]`})
	s := &Service{Cfg: c}
	if !s.reportURLAllowed("https://pdf.test/page") || s.reportURLAllowed("https://pdf.test/unlisted") || !c.AllowHTML || c.MaxRedirects != 1 {
		t.Fatalf("exact policy not enforced: %+v", c)
	}
	for raw, want := range map[string]bool{"https://pdf.test/page": true, "https://pdf.test/final": true, "https://pdf.test/unlisted": false, "https://other.test/page": false, "http://pdf.test/page": false} {
		u, _ := url.Parse(raw)
		if got := s.acquirer().AllowURL(u); got != want {
			t.Fatalf("AllowURL(%q)=%v want %v", raw, got, want)
		}
	}
	if s.acquirer().PDFOnly || !s.acquirer().StopOnRateLimit || s.acquirer().MaxRedirects == nil || *s.acquirer().MaxRedirects != 1 {
		t.Fatal("worker policy hooks missing")
	}
}

func TestReportPolicyAuditedBOKRedirect(t *testing.T) {
	first := "https://www.bok.or.kr/portal/cmmn/file/fileDown.do?atchFileId=4826d55c88464526b2c36e125afd84fd&fileSn=2"
	final := "https://file-cdn.bok.or.kr/portal/4826d55c88464526b2c36e125afd84fd/2/tempFile.pdf"
	c := mustConfig(t, newRoot(t), map[string]string{EnvAllowedHosts: "www.bok.or.kr,file-cdn.bok.or.kr", EnvAllowedURLs: `["` + first + `"]`, EnvRedirectURLs: `["` + final + `"]`, EnvMaxRedirects: "1"})
	s := &Service{Cfg: c}
	if !s.reportURLAllowed(first) || s.reportURLAllowed(final) {
		t.Fatal("source and redirect authority confused")
	}
	u, _ := url.Parse(final + "?token=temporary")
	if !s.acquirer().AllowURL(u) {
		t.Fatal("stable redirect destination should reach archive's stricter signed-token validator")
	}
	u.Path = "/portal/other/2/tempFile.pdf"
	if s.acquirer().AllowURL(u) {
		t.Fatal("different redirect destination allowed")
	}
}
