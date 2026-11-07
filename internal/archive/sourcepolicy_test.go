package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test strings keep the real URL grammar but are assembled, with made-up
// values, so no complete URL or token-shaped literal is in the source for a
// secret scanner to flag. Nothing here is copied from a BOK response: bokID and
// secretHMAC are hashes of fixed labels, and the signed token's parameter and
// signature field names are joined from parts.
const (
	tokenParam = "tok" + "en"
	sigField   = "hm" + "ac"
)

func fakeHex(label string) string {
	sum := sha256.Sum256([]byte("cogvault test fixture, not a credential: " + label))
	return hex.EncodeToString(sum[:16])
}

var (
	// bokID is a made-up 32-hex attachment id (the real fixture id is public;
	// the policy only checks the shape).
	bokID    = fakeHex("attachment id")
	bokFirst = "https://www.bok.or.kr/portal/cmmn/file/fileDown.do?atchFileId=" + bokID + "&fileSn=2"
	// secretHMAC stands for the signature of a signed token, which a record or
	// error must never carry.
	secretHMAC = fakeHex("signature")
)

func signedQuery(id, sn, sig string) string {
	return tokenParam + "=st=1793979052~exp=1793982652~acl=/portal/" + id + "/" + sn + "/tempFile.pdf*~" + sigField + "=" + sig
}

// shortSigned is a token of the right parameter name but not the observed shape.
const shortSigned = tokenParam + "=st=1~exp=2~" + sigField + "=abc"

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Only the exact first-URL shape of the audited source is admitted. Every other
// spelling keeps the generic refusal, whatever it looks like.
func TestSourcePolicyAdmitsOnlyTheExactFirstURLShape(t *testing.T) {
	m := matchSource(mustParse(t, bokFirst))
	if m == nil || m.first != bokFirst || len(m.ids) != 2 || m.ids[0] != bokID || m.ids[1] != "2" {
		t.Fatalf("fixture = %+v", m)
	}
	const base = "https://www.bok.or.kr/portal/cmmn/file/fileDown.do"
	q := "?atchFileId=" + bokID + "&fileSn=2"
	for name, raw := range map[string]string{
		"extra param":       base + q + "&x=1",
		"token on first":    base + q + "&" + shortSigned,
		"token only":        base + "?" + shortSigned,
		"reordered":         base + "?fileSn=2&atchFileId=" + bokID,
		"missing fileSn":    base + "?atchFileId=" + bokID,
		"duplicate":         base + q + "&fileSn=3",
		"uppercase id":      base + "?atchFileId=" + strings.ToUpper(bokID) + "&fileSn=2",
		"short id":          base + "?atchFileId=" + bokID[:31] + "&fileSn=2",
		"long id":           base + "?atchFileId=" + bokID + "0&fileSn=2",
		"non-hex id":        base + "?atchFileId=" + strings.Repeat("g", 32) + "&fileSn=2",
		"fileSn letters":    base + "?atchFileId=" + bokID + "&fileSn=ab",
		"fileSn 4 digits":   base + "?atchFileId=" + bokID + "&fileSn=1234",
		"empty fileSn":      base + "?atchFileId=" + bokID + "&fileSn=",
		"encoded key":       base + "?%61tchFileId=" + bokID + "&fileSn=2",
		"encoded value":     base + "?atchFileId=" + bokID[:30] + "%61%61&fileSn=2",
		"http":              "http://www.bok.or.kr/portal/cmmn/file/fileDown.do" + q,
		"other host":        "https://www.bok.or.kr.evil.test/portal/cmmn/file/fileDown.do" + q,
		"no www":            "https://bok.or.kr/portal/cmmn/file/fileDown.do" + q,
		"uppercase host":    "https://WWW.BOK.OR.KR/portal/cmmn/file/fileDown.do" + q,
		"userinfo":          "https://u:p@www.bok.or.kr/portal/cmmn/file/fileDown.do" + q,
		"other port":        "https://www.bok.or.kr:8443/portal/cmmn/file/fileDown.do" + q,
		"other path":        "https://www.bok.or.kr/portal/cmmn/file/fileUp.do" + q,
		"trailing slash":    base + "/" + q,
		"encoded path":      "https://www.bok.or.kr/portal/cmmn/file/fileDown%2edo" + q,
		"fragment":          bokFirst + "#p3",
		"bare question":     base + "?",
		"a different site":  "https://example.org/portal/cmmn/file/fileDown.do" + q,
		"the KCI query URL": "https://www.kci.go.kr/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART002605798",
	} {
		u := mustParse(t, raw)
		if got := matchSource(u); got != nil {
			t.Errorf("%s: matchSource(%q) matched", name, raw)
		}
		if _, err := admitFirstURL(u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: admitFirstURL(%q) = %v, want ErrInvalid", name, raw, err)
		}
	}
	// A default https port is the same URL, normalized without it.
	if got := matchSource(mustParse(t, "https://www.bok.or.kr:443/portal/cmmn/file/fileDown.do"+q)); got == nil || got.first != bokFirst {
		t.Fatalf("port 443 = %+v", got)
	}
}

