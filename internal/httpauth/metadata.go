package httpauth

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// wellKnownPRMPath is the well-known path for the OAuth 2.0 Protected
// Resource Metadata document (RFC 9728). Mount serves it, and the
// path-suffixed form wellKnownPRMPath+cfg.EndpointPath, from an exact-match
// dispatcher; see Mount's doc comment (auth.go) for why a prefix match here
// would be an authorization bypass.
const wellKnownPRMPath = "/.well-known/oauth-protected-resource"

// protectedResourceMetadata is the RFC 9728 Protected Resource Metadata
// document cogvault publishes in "oauth" mode. ScopesSupported is omitted
// entirely, rather than serialized as an empty array, when cfg.RequiredScopes
// is empty.
type protectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

// MetadataHandler returns an http.Handler that serves cfg's Protected
// Resource Metadata document. It responds to GET and HEAD only; any other
// method returns 405 without further processing, so a metadata route's
// behavior never depends on which verb is used to reach it.
func MetadataHandler(cfg Config) http.Handler {
	doc := protectedResourceMetadata{
		Resource:               cfg.PublicURL + cfg.EndpointPath,
		AuthorizationServers:   []string{cfg.Issuer},
		ScopesSupported:        cfg.RequiredScopes,
		BearerMethodsSupported: []string{"header"},
	}
	body, err := json.Marshal(doc)
	if err != nil {
		panic(fmt.Sprintf("httpauth: marshal protected resource metadata: %v", err))
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
		default:
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	})
}
