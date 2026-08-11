package httpauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// defaultJWKSClientTimeout bounds every discovery and JWKS request made with
// a caller-supplied nil *http.Client. http.DefaultClient has no timeout, and
// an issuer that stalls (or is targeted by an attacker) must not be able to
// hang a request indefinitely.
const defaultJWKSClientTimeout = 10 * time.Second

// minForcedRefetchInterval bounds how often KeyFor may force a refetch for a
// kid that is not in the cache while the cached set is still within its TTL.
// Without this floor, a stream of tokens carrying unknown kid values could
// drive unbounded outbound requests to the issuer.
const minForcedRefetchInterval = 60 * time.Second

// discoveryDocument is the subset of an OIDC discovery document this cache
// needs.
type discoveryDocument struct {
	JWKSURI string `json:"jwks_uri"`
}

// jwk is a single JSON Web Key as served by a JWKS endpoint.
type jwk struct {
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Kid string `json:"kid"`
	// RSA members.
	N string `json:"n,omitempty"`
	E string `json:"e,omitempty"`
	// EC members.
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

// JWKSCache resolves a signing key by kid, fetching and caching an issuer's
// JWKS document per OIDC discovery. It is the sole basis for trusting a
// bearer token in oauth mode: the discovery document and the JWKS document
// it points to are both untrusted content fetched at request time, not
// trusted config, so every fetch enforces https on the discovered jwks_uri
// and every decode fails closed rather than panicking.
type JWKSCache struct {
	issuer string
	ttl    time.Duration
	client *http.Client

	mu         sync.Mutex
	keys       map[string]crypto.PublicKey
	fetchedAt  time.Time
	lastForced time.Time
	fetching   chan struct{}
	lastErr    error
}

// NewJWKSCache builds a cache for issuer's key set. client is injected so
// tests can point it at a stub server; a nil client gets a default timeout
// rather than http.DefaultClient, which has none.
func NewJWKSCache(issuer string, ttl time.Duration, client *http.Client) *JWKSCache {
	if client == nil {
		client = &http.Client{Timeout: defaultJWKSClientTimeout}
	}
	return &JWKSCache{
		issuer: issuer,
		ttl:    ttl,
		client: client,
		keys:   make(map[string]crypto.PublicKey),
	}
}

// KeyFor resolves the public key for kid, fetching (or refetching) the JWKS
// document as needed. Concurrent misses for the same cache instance collapse
// onto a single in-flight fetch. kid is required: an empty kid is always an
// error, never a fallback to "try every key".
func (c *JWKSCache) KeyFor(ctx context.Context, kid string) (crypto.PublicKey, error) {
	if kid == "" {
		return nil, fmt.Errorf("httpauth: kid must not be empty")
	}

	c.mu.Lock()
	fresh := !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < c.ttl
	if key, ok := c.keys[kid]; ok && fresh {
		c.mu.Unlock()
		return key, nil
	}

	if ch := c.fetching; ch != nil {
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		c.mu.Lock()
		key, ok := c.keys[kid]
		fetchErr := c.lastErr
		c.mu.Unlock()
		if fetchErr != nil {
			return nil, fetchErr
		}
		if !ok {
			return nil, fmt.Errorf("httpauth: no signing key found for kid %q", kid)
		}
		return key, nil
	}

	// The cache is fresh but kid is missing: this is a forced refetch, and
	// subject to the floor. A stale cache (TTL expired) refetches freely.
	forced := fresh
	if forced && !c.lastForced.IsZero() && time.Since(c.lastForced) < minForcedRefetchInterval {
		c.mu.Unlock()
		return nil, fmt.Errorf("httpauth: no signing key found for kid %q", kid)
	}

	ch := make(chan struct{})
	c.fetching = ch
	c.mu.Unlock()

	keys, err := c.fetchKeys(ctx)

	c.mu.Lock()
	if err == nil {
		c.keys = keys
		c.fetchedAt = time.Now()
	}
	c.lastErr = err
	if forced {
		c.lastForced = time.Now()
	}
	c.fetching = nil
	close(ch)
	var key crypto.PublicKey
	var ok bool
	if err == nil {
		key, ok = c.keys[kid]
	}
	c.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("httpauth: no signing key found for kid %q", kid)
	}
	return key, nil
}

