package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCloudRunJobTriggerRunsJobWithMetadataToken(t *testing.T) {
	var gotAuth, gotFlavor string
	var runs atomic.Int32

	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFlavor = r.Header.Get("Metadata-Flavor")
		_, _ = w.Write([]byte(`{"access_token":"tok-123"}`))
	}))
	defer meta.Close()
	run := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		runs.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer run.Close()

	now := time.Unix(1000, 0)
	tr := &CloudRunJobTrigger{RunURL: run.URL, TokenURL: meta.URL, Now: func() time.Time { return now }}

	if err := tr.Trigger(context.Background()); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if gotAuth != "Bearer tok-123" || gotFlavor != "Google" {
		t.Fatalf("auth=%q flavor=%q", gotAuth, gotFlavor)
	}

	// A burst inside the coalescing window must not start another execution.
	now = now.Add(2 * time.Second)
	if err := tr.Trigger(context.Background()); err != nil {
		t.Fatalf("coalesced Trigger: %v", err)
	}
	if runs.Load() != 1 {
		t.Fatalf("runs = %d, want 1 (coalesced)", runs.Load())
	}

	now = now.Add(minTriggerGap)
	if err := tr.Trigger(context.Background()); err != nil {
		t.Fatalf("later Trigger: %v", err)
	}
	if runs.Load() != 2 {
		t.Fatalf("runs = %d, want 2", runs.Load())
	}
}

func TestCloudRunJobTriggerReportsFailures(t *testing.T) {
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"t"}`))
	}))
	defer meta.Close()
	run := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer run.Close()

	tr := &CloudRunJobTrigger{RunURL: run.URL, TokenURL: meta.URL}
	if err := tr.Trigger(context.Background()); err == nil {
		t.Fatal("expected an error for a 403 from the Run API")
	}
}
