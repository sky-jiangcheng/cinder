package httpapi

import (
	"net/http"
	"net/url"
	"repotory/internal/aiclient"
)

// loopbackGuard pins the API to requests that actually name the local
// machine. Binding to 127.0.0.1 (cmd/server) makes the API unreachable from
// the network, but not from the user's *browser*: with DNS rebinding a remote
// page's domain re-resolves to 127.0.0.1 mid-session and the browser happily
// sends the knowledge base across, because from its point of view the request
// is same-origin. The Host header — set by the browser from the page's URL,
// not from the real dial destination — is what betrays the trick, so it must
// be pinned to loopback names. Any non-browser local client (dsh-plugin,
// curl) already addresses the server by 127.0.0.1 or localhost.
//
// The Origin check is defence in depth: a rebinding page that has passed the
// Host pin still announces its true origin on state-changing requests, and
// any other cross-origin page gets no free read either.
func loopbackGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden host"})
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !isLoopbackHost(u.Host) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin requests are not allowed"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopbackHost reports whether a Host/Origin authority ("host:port" or bare
// host) resolves to this machine. An empty authority (HTTP/1.0 without Host)
// is rejected: every legitimate client sends one.
// isLoopbackHost delegates to the single definition in internal/aiclient; the
// copy that used to live here is gone (ADR-0064).
func isLoopbackHost(hostport string) bool { return aiclient.IsLoopbackHost(hostport) }
