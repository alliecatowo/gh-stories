package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// WorkerTrigger asks the media worker to run now instead of waiting for the
// next scheduled sweep. Implementations must be safe for concurrent use and
// failures are never fatal to the request: the scheduled sweep is the
// backstop, so a missed trigger only delays processing.
type WorkerTrigger interface {
	Trigger(ctx context.Context) error
}

const (
	metadataTokenURL = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token"
	// minTriggerGap coalesces bursts: one execution drains the whole queue, so
	// a second start within this window would only bill another container.
	minTriggerGap = 10 * time.Second
)

// CloudRunJobTrigger starts a Cloud Run Job through the Run Admin API
// (jobs.run), authenticating with the service's own identity from the
// metadata server. That identity needs roles/run.developer on the job only.
type CloudRunJobTrigger struct {
	// RunURL is the full jobs.run endpoint, e.g.
	// https://us-central1-run.googleapis.com/v2/projects/P/locations/R/jobs/J:run
	RunURL string

	// Client and TokenURL are overridable for tests.
	Client   *http.Client
	TokenURL string
	Now      func() time.Time

	mu   sync.Mutex
	last time.Time
}

// Trigger starts one job execution, coalescing calls made within minTriggerGap.
func (t *CloudRunJobTrigger) Trigger(ctx context.Context) error {
	now := time.Now
	if t.Now != nil {
		now = t.Now
	}
	t.mu.Lock()
	if !t.last.IsZero() && now().Sub(t.last) < minTriggerGap {
		t.mu.Unlock()
		return nil
	}
	t.last = now()
	t.mu.Unlock()

	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	tokenURL := t.TokenURL
	if tokenURL == "" {
		tokenURL = metadataTokenURL
	}

	token, err := fetchAccessToken(ctx, client, tokenURL)
	if err != nil {
		return fmt.Errorf("worker trigger token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.RunURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("worker trigger request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("worker trigger: status %d", resp.StatusCode)
	}
	return nil
}

func fetchAccessToken(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metadata server status %d", resp.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.AccessToken == "" {
		return "", errors.New("metadata server returned no access token")
	}
	return body.AccessToken, nil
}
