package trialmcp

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/httpauth"
)

// MaxRequestBytes bounds a single MCP message, independently of tool arguments.
const MaxRequestBytes = 16 << 10

var endpointPattern = regexp.MustCompile(`^(/[A-Za-z0-9_-]+)+$`)

// ValidateHTTPConfig refuses anonymous transport, ambiguous routes and mixed
// authentication settings. It never consults production configuration.
func ValidateHTTPConfig(cfg httpauth.Config) error {
	u, err := url.Parse(cfg.PublicURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return errors.New("trial HTTP requires a pathless public HTTPS URL")
	}
	p := cfg.EndpointPath
	if !endpointPattern.MatchString(p) || p == "" || p == "/" || !strings.HasPrefix(p, "/") || path.Clean(p) != p || strings.HasSuffix(p, "/") || strings.ContainsAny(p, "%?#\\ \t\r\n") || strings.HasPrefix(p, "/.well-known") {
		return errors.New("trial HTTP requires an exact clean endpoint path")
	}
	switch cfg.Mode {
	case "bearer":
		if len(cfg.BearerToken) < 32 || len(cfg.BearerToken) > 4096 || strings.ContainsAny(cfg.BearerToken, " \t\r\n") || cfg.Validator != nil || cfg.Issuer != "" || len(cfg.RequiredScopes) != 0 {
			return errors.New("trial HTTP requires only a nonempty bearer credential of at least 32 bytes")
		}
	case "oauth":
		issuer, e := url.Parse(cfg.Issuer)
		if e != nil || issuer.Scheme != "https" || issuer.Hostname() == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" || cfg.Validator == nil || cfg.BearerToken != "" || len(cfg.RequiredScopes) == 0 {
			return errors.New("trial HTTP requires an HTTPS issuer, validator and trial scopes")
		}
		for _, scope := range cfg.RequiredScopes {
			if scope == "" || strings.ContainsAny(scope, " \t\r\n\"\\") || strings.IndexFunc(scope, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
				return errors.New("trial OAuth scopes must be nonempty tokens")
			}
		}
	default:
		return errors.New("trial HTTP requires bearer or oauth authentication")
	}
	return nil
}

// NewHTTPHandler mounts one stateless, bounded Streamable HTTP endpoint. Only
// the suffixed discovery route belongs to this resource; the host's bare
// well-known route may belong to a different (production) resource.
func NewHTTPHandler(s *server.MCPServer, cfg httpauth.Config) (http.Handler, error) {
	if err := ValidateHTTPConfig(cfg); err != nil {
		return nil, err
	}
	cfg.MaxBodyBytes = MaxRequestBytes
	cfg.MaxStreamSeconds = 30
	stream := server.NewStreamableHTTPServer(s, server.WithEndpointPath(cfg.EndpointPath), server.WithStateLess(true))
	protected := httpauth.Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Read under the middleware cap before the library parser so body
		// errors cannot expose arbitrary reader or transport diagnostics.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		stream.ServeHTTP(w, r)
	}))
	discovery := "/.well-known/oauth-protected-resource" + cfg.EndpointPath
	metadata := httpauth.MetadataHandler(cfg)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if cfg.Mode == "oauth" && r.URL.Path == discovery {
			metadata.ServeHTTP(w, r)
			return
		}
		if r.URL.Path != cfg.EndpointPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		protected.ServeHTTP(&challengeWriter{ResponseWriter: w, resource: cfg.PublicURL + discovery, bare: cfg.PublicURL + "/.well-known/oauth-protected-resource"}, r)
	}), nil
}

type challengeWriter struct {
	http.ResponseWriter
	resource, bare string
}

func (w *challengeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *challengeWriter) Flush()                      { _ = http.NewResponseController(w.ResponseWriter).Flush() }
func (w *challengeWriter) WriteHeader(code int) {
	if value := w.Header().Get("WWW-Authenticate"); value != "" {
		w.Header().Set("WWW-Authenticate", strings.ReplaceAll(value, `resource_metadata="`+w.bare+`"`, `resource_metadata="`+w.resource+`"`))
	}
	w.ResponseWriter.WriteHeader(code)
}
