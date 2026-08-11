package httpauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testJWK mirrors the wire shape of a JSON Web Key well enough to build stub
// JWKS responses. It is deliberately independent of the jwk type in jwks.go
// so the test encodes keys the same way a real IdP would, not the way the
// cache happens to decode them.
type testJWK struct {
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Kid string `json:"kid"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
}

func b64(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func rsaJWK(kid string, pub *rsa.PublicKey) testJWK {
	return testJWK{
		Kty: "RSA",
		Use: "sig",
		Kid: kid,
		N:   b64(pub.N.Bytes()),
		E:   b64(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func ecJWK(kid string, pub *ecdsa.PublicKey) testJWK {
	size := (pub.Curve.Params().BitSize + 7) / 8
	return testJWK{
		Kty: "EC",
		Use: "sig",
		Kid: kid,
		Crv: "P-256",
		X:   b64(pub.X.FillBytes(make([]byte, size))),
		Y:   b64(pub.Y.FillBytes(make([]byte, size))),
	}
}

// genRSAKey generates a fresh 2048-bit RSA key, failing the test on error.
func genRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return key
}

// genECKey generates a fresh P-256 ECDSA key, failing the test on error.
func genECKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	return key
}

// stubServer serves a discovery document and a JWKS document from one
// httptest.Server, counting requests to each so tests can assert exact fetch
// counts.
type stubServer struct {
	srv           *httptest.Server
	discoveryHits int64
	jwksHits      int64
	keys          []testJWK
}

func newStubServer(t *testing.T, keys []testJWK) *stubServer {
	t.Helper()
	s := &stubServer{keys: keys}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&s.discoveryHits, 1)
		fmt.Fprintf(w, `{"issuer":"%s","jwks_uri":"%s/jwks.json"}`, s.srv.URL, s.srv.URL)
	})
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&s.jwksHits, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": s.keys})
	})
	s.srv = httptest.NewTLSServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func TestJWKSHappyPathRSAAndEC(t *testing.T) {
	rsaKey := genRSAKey(t)
	ecKey := genECKey(t)

	stub := newStubServer(t, []testJWK{
		rsaJWK("rsa-1", &rsaKey.PublicKey),
		ecJWK("ec-1", &ecKey.PublicKey),
	})
	cache := NewJWKSCache(stub.srv.URL, time.Hour, stub.srv.Client())

	got, err := cache.KeyFor(context.Background(), "rsa-1")
	if err != nil {
		t.Fatalf("KeyFor(rsa-1): %v", err)
	}
	gotRSA, ok := got.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("KeyFor(rsa-1) returned %T, want *rsa.PublicKey", got)
	}
	if gotRSA.N.Cmp(rsaKey.PublicKey.N) != 0 {
		t.Errorf("modulus mismatch: got %s want %s", gotRSA.N, rsaKey.PublicKey.N)
	}
	if gotRSA.E != rsaKey.PublicKey.E {
		t.Errorf("exponent mismatch: got %d want %d", gotRSA.E, rsaKey.PublicKey.E)
	}

	got2, err := cache.KeyFor(context.Background(), "ec-1")
	if err != nil {
		t.Fatalf("KeyFor(ec-1): %v", err)
	}
	gotEC, ok := got2.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("KeyFor(ec-1) returned %T, want *ecdsa.PublicKey", got2)
	}
	if gotEC.X.Cmp(ecKey.PublicKey.X) != 0 || gotEC.Y.Cmp(ecKey.PublicKey.Y) != 0 {
		t.Errorf("EC point mismatch: got (%s,%s) want (%s,%s)", gotEC.X, gotEC.Y, ecKey.PublicKey.X, ecKey.PublicKey.Y)
	}
}

func TestJWKSCachedWithinTTLNoRefetch(t *testing.T) {
	rsaKey := genRSAKey(t)
	stub := newStubServer(t, []testJWK{rsaJWK("k1", &rsaKey.PublicKey)})
	cache := NewJWKSCache(stub.srv.URL, time.Hour, stub.srv.Client())

	if _, err := cache.KeyFor(context.Background(), "k1"); err != nil {
		t.Fatalf("first KeyFor: %v", err)
	}
	if _, err := cache.KeyFor(context.Background(), "k1"); err != nil {
		t.Fatalf("second KeyFor: %v", err)
	}
	if got := atomic.LoadInt64(&stub.jwksHits); got != 1 {
		t.Errorf("jwks hits = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&stub.discoveryHits); got != 1 {
		t.Errorf("discovery hits = %d, want 1", got)
	}
}

// TestJWKSUnknownKidRefetchesOnceThenFloorSuppresses proves both the
// forced-refetch-on-unknown-kid behavior and the minimum-refetch-interval
// floor: a second unknown kid requested immediately afterward (well inside
// the 60s floor window) must not trigger another fetch.
func TestJWKSUnknownKidRefetchesOnceThenFloorSuppresses(t *testing.T) {
	rsaKey := genRSAKey(t)
	stub := newStubServer(t, []testJWK{rsaJWK("k1", &rsaKey.PublicKey)})
	cache := NewJWKSCache(stub.srv.URL, time.Hour, stub.srv.Client())

	if _, err := cache.KeyFor(context.Background(), "k1"); err != nil {
		t.Fatalf("priming KeyFor: %v", err)
	}
	if got := atomic.LoadInt64(&stub.jwksHits); got != 1 {
		t.Fatalf("priming jwks hits = %d, want 1", got)
	}

	if _, err := cache.KeyFor(context.Background(), "unknown-1"); err == nil {
		t.Fatal("KeyFor(unknown-1): want error, got nil")
	}
	if got := atomic.LoadInt64(&stub.jwksHits); got != 2 {
		t.Fatalf("after unknown kid, jwks hits = %d, want 2 (exactly one refetch)", got)
	}

	// Still inside the 60s floor window: must not fetch again.
	if _, err := cache.KeyFor(context.Background(), "unknown-2"); err == nil {
		t.Fatal("KeyFor(unknown-2): want error, got nil")
	}
	if got := atomic.LoadInt64(&stub.jwksHits); got != 2 {
		t.Errorf("floor window: jwks hits = %d, want still 2 (floor must suppress refetch)", got)
	}
	if got := atomic.LoadInt64(&stub.discoveryHits); got != 2 {
		t.Errorf("floor window: discovery hits = %d, want still 2", got)
	}
}

// TestJWKSConcurrentUnknownKidSingleFetch is the concurrency requirement:
// fifty goroutines racing on an unknown kid must collapse onto exactly one
// JWKS fetch. Run with -race.
func TestJWKSConcurrentUnknownKidSingleFetch(t *testing.T) {
	rsaKey := genRSAKey(t)
	stub := newStubServer(t, []testJWK{rsaJWK("k1", &rsaKey.PublicKey)})
	cache := NewJWKSCache(stub.srv.URL, time.Hour, stub.srv.Client())

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, _ = cache.KeyFor(context.Background(), "unknown-kid")
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&stub.jwksHits); got != 1 {
		t.Errorf("jwks hits = %d, want exactly 1", got)
	}
	if got := atomic.LoadInt64(&stub.discoveryHits); got != 1 {
		t.Errorf("discovery hits = %d, want exactly 1", got)
	}
}

func TestJWKSRejectsNonHTTPSJWKSURI(t *testing.T) {
	var discoveryHits, jwksHits int64
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&discoveryHits, 1)
		fmt.Fprintf(w, `{"issuer":"%s","jwks_uri":"http://evil.example.com/jwks.json"}`, srv.URL)
	})
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&jwksHits, 1)
		w.WriteHeader(http.StatusOK)
	})
	srv = httptest.NewTLSServer(mux)
	defer srv.Close()

	cache := NewJWKSCache(srv.URL, time.Hour, srv.Client())
	if _, err := cache.KeyFor(context.Background(), "any-kid"); err == nil {
		t.Fatal("KeyFor: want error for http jwks_uri, got nil")
	}
	if got := atomic.LoadInt64(&discoveryHits); got != 1 {
		t.Errorf("discovery hits = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&jwksHits); got != 0 {
		t.Errorf("jwks hits = %d, want 0 (must reject before fetching)", got)
	}
}

func TestJWKSMalformedBase64ReturnsErrorNotPanic(t *testing.T) {
	stub := newStubServer(t, []testJWK{
		{Kty: "RSA", Use: "sig", Kid: "bad-1", N: "not-valid-base64!!!", E: b64(big.NewInt(65537).Bytes())},
	})
	cache := NewJWKSCache(stub.srv.URL, time.Hour, stub.srv.Client())

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("KeyFor panicked: %v", r)
		}
	}()
	if _, err := cache.KeyFor(context.Background(), "bad-1"); err == nil {
		t.Fatal("KeyFor(bad-1): want error, got nil")
	}
}

// TestJWKSSkipsUnsupportedKeyTypeWithoutFailingFetch exercises the resolved
// ambiguity that an unsupported kty in the set is skipped, not fatal to the
// whole fetch.
func TestJWKSSkipsUnsupportedKeyTypeWithoutFailingFetch(t *testing.T) {
	rsaKey := genRSAKey(t)
	stub := newStubServer(t, []testJWK{
		{Kty: "oct", Kid: "sym-1"},
		rsaJWK("rsa-1", &rsaKey.PublicKey),
	})
	cache := NewJWKSCache(stub.srv.URL, time.Hour, stub.srv.Client())

	if _, err := cache.KeyFor(context.Background(), "rsa-1"); err != nil {
		t.Fatalf("KeyFor(rsa-1) with unsupported sibling key present: %v", err)
	}
}

func TestJWKSEmptyKidIsError(t *testing.T) {
	cache := NewJWKSCache("https://example.com", time.Hour, http.DefaultClient)
	if _, err := cache.KeyFor(context.Background(), ""); err == nil {
		t.Fatal(`KeyFor(""): want error, got nil`)
	}
}

func TestNewJWKSCacheDefaultsClientTimeout(t *testing.T) {
	cache := NewJWKSCache("https://example.com", time.Hour, nil)
	if cache.client == nil {
		t.Fatal("client is nil")
	}
	if cache.client.Timeout <= 0 {
		t.Errorf("client.Timeout = %v, want > 0", cache.client.Timeout)
	}
}

func TestDiscoveryURLForJoinsPathCorrectly(t *testing.T) {
	cases := []struct {
		issuer string
		want   string
	}{
		{"https://auth.example.com", "https://auth.example.com/.well-known/openid-configuration"},
		{"https://auth.example.com/", "https://auth.example.com/.well-known/openid-configuration"},
		{"https://auth.example.com/tenant", "https://auth.example.com/tenant/.well-known/openid-configuration"},
		{"https://auth.example.com/tenant/", "https://auth.example.com/tenant/.well-known/openid-configuration"},
	}
	for _, c := range cases {
		got, err := discoveryURLFor(c.issuer)
		if err != nil {
			t.Fatalf("discoveryURLFor(%q): %v", c.issuer, err)
		}
		if got != c.want {
			t.Errorf("discoveryURLFor(%q) = %q, want %q", c.issuer, got, c.want)
		}
	}
}

// TestJWKSRejectsHTTPSToHTTPRedirect proves the https check on jwks_uri
// cannot be bypassed by a redirect: the discovered jwks_uri itself is https
// (so it passes the scheme check), but the https endpoint responds with a
// 302 to a plaintext target. The plaintext server's hit counter proves the
// redirect was never followed.
func TestJWKSRejectsHTTPSToHTTPRedirect(t *testing.T) {
	var plaintextHits int64
	plaintextSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&plaintextHits, 1)
		fmt.Fprint(w, `{"keys":[]}`)
	}))
	defer plaintextSrv.Close()

	var tlsSrv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"issuer":"%s","jwks_uri":"%s/jwks.json"}`, tlsSrv.URL, tlsSrv.URL)
	})
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plaintextSrv.URL+"/jwks.json", http.StatusFound)
	})
	tlsSrv = httptest.NewTLSServer(mux)
	defer tlsSrv.Close()

	cache := NewJWKSCache(tlsSrv.URL, time.Hour, tlsSrv.Client())
	if _, err := cache.KeyFor(context.Background(), "any-kid"); err == nil {
		t.Fatal("KeyFor: want error when jwks_uri redirects to http, got nil")
	}
	if got := atomic.LoadInt64(&plaintextHits); got != 0 {
		t.Errorf("plaintext server hits = %d, want 0 (redirect must not be followed)", got)
	}
}