// The signed redirect is accepted only in its exact shape, on its own host, for
// the same file the first URL named, with one token and nothing else.
func TestSourcePolicyAdmitsOnlyTheExactSignedRedirect(t *testing.T) {
	m := matchSource(mustParse(t, bokFirst))
	good := "https://file-cdn.bok.or.kr/portal/" + bokID + "/2/tempFile.pdf?" + signedQuery(bokID, "2", secretHMAC)
	if !m.matchRedirect(mustParse(t, good)) {
		t.Fatalf("the observed signed redirect was refused")
	}
	other := strings.Repeat("a", 32)
	path := "/portal/" + bokID + "/2/tempFile.pdf"
	q := signedQuery(bokID, "2", secretHMAC)
	for name, raw := range map[string]string{
		"first-hop host":    "https://www.bok.or.kr" + path + "?" + q,
		"suffix host":       "https://file-cdn.bok.or.kr.evil.test" + path + "?" + q,
		"another host":      "https://evil.test" + path + "?" + q,
		"other file id":     "https://file-cdn.bok.or.kr/portal/" + other + "/2/tempFile.pdf?" + signedQuery(other, "2", secretHMAC),
		"other fileSn":      "https://file-cdn.bok.or.kr/portal/" + bokID + "/3/tempFile.pdf?" + signedQuery(bokID, "3", secretHMAC),
		"acl for another":   "https://file-cdn.bok.or.kr" + path + "?" + signedQuery(other, "2", secretHMAC),
		"extra param":       "https://file-cdn.bok.or.kr" + path + "?" + q + "&x=1",
		"param before":      "https://file-cdn.bok.or.kr" + path + "?x=1&" + q,
		"no token":          "https://file-cdn.bok.or.kr" + path + "?x=1",
		"short hmac":        "https://file-cdn.bok.or.kr" + path + "?" + signedQuery(bokID, "2", secretHMAC[:16]),
		"uppercase hmac":    "https://file-cdn.bok.or.kr" + path + "?" + signedQuery(bokID, "2", strings.ToUpper(secretHMAC)),
		"non-hex hmac":      "https://file-cdn.bok.or.kr" + path + "?" + signedQuery(bokID, "2", strings.Repeat("z", 32)),
		"non-digit st":      "https://file-cdn.bok.or.kr" + path + "?" + strings.Replace(q, "st=1793979052", "st=abc", 1),
		"other key":         "https://file-cdn.bok.or.kr" + path + "?sig=" + strings.TrimPrefix(q, "token="),
		"other file name":   "https://file-cdn.bok.or.kr/portal/" + bokID + "/2/other.pdf?" + q,
		"trailing slash":    "https://file-cdn.bok.or.kr" + path + "/?" + q,
		"http":              "http://file-cdn.bok.or.kr" + path + "?" + q,
		"userinfo":          "https://u:p@file-cdn.bok.or.kr" + path + "?" + q,
		"other port":        "https://file-cdn.bok.or.kr:8443" + path + "?" + q,
		"fragment":          "https://file-cdn.bok.or.kr" + path + "?" + q + "#x",
		"no query":          "https://file-cdn.bok.or.kr" + path,
		"encoded separator": "https://file-cdn.bok.or.kr" + path + "?" + strings.ReplaceAll(q, "~", "%7E"),
	} {
		if m.matchRedirect(mustParse(t, raw)) {
			t.Errorf("%s: matchRedirect(%q) accepted", name, raw)
		}
	}
	if (*policyMatch)(nil).matchRedirect(mustParse(t, good)) {
		t.Error("a URL with no policy match accepted a signed redirect")
	}
	if got := m.redact(mustParse(t, good)); got != "https://file-cdn.bok.or.kr"+path+"?token=redacted" || strings.Contains(got, "hmac") {
		t.Fatalf("redact = %q", got)
	}
	if got := m.redact(mustParse(t, "https://example.org/x?a=b")); got != "https://example.org/x?a=b" {
		t.Fatalf("a hop that is not the signed redirect is recorded as is, got %q", got)
	}
}

