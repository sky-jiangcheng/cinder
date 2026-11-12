package db

import "testing"

// The success path is the one both entry points take on a normal startup; the
// retry path waits on a real cross-process lock and belongs to the race tests.
func TestOpenWithRetryReturnsAHandle(t *testing.T) {
	database, err := OpenWithRetry(":memory:")
	if err != nil {
		t.Fatalf("OpenWithRetry: %v", err)
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
}
