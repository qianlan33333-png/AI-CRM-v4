package main

import (
	"net/http"
	"strings"
)

func mountInvitations(next, handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/group_chat_picker.js") {
			clone := r.Clone(r.Context())
			u := *r.URL
			u.Path = "/static/admin_console/invitation_picker.js"
			clone.URL = &u
			next.ServeHTTP(w, clone)
			return
		}
		if r.URL.Path == "/api/admin/group-invitations" || strings.HasPrefix(r.URL.Path, "/api/admin/group-invitations/") || r.URL.Path == "/api/admin/group-directory" || strings.HasPrefix(r.URL.Path, "/api/admin/group-directory/") || strings.HasPrefix(r.URL.Path, "/gi/") {
			handler.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