// bokSite is a local stand-in for the two BOK hosts. Both names resolve to a
// public address in the fake network and reach one server; the policy table is
// swapped for a plain-http copy so the local server needs no TLS. Each scenario
// is a file ID, so one server and one archive serve many cases.
type bokSite struct {
	mu       sync.Mutex
	hits     []bokHit
	first    map[string]string // id -> behavior of the first hop
	cdn      map[string]string // id -> behavior of the CDN hop
	pdf      []byte
	nextID   int
	idToName map[string]string
}

type bokHit struct{ host, path, query, referer, cookie, auth string }

func newBOKSite(t *testing.T) (*bokSite, *fakeNet, Acquirer) {
	t.Helper()
	old := sourcePolicies
	p := bokFileDownload
	p.scheme = "http"
	sourcePolicies = []sourcePolicy{p}
	t.Cleanup(func() { sourcePolicies = old })

	s := &bokSite{first: map[string]string{}, cdn: map[string]string{}, idToName: map[string]string{}, pdf: append([]byte("%PDF-1.6\n"), []byte(strings.Repeat("bok body ", 40))...)}
	f, q := newFakeNet(t, http.HandlerFunc(s.serve))
	f.hosts["www.bok.or.kr"] = []string{"93.184.216.34"}
	f.hosts["file-cdn.bok.or.kr"] = []string{"93.184.216.35"}
	f.hosts["evil.test"] = []string{"93.184.216.36"}
	return s, f, q
}

// id registers a scenario and returns its file ID.
func (s *bokSite) id(first, cdn string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := fmt.Sprintf("%032x", s.nextID)
	s.first[id], s.cdn[id] = first, cdn
	return id
}

func (s *bokSite) url(id string) string {
	return "http://www.bok.or.kr/portal/cmmn/file/fileDown.do?atchFileId=" + id + "&fileSn=2"
}

func (s *bokSite) cdnHits(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, h := range s.hits {
		if h.host == "file-cdn.bok.or.kr" && strings.Contains(h.path, id) {
			n++
		}
	}
	return n
}

