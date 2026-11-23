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
// any other cross-origin page gets no free read either. It deliberately
// checks the HOST, not the port: the product's own Vite dev server runs on a
// different loopback port and proxies /api here with changeOrigin:true, so
// pinning the port would break local development. Cross-port browser writes
// are stopped at the transport instead — /api/rpc requires application/json,
// any cross-origin fetch then needs a CORS preflight, and this server grants
// none (no Access-Control-Allow-* headers anywhere).
//
// Sec-Fetch-Site is a third, browser-supplied signal: a REMOTE page reaching
// this loopback address directly reports "cross-site" even when it lies about
// Host/Origin. Non-browser clients (curl, dsh-plugin, the IDE extension) do
// not send the header and are unaffected.
func loopbackGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden host"})
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-site requests are not allowed"})
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
