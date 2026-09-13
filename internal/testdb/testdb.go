// Package testdb gives integration tests a real, isolated PostgreSQL database.
// These tests run against an actual server with the actual migrations — there
// is no in-memory substitute, because the authorization rules ARE SQL.
package testdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/clock"
	"github.com/alliecatowo/gh-stories/internal/db"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/store"
)

// AdminURL is the connection string used to create per-test databases.
func AdminURL() string {
	if v := os.Getenv("GHS_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://stories:stories_local_dev@localhost:55432/stories?sslmode=disable"
}

// New creates a throwaway database, migrates it, and returns a Store wired to
// a controllable clock so expiry boundaries can be crossed deterministically.
func New(t *testing.T) (*store.Store, *clock.Controllable) {
	t.Helper()
	ctx := context.Background()

	admin, err := db.Open(ctx, AdminURL())
	if err != nil {
		t.Skipf("integration database unavailable (%v); start it with `mise run infra:up`", err)
	}
	name := "ghs_test_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		admin.Close()
		t.Fatalf("create test database: %v", err)
	}
	admin.Close()

	url := replaceDBName(AdminURL(), name)
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if _, err := db.MigrateEmbedded(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("migrate test database: %v", err)
	}

	// A fixed instant keeps every expiry assertion reproducible.
	clk := clock.NewControllable(time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC))

	t.Cleanup(func() {
		pool.Close()
		a, err := db.Open(context.Background(), AdminURL())
		if err != nil {
			return
		}
		defer a.Close()
		_, _ = a.Exec(context.Background(),
			fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name))
	})

	return store.New(pool, clk), clk
}

func replaceDBName(url, name string) string {
	i := strings.LastIndex(url, "/")
	j := strings.Index(url[i:], "?")
	if j < 0 {
		return url[:i+1] + name
	}
	return url[:i+1] + name + url[i+j:]
}

// Account registers a Stories account for a fictional GitHub identity.
func Account(t *testing.T, s *store.Store, gitHubID int64, login string) *domain.User {
	t.Helper()
	u, _, err := s.EnsureUser(context.Background(), nil, domain.Identity{
		GitHubID:   domain.GitHubID(gitHubID),
		Login:      login,
		AvatarURL:  "https://avatars.example/" + login + ".png",
		ProfileURL: "https://github.com/" + login,
	})
	if err != nil {
		t.Fatalf("create account %s: %v", login, err)
	}
	return u
}

// Identity records a GitHub identity that has NOT signed up for Stories.
func Identity(t *testing.T, s *store.Store, gitHubID int64, login string) domain.Identity {
	t.Helper()
	id := domain.Identity{
		GitHubID:   domain.GitHubID(gitHubID),
		Login:      login,
		AvatarURL:  "https://avatars.example/" + login + ".png",
		ProfileURL: "https://github.com/" + login,
	}
	if err := s.UpsertIdentity(context.Background(), nil, id); err != nil {
		t.Fatalf("upsert identity %s: %v", login, err)
	}
	return id
}

// PublishStory is the shortcut a test uses when it cares about authorization
// rather than about the media pipeline. It walks the same intent → finalize →
// publish path the worker uses, so publication time and expiry are real.
func PublishStory(t *testing.T, s *store.Store, author *domain.User,
	vis domain.Visibility, listID *uuid.UUID, caption string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	ui, err := s.CreateUploadIntent(ctx, author.ID,
		"u/"+author.ID.String()+"/"+uuid.NewString(), "image/jpeg", 1024, 30*time.Minute)
	if err != nil {
		t.Fatalf("upload intent: %v", err)
	}
	storyID, _, err := s.FinalizeUpload(ctx, author.ID, ui.ID, 1024,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		store.StoryDraft{
			Caption: caption, Visibility: vis, AudienceListID: listID,
			AllowReplies: true, AllowReactions: true,
		}, "fixture.jpg")
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	job, err := s.ClaimMediaJob(ctx, "test-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if _, _, err := s.PublishStory(ctx, store.PublishResult{
		JobID: job.ID, StoryID: storyID,
		Variants: []domain.MediaVariant{{
			Kind: domain.VariantImage, ObjectKey: "m/" + storyID.String() + "/image.jpg",
			MIME: "image/jpeg", Width: 1080, Height: 1920, ByteSize: 900,
		}, {
			Kind: domain.VariantThumb, ObjectKey: "m/" + storyID.String() + "/thumb.jpg",
			MIME: "image/jpeg", Width: 108, Height: 192, ByteSize: 90,
		}},
	}, 24*time.Hour); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return storyID
}