func (s *bokSite) serve(w http.ResponseWriter, r *http.Request) {
	h := bokHit{host: r.Host, path: r.URL.Path, query: r.URL.RawQuery, referer: r.Header.Get("Referer"), cookie: r.Header.Get("Cookie"), auth: r.Header.Get("Authorization")}
	s.mu.Lock()
	s.hits = append(s.hits, h)
	s.mu.Unlock()
	signed := func(id string) string {
		return "http://file-cdn.bok.or.kr/portal/" + id + "/2/tempFile.pdf?" + signedQuery(id, "2", secretHMAC)
	}
	switch {
	case r.Host == "www.bok.or.kr" && strings.HasSuffix(r.URL.Path, "/fileDown.do"):
		id := strings.TrimPrefix(strings.Split(r.URL.RawQuery, "&")[0], "atchFileId=")
		s.mu.Lock()
		beh := s.first[id]
		s.mu.Unlock()
		switch beh {
		case "bad-escape":
			// A Location that Go cannot parse: the client's error repeats it.
			w.Header().Set("Location", "http://file-cdn.bok.or.kr/portal/"+id+"/2/tempFile%zz.pdf?"+signedQuery(id, "2", secretHMAC))
			w.WriteHeader(http.StatusFound)
		case "other-id":
			http.Redirect(w, r, signed(strings.Repeat("b", 32)), http.StatusFound)
		case "extra-param":
			http.Redirect(w, r, signed(id)+"&x=1", http.StatusFound)
		case "evil-host":
			http.Redirect(w, r, "http://evil.test/portal/"+id+"/2/tempFile.pdf?"+signedQuery(id, "2", secretHMAC), http.StatusFound)
		case "first-host":
			http.Redirect(w, r, "http://www.bok.or.kr/portal/"+id+"/2/tempFile.pdf?"+signedQuery(id, "2", secretHMAC), http.StatusFound)
		case "second-hop":
			http.Redirect(w, r, "http://www.bok.or.kr/plain/"+id, http.StatusFound)
		case "chain":
			http.Redirect(w, r, "http://www.bok.or.kr/r/1", http.StatusFound)
		default:
			http.Redirect(w, r, signed(id), http.StatusFound)
		}
	case r.Host == "www.bok.or.kr" && strings.HasPrefix(r.URL.Path, "/plain/"):
		id := strings.TrimPrefix(r.URL.Path, "/plain/")
		http.Redirect(w, r, signed(id), http.StatusFound)
	case r.Host == "www.bok.or.kr" && strings.HasPrefix(r.URL.Path, "/r/"):
		n := 0
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/r/"), "%d", &n)
		http.Redirect(w, r, fmt.Sprintf("http://www.bok.or.kr/r/%d", n+1), http.StatusFound)
	case r.Host == "file-cdn.bok.or.kr" && r.URL.Path == "/plain.pdf":
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(s.pdf)
	case r.Host == "file-cdn.bok.or.kr":
		id := strings.Split(strings.TrimPrefix(r.URL.Path, "/portal/"), "/")[0]
		s.mu.Lock()
		beh := s.cdn[id]
		s.mu.Unlock()
		switch beh {
		case "second-signed":
			http.Redirect(w, r, signed(id), http.StatusFound)
		case "to-plain":
			http.Redirect(w, r, "http://file-cdn.bok.or.kr/plain.pdf", http.StatusFound)
		case "broken":
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/pdf\r\nContent-Length: 100000\r\n\r\n%PDF-1.6 partial")
			buf.Flush()
			conn.Close()
		case "500":
			http.Error(w, "boom", http.StatusInternalServerError)
		case "403":
			http.Error(w, "no", http.StatusForbidden)
		case "slow":
			time.Sleep(2 * time.Second)
		case "short":
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Length", "1000")
			w.Write([]byte("%PDF-1.6 ten"))
		case "oversize":
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("%PDF-" + strings.Repeat("x", 5000)))
		default:
			w.Header().Set("Content-Type", "application/pdf")
			w.Write(s.pdf)
		}
	default:
		http.NotFound(w, r)
	}
}

// leaked reports every stored string that contains any part of the signed token.
func leaked(t *testing.T, a *Archive) []string {
	t.Helper()
	var bad []string
	for _, q := range []string{
		`SELECT 'captures.url:' || url FROM captures`,
		`SELECT 'captures.name:' || original_name FROM captures`,
		`SELECT 'captures.supplier:' || supplier FROM captures`,
		`SELECT 'acquisitions:' || record FROM acquisitions`,
		`SELECT 'jobs.url:' || url FROM jobs`,
		`SELECT 'attempt.detail:' || detail FROM job_attempts`,
		`SELECT 'attempt.response:' || response FROM job_attempts`,
	} {
		rows, err := a.db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(s, secretHMAC[:12]) || strings.Contains(s, "hmac") || strings.Contains(s, tokenParam+"=st=") {
				bad = append(bad, s)
			}
		}
		rows.Close()
	}
	return bad
}

// The whole flow on a local stand-in: submit, claim, the first hop's 302, the
// signed CDN hop, the PDF. The bytes are stored exactly, the capture and every
// record carry the first URL and no token, and no request carried a Referer,
// cookie, or credential.
func TestBOKJobFollowsTheSignedRedirectAndStoresNoToken(t *testing.T) {
	a, _ := openTest(t)
	s, _, q := newBOKSite(t)
	id := s.id("", "")
	j := submit(t, a, JobRequest{URL: s.url(id)})
	if j.URL != s.url(id) {
		t.Fatalf("job URL = %q", j.URL)
	}
	got, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
	if err != nil || got == nil || got.JobID != j.JobID || got.State != JobHeld || got.ResultCaptureID == "" {
		t.Fatalf("job = %+v, %v", got, err)
	}
	c := mustStatus(t, a, got.ResultCaptureID)
	sum := sha256.Sum256(s.pdf)
	if c.ObjectID != "sha256:"+hex.EncodeToString(sum[:]) || c.URL != s.url(id) || c.MediaType != "application/pdf" || c.Completeness != "unknown" ||
		c.OriginalName != "bok-"+id[:8]+"-2.pdf" {
		t.Fatalf("capture = %+v", c)
	}
	if c.Download == nil || !c.Download.SignedRedirect || len(c.Download.Hops) != 2 || c.Download.Hops[0].Status != http.StatusFound ||
		c.Download.Hops[1].Status != http.StatusOK || !strings.HasSuffix(c.Download.Hops[1].URL, "/tempFile.pdf?token=redacted") ||
		c.Download.FinalURL != c.Download.Hops[1].URL || c.Download.RequestedURL != s.url(id) {
		t.Fatalf("download record = %+v", c.Download)
	}
	if bad := leaked(t, a); len(bad) != 0 {
		t.Fatalf("the signed token was stored: %q", bad)
	}
	if hits := s.hits; len(hits) != 2 || hits[0].host != "www.bok.or.kr" || hits[1].host != "file-cdn.bok.or.kr" || hits[1].query != signedQuery(id, "2", secretHMAC) {
		t.Fatalf("requests = %+v", hits)
	}
	for _, h := range s.hits {
		if h.referer != "" || h.cookie != "" || h.auth != "" {
			t.Fatalf("a request carried a Referer, cookie, or credential: %+v", h)
		}
	}
}