// TestJWKSRejectsOversizedBody proves fetchJSON bounds how much of a
// response body it will read: a jwks.json body far larger than the cap must
// fail decoding rather than being read in full.
func TestJWKSRejectsOversizedBody(t *testing.T) {
	stub := newStubServerWithJWKSBody(t, func(w http.ResponseWriter) {
		fmt.Fprint(w, `{"keys":[{"kty":"RSA","kid":"`)
		fmt.Fprint(w, strings.Repeat("a", maxJWKSResponseBytes+1024))
		fmt.Fprint(w, `"}]}`)
	})
	cache := NewJWKSCache(stub.srv.URL, time.Hour, stub.srv.Client())

	if _, err := cache.KeyFor(context.Background(), "any-kid"); err == nil {
		t.Fatal("KeyFor: want error for oversized jwks body, got nil")
	}
}

// newStubServerWithJWKSBody is like newStubServer but lets the caller write
// an arbitrary jwks.json response body, for tests that need to control the
// body's size or shape rather than serve a well-formed key set.
func newStubServerWithJWKSBody(t *testing.T, writeBody func(w http.ResponseWriter)) *stubServer {
	t.Helper()
	s := &stubServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&s.discoveryHits, 1)
		fmt.Fprintf(w, `{"issuer":"%s","jwks_uri":"%s/jwks.json"}`, s.srv.URL, s.srv.URL)
	})
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&s.jwksHits, 1)
		writeBody(w)
	})
	s.srv = httptest.NewTLSServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

