package http

import nethttp "net/http"

// cliLoginChallenge reuses the browser login nonce and never creates a session.
func (handler *Handler) cliLoginChallenge(response nethttp.ResponseWriter, request *nethttp.Request) {
	response.Header().Set("Cache-Control", "no-store")
	token, err := handler.issueLoginCSRF(response)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writeJSON(response, nethttp.StatusOK, map[string]any{"login_csrf_token": token, "login_path": "/login"})
}

func (handler *Handler) cliSession(response nethttp.ResponseWriter, request *nethttp.Request) {
	response.Header().Set("Cache-Control", "no-store")
	actor, ok := handler.authenticatedActor(response, request)
	if !ok {
		return
	}
	// Authentication already checks the current session version and account state.
	writeJSON(response, nethttp.StatusOK, map[string]any{"kind": "human", "actor_id": actor.InternalID, "roles": actor.Roles, "session_version": actor.SessionVersion, "server_origin": handler.publicOrigin})
}