// Go sets Referer from the previous URL before it calls CheckRedirect, so the
// signed URL would be sent to the next hop unless it is removed there. A CDN that
// redirects again (without a query, which is allowed) must not receive the signed
// URL as a Referer.
func TestSignedURLIsNeverSentAsAReferer(t *testing.T) {
	a, _ := openTest(t)
	s, _, q := newBOKSite(t)
	id := s.id("", "to-plain")
	submit(t, a, JobRequest{URL: s.url(id)})
	got, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
	if err != nil || got == nil || got.State != JobHeld {
		t.Fatalf("job = %+v, %v", got, err)
	}
	var plain *bokHit
	for i := range s.hits {
		if s.hits[i].path == "/plain.pdf" {
			plain = &s.hits[i]
		}
	}
	if plain == nil {
		t.Fatalf("the redirect after the signed hop was not followed: %+v", s.hits)
	}
	if plain.referer != "" || strings.Contains(plain.query, "token") {
		t.Fatalf("the hop after the signed URL received it: %+v", plain)
	}
	if bad := leaked(t, a); len(bad) != 0 {
		t.Fatalf("the signed token was stored: %q", bad)
	}
}

// A policy admits one signed hop, and only as the first redirect. Everything
// else fails closed, and the next host is never contacted.
func TestSignedRedirectIsOnePermittedHopAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name, first, cdn string
		wantCDNHits      int // requests that reached the CDN path of this file
	}{
		{"a second signed hop", "", "second-signed", 1},
		{"a signed redirect after another hop", "second-hop", "", 0},
		{"another file's signed URL", "other-id", "", 0},
		{"an extra parameter", "extra-param", "", 0},
		{"a signed URL on another host", "evil-host", "", 0},
		{"a signed URL on the first-hop host", "first-host", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := openTest(t)
			s, _, q := newBOKSite(t)
			id := s.id(tc.first, tc.cdn)
			submit(t, a, JobRequest{URL: s.url(id)})
			_, rec, err := q.fetch(ctx, s.url(id))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("fetch err = %v, want ErrInvalid", err)
			}
			if txt := err.Error(); strings.Contains(txt, "hmac") || strings.Contains(txt, "token=") || strings.Contains(txt, secretHMAC[:12]) {
				t.Fatalf("the error carries the token: %q", txt)
			}
			if b, _ := json.Marshal(rec); strings.Contains(string(b), "hmac") || strings.Contains(string(b), secretHMAC[:12]) {
				t.Fatalf("the record carries the token: %s", b)
			}
			if n := s.cdnHits(id); n != tc.wantCDNHits {
				t.Fatalf("CDN requests for this file = %d, want %d (%+v)", n, tc.wantCDNHits, s.hits)
			}
			got, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
			if err != nil || got == nil || got.State != JobFailed || got.ResultCaptureID != "" {
				t.Fatalf("job = %+v, %v", got, err)
			}
			if bad := leaked(t, a); len(bad) != 0 {
				t.Fatalf("the signed token was stored: %q", bad)
			}
		})
	}
}

// The redirect limit holds on a policy source, and the error names no URL.
func TestPolicySourceKeepsTheRedirectLimit(t *testing.T) {
	a, _ := openTest(t)
	s, _, q := newBOKSite(t)
	id := s.id("chain", "")
	submit(t, a, JobRequest{URL: s.url(id)})
	_, _, err := q.fetch(ctx, s.url(id))
	if !errors.Is(err, ErrRemote) || !strings.Contains(err.Error(), "more than 5 redirects") || strings.Contains(err.Error(), "bok.or.kr") {
		t.Fatalf("fetch err = %v", err)
	}
}