// TestJWKSSkipsAndRejectsMalformedKeys is a table-style test covering the
// skip-not-fatal and reject-single-key paths that TestJWKSSkipsUnsupportedKeyTypeWithoutFailingFetch
// alone did not exercise: every prior test JWK set use to "sig", so an
// inverted use check would still have passed. It also covers the M1 (RSA e
// overflow) and M2 (EC point not on curve) decode fixes.
func TestJWKSSkipsAndRejectsMalformedKeys(t *testing.T) {
	rsaKey := genRSAKey(t)
	ecKey := genECKey(t)

	encOnly := rsaJWK("enc-1", &rsaKey.PublicKey)
	encOnly.Use = "enc"

	unknownCurve := ecJWK("unknown-crv-1", &ecKey.PublicKey)
	unknownCurve.Crv = "P-999"

	emptyCoords := testJWK{Kty: "EC", Use: "sig", Kid: "empty-coord-1", Crv: "P-256"}

	oversizedE := rsaJWK("oversized-e-1", &rsaKey.PublicKey)
	oversizedE.E = b64([]byte{1, 2, 3, 4, 5})

	offCurve := testJWK{
		Kty: "EC",
		Use: "sig",
		Kid: "off-curve-1",
		Crv: "P-256",
		X:   b64(big.NewInt(1).FillBytes(make([]byte, 32))),
		Y:   b64(big.NewInt(1).FillBytes(make([]byte, 32))),
	}

	stub := newStubServer(t, []testJWK{
		rsaJWK("good-1", &rsaKey.PublicKey),
		encOnly,
		unknownCurve,
		emptyCoords,
		oversizedE,
		offCurve,
	})
	cache := NewJWKSCache(stub.srv.URL, time.Hour, stub.srv.Client())

	if _, err := cache.KeyFor(context.Background(), "good-1"); err != nil {
		t.Fatalf("KeyFor(good-1): control key with sibling malformed keys present: %v", err)
	}

	for _, kid := range []string{"enc-1", "unknown-crv-1", "empty-coord-1", "oversized-e-1", "off-curve-1"} {
		t.Run(kid, func(t *testing.T) {
			if _, err := cache.KeyFor(context.Background(), kid); err == nil {
				t.Fatalf("KeyFor(%s): want error (key skipped at decode), got nil", kid)
			}
		})
	}
}

