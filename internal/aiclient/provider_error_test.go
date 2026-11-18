package aiclient

import (
	"encoding/json"
	"strings"
	"testing"
)

// A gateway can answer HTTP 200 with an error envelope and no choices at all.
// OpenRouter does this when the upstream provider's worker pool is saturated:
// the body carries {"error":{"message":"...ResourceExhausted..."},"metadata":
// {"error_type":"provider_unavailable"}} and no choices array. The old code
// only looked at Choices, so the provider's own explanation was thrown away
// and the user saw "AI endpoint returned no choices" — which names neither the
// cause nor the remedy, and reads as a broken wiring.

func TestProviderErrorRendering(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
		none bool
	}{
		{
			name: "no error envelope",
			body: `{"choices":[]}`,
			none: true,
		},
		{
			name: "message only",
			body: `{"error":{"message":"model not loaded"}}`,
			want: []string{"model not loaded"},
		},
		{
			name: "error type only",
			body: `{"error":{"metadata":{"error_type":"provider_unavailable"}}}`,
			want: []string{"provider_unavailable"},
		},
		{
			name: "message and error type",
			body: `{"error":{"message":"Worker local total request limit reached (16/16)","metadata":{"error_type":"provider_unavailable"}}}`,
			want: []string{"provider_unavailable", "Worker local total request limit reached"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cr Response
			if err := json.Unmarshal([]byte(tc.body), &cr); err != nil {
				t.Fatal(err)
			}
			err := cr.ProviderError()
			if tc.none {
				if err != nil {
					t.Fatalf("providerError = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("providerError = nil, want the provider's own error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}