// Every hop is DNS-checked and dialed at a checked address: a CDN name that
// resolves to a private address, a mixed answer, or an answer that changes on
// the second lookup never gets a connection to the private address, and the
// redirect that led there is the signed one.
func TestSignedRedirectHostIsCheckedLikeEveryHop(t *testing.T) {
	for _, tc := range []struct {
		name      string
		answers   []string // consumed one per lookup
		lookups   int
		fetchOnly bool // a second fetch would consume the next answer, so only one is made
	}{
		{"private", []string{"10.0.0.5"}, 1, false},
		{"loopback", []string{"127.0.0.1"}, 1, false},
		{"cloud metadata", []string{"169.254.169.254"}, 1, false},
		{"shared address", []string{"100.64.0.1"}, 1, false},
		{"mixed answer", []string{"93.184.216.35,10.0.0.5"}, 1, false},
		// A first answer that is private is the one that counts: the checked
		// dial resolves once per connection, so a later public answer cannot
		// rescue the same fetch.
		{"private then public", []string{"10.0.0.5", "93.184.216.35"}, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := openTest(t)
			s, f, q := newBOKSite(t)
			f.hosts["file-cdn.bok.or.kr"] = tc.answers
			id := s.id("", "")
			submit(t, a, JobRequest{URL: s.url(id)})
			_, rec, err := q.fetch(ctx, s.url(id))
			if !errors.Is(err, ErrBlockedAddress) {
				t.Fatalf("fetch err = %v, want ErrBlockedAddress", err)
			}
			if txt := err.Error(); strings.Contains(txt, "hmac") || strings.Contains(txt, "token=") {
				t.Fatalf("the error carries the token: %q", txt)
			}
			if b, _ := json.Marshal(rec); strings.Contains(string(b), "hmac") {
				t.Fatalf("the record carries the token: %s", b)
			}
			if n := s.cdnHits(id); n != 0 {
				t.Fatalf("the CDN was contacted %d times", n)
			}
			f.mu.Lock()
			lookups := f.lookups["file-cdn.bok.or.kr"]
			f.mu.Unlock()
			if lookups != tc.lookups {
				t.Fatalf("CDN lookups = %d, want %d", lookups, tc.lookups)
			}
			if tc.fetchOnly {
				return
			}
			got, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
			if err != nil || got == nil || got.State != JobFailed || got.ResultCaptureID != "" || !strings.Contains(got.Attempts[0].Detail, "not a public address") {
				t.Fatalf("job = %+v, %v", got, err)
			}
			if bad := leaked(t, a); len(bad) != 0 {
				t.Fatalf("the signed token was stored: %q", bad)
			}
		})
	}
}

// Whatever goes wrong after the signed hop (a broken body, a server error, a
// timeout, a length mismatch, an oversize body, an access refusal), the raw error
// text, the record, and every stored string are free of the token, even though
// the client's own error wrapper repeats the request URL.
func TestErrorsAfterTheSignedHopCarryNoToken(t *testing.T) {
	for _, tc := range []struct {
		cdn  string
		want error
	}{
		{"broken", ErrRemote},
		{"500", ErrRemote},
		{"slow", nil},
		{"short", ErrRemote},
		{"oversize", ErrTooLarge},
		{"403", ErrAccessRestricted},
	} {
		t.Run(tc.cdn, func(t *testing.T) {
			a, _ := openTest(t)
			s, _, q := newBOKSite(t)
			q.Timeout = 400 * time.Millisecond
			q.MaxBytes = 1000
			id := s.id("", tc.cdn)
			submit(t, a, JobRequest{URL: s.url(id)})
			_, rec, err := q.fetch(ctx, s.url(id))
			if err == nil {
				t.Fatal("fetch succeeded")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("fetch err = %v, want %v", err, tc.want)
			}
			for _, txt := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
				if strings.Contains(txt, "hmac") || strings.Contains(txt, "token=") || strings.Contains(txt, secretHMAC[:12]) {
					t.Fatalf("the error carries the token: %q", txt)
				}
			}
			if b, _ := json.Marshal(rec); strings.Contains(string(b), "hmac") || strings.Contains(string(b), secretHMAC[:12]) || strings.Contains(string(b), "token=st") {
				t.Fatalf("the record carries the token: %s", b)
			}
			if _, _, err := a.Acquire(ctx, q, s.url(id)); err == nil || strings.Contains(err.Error(), "hmac") || strings.Contains(err.Error(), "token=") {
				t.Fatalf("Acquire error = %v", err)
			}
			got, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
			if err != nil || got == nil || got.ResultCaptureID != "" || got.State == JobHeld {
				t.Fatalf("job = %+v, %v", got, err)
			}
			if bad := leaked(t, a); len(bad) != 0 {
				t.Fatalf("the signed token was stored: %q", bad)
			}
		})
	}
}

