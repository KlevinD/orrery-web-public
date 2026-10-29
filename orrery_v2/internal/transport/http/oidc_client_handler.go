package httptransport

import (
	"encoding/json"
	"net/http"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
)

type oidcClientListDTO struct {
	Clients []oidcClientDTO `json:"clients"`
}

type oidcClientDTO struct {
	ClientID                string   `json:"client_id"`
	DisplayName             string   `json:"display_name,omitempty"`
	Kind                    string   `json:"kind,omitempty"`
	FirstParty              bool     `json:"first_party"`
	AutoConsent             bool     `json:"auto_consent"`
	AllowedScopes           []string `json:"allowed_scopes"`
	Audience                []string `json:"audience,omitempty"`
	RedirectURIs            []string `json:"redirect_uris,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	ClientSecret            string   `json:"client_secret,omitempty"`
}

func (r *Router) handleListOIDCClients(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	if !r.authorizeIdentityAdmin(w, req, false) {
		return
	}
	if r == nil || r.oidcClientAdmin == nil {
		status, body := MapError(operations.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	clients, err := r.oidcClientAdmin.ListClients(req.Context())
	if err != nil {
		status, body := MapError(identityAdminError(err), cid)
		writeJSON(w, status, body)
		return
	}
	out := oidcClientListDTO{Clients: make([]oidcClientDTO, 0, len(clients))}
	for _, client := range clients {
		out.Clients = append(out.Clients, oidcClient(client))
	}
	writeJSON(w, http.StatusOK, out)
}

func (r *Router) handleSaveOIDCClient(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.identityAdminViewer(w, req, true)
	if !ok {
		return
	}
	if r == nil || r.oidcClientAdmin == nil {
		status, body := MapError(operations.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	var body oidcClientDTO
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	client, err := r.oidcClientAdmin.SaveClient(req.Context(), identity.OIDCClient{
		ClientID:                body.ClientID,
		DisplayName:             body.DisplayName,
		Kind:                    body.Kind,
		FirstParty:              body.FirstParty,
		AutoConsent:             body.AutoConsent,
		AllowedScopes:           body.AllowedScopes,
		Audience:                body.Audience,
		RedirectURIs:            body.RedirectURIs,
		GrantTypes:              body.GrantTypes,
		ResponseTypes:           body.ResponseTypes,
		TokenEndpointAuthMethod: body.TokenEndpointAuthMethod,
		ClientSecret:            body.ClientSecret,
	})
	if err != nil {
		status, response := MapError(identityAdminError(err), cid)
		writeJSON(w, status, response)
		return
	}
	r.recordOIDCClientAudit(req, viewer, client, "oidc.client.upsert")
	writeJSON(w, http.StatusAccepted, oidcClient(client))
}

func (r *Router) recordOIDCClientAudit(req *http.Request, viewer operations.ViewerRef, client identity.OIDCClient, action string) {
	if r == nil || r.auditRecorder == nil || req == nil {
		return
	}
	_ = r.auditRecorder.RecordAuditEvent(req.Context(), operations.AuditEventDTO{
		ActorID:       firstNonEmpty(viewer.ViewerID, "unknown"),
		ViewerID:      viewer.ViewerID,
		EntityType:    "oidc_client",
		EntityID:      client.ClientID,
		Action:        action,
		Result:        "succeeded",
		Severity:      "info",
		CorrelationID: correlationID(req),
		Summary:       action + " " + client.ClientID,
		Metadata: map[string]string{
			"client_id":      client.ClientID,
			"kind":           client.Kind,
			"first_party":    oidcBoolString(client.FirstParty),
			"auto_consent":   oidcBoolString(client.AutoConsent),
			"allowed_scopes": strings.Join(client.AllowedScopes, " "),
			"audience":       strings.Join(client.Audience, " "),
			"redirect_uris":  strings.Join(client.RedirectURIs, " "),
			"grant_types":    strings.Join(client.GrantTypes, " "),
			"response_types": strings.Join(client.ResponseTypes, " "),
		},
	})
}

func oidcClient(client identity.OIDCClient) oidcClientDTO {
	return oidcClientDTO{
		ClientID:                client.ClientID,
		DisplayName:             client.DisplayName,
		Kind:                    client.Kind,
		FirstParty:              client.FirstParty,
		AutoConsent:             client.AutoConsent,
		AllowedScopes:           append([]string(nil), client.AllowedScopes...),
		Audience:                append([]string(nil), client.Audience...),
		RedirectURIs:            append([]string(nil), client.RedirectURIs...),
		GrantTypes:              append([]string(nil), client.GrantTypes...),
		ResponseTypes:           append([]string(nil), client.ResponseTypes...),
		TokenEndpointAuthMethod: client.TokenEndpointAuthMethod,
		ClientSecret:            client.ClientSecret,
	}
}

func oidcBoolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