// fetchKeys performs the discovery and JWKS HTTP round trips and decodes the
// result. It touches no cache state, so it needs no lock; callers commit the
// result under c.mu.
func (c *JWKSCache) fetchKeys(ctx context.Context) (map[string]crypto.PublicKey, error) {
	discoveryURL, err := discoveryURLFor(c.issuer)
	if err != nil {
		return nil, err
	}

	var doc discoveryDocument
	if err := c.fetchJSON(ctx, discoveryURL, &doc); err != nil {
		return nil, fmt.Errorf("httpauth: fetching discovery document: %w", err)
	}

	jwksURL, err := url.Parse(doc.JWKSURI)
	if err != nil {
		return nil, fmt.Errorf("httpauth: jwks_uri %q is not a valid URL: %w", doc.JWKSURI, err)
	}
	if jwksURL.Scheme != "https" {
		return nil, fmt.Errorf("httpauth: jwks_uri %q must use https", doc.JWKSURI)
	}

	var jwks jwksDocument
	if err := c.fetchJSON(ctx, jwksURL.String(), &jwks); err != nil {
		return nil, fmt.Errorf("httpauth: fetching jwks document: %w", err)
	}

	keys := make(map[string]crypto.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		// A present use other than "sig" excludes the key. alg is
		// intentionally not checked: algorithm enforcement belongs to the
		// token parser, and duplicating it here would create two places to
		// keep in sync.
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		var pub crypto.PublicKey
		var decodeErr error
		switch k.Kty {
		case "RSA":
			pub, decodeErr = decodeRSAPublicKey(k)
		case "EC":
			pub, decodeErr = decodeECPublicKey(k)
		default:
			// An unsupported kty is skipped, not fatal: the key set may
			// legitimately contain a type this server does not use.
			continue
		}
		if decodeErr != nil {
			// A malformed key of a supported type is also skipped rather
			// than failing the whole fetch; KeyFor reports "not found" if
			// the requested kid happens to be this one.
			continue
		}
		keys[k.Kid] = pub
	}
	return keys, nil
}

// fetchJSON performs a GET against url and decodes the JSON body into out.
func (c *JWKSCache) fetchJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// discoveryURLFor builds the OIDC discovery URL for issuer. It owns the
// check that turns a trusted-but-unvalidated config string into a URL: the
// issuer must be an https URL with a host and no query, fragment, or
// userinfo, so a malformed issuer fails with a clear error here rather than
// producing a malformed /.well-known/... request. The join is normalized so
// an issuer with or without a trailing slash yields exactly one "/" before
// ".well-known".
func discoveryURLFor(issuer string) (string, error) {
	u, err := url.Parse(issuer)
	if err != nil {
		return "", fmt.Errorf("httpauth: issuer %q is not a valid URL: %w", issuer, err)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("httpauth: issuer %q must use https", issuer)
	}
	if u.Host == "" {
		return "", fmt.Errorf("httpauth: issuer %q has no host", issuer)
	}
	if u.RawQuery != "" {
		return "", fmt.Errorf("httpauth: issuer %q must not carry a query", issuer)
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("httpauth: issuer %q must not carry a fragment", issuer)
	}
	if u.User != nil {
		return "", fmt.Errorf("httpauth: issuer %q must not carry userinfo", issuer)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/.well-known/openid-configuration"
	return u.String(), nil
}

// ecCurves maps the JWK "crv" values this cache supports to their
// crypto/elliptic curves. A crv outside this set is rejected.
var ecCurves = map[string]elliptic.Curve{
	"P-256": elliptic.P256(),
	"P-384": elliptic.P384(),
	"P-521": elliptic.P521(),
}

// decodeRSAPublicKey builds an *rsa.PublicKey from a JWK's n and e members.
// JWK values are unpadded base64url, so this decodes with
// base64.RawURLEncoding, not base64.URLEncoding: a padding mismatch would
// otherwise produce a decode failure or, worse, a subtly wrong modulus.
func decodeRSAPublicKey(k jwk) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("httpauth: jwk %q has malformed n: %w", k.Kid, err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("httpauth: jwk %q has malformed e: %w", k.Kid, err)
	}
	if len(nBytes) == 0 || len(eBytes) == 0 {
		return nil, fmt.Errorf("httpauth: jwk %q has empty n or e", k.Kid)
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(new(big.Int).SetBytes(eBytes).Int64()),
	}, nil
}

// decodeECPublicKey builds an *ecdsa.PublicKey from a JWK's crv, x, and y
// members, mapping crv to its crypto/elliptic curve and rejecting any curve
// not in ecCurves.
func decodeECPublicKey(k jwk) (*ecdsa.PublicKey, error) {
	curve, ok := ecCurves[k.Crv]
	if !ok {
		return nil, fmt.Errorf("httpauth: jwk %q has unsupported curve %q", k.Kid, k.Crv)
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, fmt.Errorf("httpauth: jwk %q has malformed x: %w", k.Kid, err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, fmt.Errorf("httpauth: jwk %q has malformed y: %w", k.Kid, err)
	}
	if len(xBytes) == 0 || len(yBytes) == 0 {
		return nil, fmt.Errorf("httpauth: jwk %q has empty x or y", k.Kid)
	}
	return &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}, nil
}