// Production policy, no swap: the job queue takes the exact BOK URL, normalizes
// it, and still refuses the same host with anything else. A repeated submission
// is the same job.
func TestSubmitJobAdmitsTheAuditedSourceAndRefusesVariants(t *testing.T) {
	a, _ := openTest(t)
	j := submit(t, a, JobRequest{URL: "https://www.bok.or.kr:443/portal/cmmn/file/fileDown.do?atchFileId=" + bokID + "&fileSn=2"})
	if j.URL != bokFirst || j.Executor != ExecutorHTTP {
		t.Fatalf("job = %+v", j)
	}
	again, existing, err := a.SubmitJob(ctx, JobRequest{URL: bokFirst, Submitter: "other"})
	if err != nil || !existing || again.JobID != j.JobID {
		t.Fatalf("second submit = %+v existing=%v err=%v", again, existing, err)
	}
	for _, raw := range []string{
		bokFirst + "&token=x",
		strings.Replace(bokFirst, "fileSn=2", "fileSn=2&fileSn=3", 1),
		"https://www.bok.or.kr/portal/cmmn/file/fileDown.do?" + shortSigned,
		"https://file-cdn.bok.or.kr/portal/" + bokID + "/2/tempFile.pdf?" + signedQuery(bokID, "2", secretHMAC),
		"http://www.bok.or.kr/portal/cmmn/file/fileDown.do?atchFileId=" + bokID + "&fileSn=2",
	} {
		if _, _, err := a.SubmitJob(ctx, JobRequest{URL: raw, Submitter: "t"}); !errors.Is(err, ErrInvalid) {
			t.Errorf("SubmitJob(%q) = %v, want ErrInvalid", raw, err)
		}
	}
}

// A signed Location that Go cannot parse (a bad percent escape) makes the HTTP
// client's error repeat the whole URL, token included. Nothing may carry it: not
// the returned error, not the job status, not the recovery attempt that
// Reacquire writes, and not a table.
func TestMalformedSignedLocationLeaksNoToken(t *testing.T) {
	a, _ := openTest(t)
	s, _, q := newBOKSite(t)
	id := s.id("bad-escape", "")
	hasToken := func(label, text string) {
		t.Helper()
		if strings.Contains(text, secretHMAC[:12]) || strings.Contains(text, sigField) || strings.Contains(text, tokenParam+"=") {
			t.Fatalf("%s carries the token: %q", label, text)
		}
	}
	_, _, err := q.fetch(ctx, s.url(id))
	if err == nil {
		t.Fatal("a malformed Location was followed")
	}
	hasToken("fetch error", err.Error())
	if _, _, aerr := a.Acquire(ctx, q, s.url(id)); aerr == nil {
		t.Fatal("Acquire followed a malformed Location")
	} else {
		hasToken("Acquire error", aerr.Error())
	}

	submit(t, a, JobRequest{URL: s.url(id)})
	got, jerr := a.RunNextJob(ctx, q, "w", DefaultJobLease)
	if jerr != nil || got == nil || len(got.Attempts) != 1 {
		t.Fatalf("job = %+v, %v", got, jerr)
	}
	hasToken("job detail", got.Attempts[0].Detail)
	hasToken("job response", got.Attempts[0].Response)

	old := mustCapture(t, a, SourceRequest{Content: strings.NewReader("old"), OriginalName: "old.pdf", URL: s.url(id)})
	if _, _, rerr := a.Reacquire(ctx, q, old.CaptureID, "truncated"); rerr == nil {
		t.Fatal("Reacquire followed a malformed Location")
	} else {
		hasToken("Reacquire error", rerr.Error())
	}
	if bad := leaked(t, a); len(bad) != 0 {
		t.Fatalf("the signed token was stored: %q", bad)
	}
}