// TestJWKSFailedFetchFloorSuppressesRetry proves the M3 fix: when the issuer
// is unreachable (or has never answered successfully), the minimum-refetch
// floor still bounds retries. Before the fix, fetchedAt stays zero forever
// on a down issuer, so "fresh" and "forced" were always false and the floor
// never engaged, letting every KeyFor call drive a fresh round trip.
func TestJWKSFailedFetchFloorSuppressesRetry(t *testing.T) {
	var discoveryHits int64
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&discoveryHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	cache := NewJWKSCache(srv.URL, time.Hour, srv.Client())

	if _, err := cache.KeyFor(context.Background(), "any-kid"); err == nil {
		t.Fatal("KeyFor: want error from failing issuer, got nil")
	}
	if got := atomic.LoadInt64(&discoveryHits); got != 1 {
		t.Fatalf("discovery hits after first failure = %d, want 1", got)
	}

	if _, err := cache.KeyFor(context.Background(), "any-kid"); err == nil {
		t.Fatal("KeyFor: want error from failing issuer, got nil")
	}
	if got := atomic.LoadInt64(&discoveryHits); got != 1 {
		t.Errorf("discovery hits after second failure = %d, want still 1 (floor must suppress retry)", got)
	}
}

// TestJWKSLeaderCancellationDoesNotFailOtherWaiters proves the M4 fix: the
// goroutine that becomes the in-flight fetch leader has its context canceled
// mid-fetch, but other callers collapsed onto the same fetch (with their own
// live contexts) must still get the key, not the leader's cancellation
// error.
func TestJWKSLeaderCancellationDoesNotFailOtherWaiters(t *testing.T) {
	rsaKey := genRSAKey(t)

	handlerEntered := make(chan struct{})
	release := make(chan struct{})
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"issuer":"%s","jwks_uri":"%s/jwks.json"}`, srv.URL, srv.URL)
	})
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		close(handlerEntered)
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []testJWK{rsaJWK("k1", &rsaKey.PublicKey)}})
	})
	srv = httptest.NewTLSServer(mux)
	defer srv.Close()

	cache := NewJWKSCache(srv.URL, time.Hour, srv.Client())

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	go func() {
		_, _ = cache.KeyFor(leaderCtx, "k1")
	}()
	<-handlerEntered // the leader is now the in-flight fetcher, blocked on release

	const n = 5
	var wg sync.WaitGroup
	results := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, results[i] = cache.KeyFor(context.Background(), "k1")
		}(i)
	}
	time.Sleep(20 * time.Millisecond) // let the waiters reach the ch-wait select
	cancelLeader()
	close(release)
	wg.Wait()

	for i, err := range results {
		if err != nil {
			t.Errorf("waiter %d: KeyFor returned error after leader's context was canceled: %v", i, err)
		}
	}
}

func TestDiscoveryURLForRejectsMalformedIssuer(t *testing.T) {
	cases := []string{
		"http://auth.example.com",
		"https://",
		"https://auth.example.com/?x=1",
		"https://auth.example.com/?",
		"https://auth.example.com/#f",
		"https://user:pass@auth.example.com",
	}
	for _, issuer := range cases {
		if _, err := discoveryURLFor(issuer); err == nil {
			t.Errorf("discoveryURLFor(%q): want error, got nil", issuer)
		}
	}
}
