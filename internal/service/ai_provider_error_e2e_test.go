package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"repotory/internal/db"
)

// End to end through the real HTTP path: a 200 body with an error envelope and
// no choices must surface the provider's words, not "no choices".
func TestAskAISurfacesProviderError(t *testing.T) {
	body := `{"id":"gen-1","error":{"message":"Upstream error from Nvidia: ResourceExhausted: Worker local total request limit reached (16/16)","code":502,"metadata":{"error_type":"provider_unavailable"}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	svc, _ := setupService(t)
	if err := db.SetConfig(svc.db, "ai_chat_base_url", srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := db.SetConfig(svc.db, "ai_chat_model", "test-model"); err != nil {
		t.Fatal(err)
	}

	_, err := svc.AskAI(context.Background(), 0, "问题")
	if err == nil {
		t.Fatal("expected an error for a 200 body carrying an error envelope")
	}
	msg := err.Error()
	if !strings.Contains(msg, "provider_unavailable") {
		t.Errorf("error %q does not carry the provider's error_type", msg)
	}
	if !strings.Contains(msg, "ResourceExhausted") {
		t.Errorf("error %q does not carry the provider's message", msg)
	}
	if strings.Contains(msg, "no choices") {
		t.Errorf("error %q still collapses to the unhelpful 'no choices' wording", msg)
	}
}

// A body with neither choices nor an error envelope is genuinely malformed, and
// the old wording is still the accurate one for it.
func TestAskAIKeepsNoChoicesWordingWhenThereIsNoErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	svc, _ := setupService(t)
	if err := db.SetConfig(svc.db, "ai_chat_base_url", srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := db.SetConfig(svc.db, "ai_chat_model", "test-model"); err != nil {
		t.Fatal(err)
	}

	_, err := svc.AskAI(context.Background(), 0, "问题")
	if err == nil {
		t.Fatal("expected an error for a body with no choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("error %q should keep the 'no choices' wording for a truly empty body", err)
	}
}
