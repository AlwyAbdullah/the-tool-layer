package auth

import (
	"encoding/json"
	"net/http"
)

// ResourceMetadata is the RFC 9728 Protected Resource Metadata document
// an MCP server publishes so clients can find its authorization server.
type ResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
	BearerMethods        []string `json:"bearer_methods_supported"`
}

// MetadataHandler serves the document at /.well-known/oauth-protected-
// resource (plus the resource path, if any). Keep ScopesSupported to
// the minimal set for basic use; ask for more with a scope challenge
// when a privileged tool is first called.
func MetadataHandler(m ResourceMetadata) http.Handler {
	if m.BearerMethods == nil {
		m.BearerMethods = []string{"header"}
	}
	body, _ := json.Marshal(m)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
}
