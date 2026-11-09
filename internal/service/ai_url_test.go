package service

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Regression guard for the localhost→127.0.0.1 fallback in aiGet/aiPostJSON.
//
// The old implementation rewrote the URL with a string replace on "://localhost:".
// Two things went wrong with that:
//   - a base URL already typed as an IP still went through the replace branch and
//     came out as the unparseable "http://127.0.0.1:127.0.0.1:1234/v1"
//   - a path or query containing "localhost" would be rewritten too
//
// The host is now swapped through net/url, so only an actual localhost hostname
// changes and the result must always stay parseable.
func TestAiLocalhostFallback(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" means "no fallback applies"
	}{
		{"localhost with port", "http://localhost:1234/v1/models", "http://127.0.0.1:1234/v1/models"},
		{"uppercase host", "http://LOCALHOST:1234/v1/models", "http://127.0.0.1:1234/v1/models"},
		{"no port", "http://localhost/v1/models", "http://127.0.0.1/v1/models"},
		{"query preserved", "http://localhost:1234/v1/models?a=b", "http://127.0.0.1:1234/v1/models?a=b"},
		// Already an IP: nothing to do. The old code produced the doubled host.
		{"ipv4 literal", "http://127.0.0.1:1235/v1/models", ""},
		{"ipv4 literal 1234", "http://127.0.0.1:1234/v1/models", ""},
		// Remote hosts must never be redirected to the local machine.
		{"remote host", "https://api.openai.com/v1/models", ""},
		{"lan host", "http://192.168.1.50:1234/v1/models", ""},
		// localhost appearing anywhere but the host is not a host match.
		{"localhost in path", "http://example.com/localhost/v1", ""},
		{"localhost in query", "http://example.com/v1?q=localhost", ""},
		{"localhost in path of localhost host", "http://localhost/localhost/v1", "http://127.0.0.1/localhost/v1"},
		{"subdomain is not localhost", "http://notlocalhost:1234/v1", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := aiLocalhostFallback(tc.in)
			if got != tc.want {
				t.Fatalf("aiLocalhostFallback(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if got != "" && strings.Contains(got, "127.0.0.1:127.0.0.1") {
				t.Errorf("fallback produced a doubled host: %q", got)
			}
		})
	}
}

// A base URL typed as an IP must produce usable candidates — the probe used to
// blow up on "invalid port" before it ever reached the network.
func TestListAIModels_IPBaseURLStaysParseable(t *testing.T) {
	// Port 1 is closed, so this fails at connect rather than at parse. The point
	// is that the error is the connection hint, never a malformed-URL error.
	_, err := ListAIModels("http://127.0.0.1:1/v1", "")
	if err == nil {
		t.Fatal("expected an error from a closed port")
	}
	if strings.Contains(err.Error(), "invalid port") || strings.Contains(err.Error(), "invalid URL") {
		t.Errorf("IP base URL produced a parse error instead of a network error: %v", err)
	}
}

// aiProxyFunc must keep AI traffic off a dead local proxy: a loopback target is
// always direct, and a remote target that would have gone through a loopback
// (developer-box) proxy must also go direct. A genuine non-loopback upstream
// proxy is still honored. The env resolver is injected so the case table is
// deterministic and does not depend on the process proxy environment.
func TestAiProxyFuncBypassesLoopbackProxy(t *testing.T) {
	loopbackProxy := &url.URL{Scheme: "http", Host: "127.0.0.1:61865"}
	upstreamProxy := &url.URL{Scheme: "http", Host: "10.0.0.1:8080"}

	cases := []struct {
		name      string
		target    string
		envProxy  *url.URL // value returned by the (injected) env resolver
		wantProxy bool     // true => the env proxy is returned; false => direct (nil)
	}{
		{"loopback target direct regardless of proxy", "http://127.0.0.1:11434/api", upstreamProxy, false},
		{"localhost target direct regardless of proxy", "http://localhost:1234/api", upstreamProxy, false},
		{"remote + loopback proxy -> direct", "https://openrouter.ai/api/v1", loopbackProxy, false},
		{"remote + no proxy -> direct", "https://openrouter.ai/api/v1", nil, false},
		{"remote + non-loopback upstream proxy -> used", "https://openrouter.ai/api/v1", upstreamProxy, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := func(*http.Request) (*url.URL, error) { return tc.envProxy, nil }
			req, err := http.NewRequest(http.MethodGet, tc.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := aiProxyFuncWith(req, resolver)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantProxy && got == nil {
				t.Fatalf("want the upstream proxy, got direct (nil)")
			}
			if !tc.wantProxy && got != nil {
				t.Fatalf("want direct (nil), got proxy %v", got)
			}
		})
	}
}

// isLoopbackHost classifies the host half of an endpoint. Local AI servers
// (Ollama/LM Studio) and the localhost->127.0.0.1 fallback target sit on the
// loopback interface; everything else (LAN, public cloud) does not.
func TestIsLoopbackHost(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"127.0.0.1:11434", true},
		{"[::1]:11434", true},
		{"openrouter.ai", false},
		{"api.openai.com", false},
		{"192.168.1.50", false},
		{"10.0.0.1:8080", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isLoopbackHost(tc.in); got != tc.want {
				t.Fatalf("isLoopbackHost(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
