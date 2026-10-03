package config

import "testing"

func lookup(extra map[string]string) func(string) (string, bool) {
	base := map[string]string{
		"GHS_DATABASE_URL":         "postgres://u:p@localhost/db",
		"GHS_S3_BUCKET":            "b",
		"GHS_S3_ACCESS_KEY_ID":     "id",
		"GHS_S3_SECRET_ACCESS_KEY": "secret",
		"GHS_PUBLIC_URL":           "http://localhost:8787",
		"GHS_SECRET_KEY":           "0123456789abcdef0123456789abcdef-test",
	}
	for k, v := range extra {
		base[k] = v
	}
	return func(k string) (string, bool) { v, ok := base[k]; return v, ok }
}

func TestWorkerTriggerURLIsOptional(t *testing.T) {
	c, err := LoadFrom(lookup(nil))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if c.WorkerTriggerURL != "" {
		t.Fatalf("WorkerTriggerURL = %q, want empty by default", c.WorkerTriggerURL)
	}
	c, err = LoadFrom(lookup(map[string]string{"GHS_WORKER_TRIGGER_URL": "https://run.example/jobs/w:run"}))
	if err != nil || c.WorkerTriggerURL == "" {
		t.Fatalf("configured trigger URL not loaded: %v", err)
	}
}

func TestWorkerTriggerURLMustBeHTTPSInProduction(t *testing.T) {
	_, err := LoadFrom(lookup(map[string]string{
		"GHS_ENV":                "production",
		"GHS_PUBLIC_URL":         "https://stories.example.com",
		"GHS_SECRET_KEY":         "0123456789abcdef0123456789abcdef-prod",
		"GHS_WORKER_TRIGGER_URL": "http://insecure.example/run",
	}))
	if err == nil {
		t.Fatal("expected production config with an http trigger URL to be rejected")
	}
}
