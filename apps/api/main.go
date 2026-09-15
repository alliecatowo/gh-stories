// Command api serves the GitHub Stories /v1 API and the small account
// application on one process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/account"
	"github.com/alliecatowo/gh-stories/internal/api"
	"github.com/alliecatowo/gh-stories/internal/auth"
	"github.com/alliecatowo/gh-stories/internal/clock"
	"github.com/alliecatowo/gh-stories/internal/config"
	"github.com/alliecatowo/gh-stories/internal/db"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/ghclient"
	"github.com/alliecatowo/gh-stories/internal/objstore"
	"github.com/alliecatowo/gh-stories/internal/store"
	"github.com/alliecatowo/gh-stories/internal/version"
)

func main() {
	var (
		migrate     = flag.Bool("migrate", false, "apply pending migrations and exit")
		autoMigrate = flag.Bool("auto-migrate", false, "apply pending migrations before serving")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("gh-stories api", version.Short(), version.BuildDate)
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger, *migrate, *autoMigrate); err != nil {
		// Configuration problems are reported by NAME, never by value.
		logger.Error("startup failed", "err", err.Error())
		os.Exit(1)
	}
}

func run(logger *slog.Logger, migrateOnly, autoMigrate bool) error {
	cfg, err := config.Load()
	if err != nil {
		var missing *config.MissingError
		if errors.As(err, &missing) {
			return fmt.Errorf("%w\n\nSee .env.example for what each variable means", err)
		}
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if migrateOnly || autoMigrate {
		applied, err := db.MigrateEmbedded(ctx, pool)
		if err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		logger.Info("migrations applied", "count", len(applied), "versions", applied)
		if migrateOnly {
			return nil
		}
	}

	// Production never gets a controllable clock: config refuses to start with
	// GHS_TEST_CLOCK set, so this can only ever be Real outside local/test.
	clk := clock.Clock(clock.Real{})
	st := store.New(pool, clk)

	// Moderator access is granted by configuration, not by anything in the
	// product. Applying it at startup also revokes anyone no longer listed.
	modIDs := make([]domain.GitHubID, 0, len(cfg.ModeratorGitHubIDs))
	for _, id := range cfg.ModeratorGitHubIDs {
		modIDs = append(modIDs, domain.GitHubID(id))
	}
	if granted, revoked, err := st.SyncModerators(ctx, modIDs); err != nil {
		logger.Warn("could not apply the moderator list", "err", err)
	} else if granted > 0 || revoked > 0 {
		logger.Info("moderator list applied", "granted", granted, "revoked", revoked)
	}

	objects, err := objstore.New(objstore.Config{
		Endpoint:       cfg.S3Endpoint,
		Region:         cfg.S3Region,
		Bucket:         cfg.S3Bucket,
		AccessKeyID:    cfg.S3AccessKeyID,
		SecretKey:      cfg.S3SecretKey,
		ForcePathStyle: cfg.S3ForcePathStyle,
	})
	if err != nil {
		return fmt.Errorf("object storage: %w", err)
	}

	authService := auth.NewService(st, ghclient.New(ghclient.Options{
		UserAgent: "gh-stories/" + version.Version,
	}), cfg)

	apiServer := api.New(cfg, st, clk,
		&objectReaderAdapter{objects},
		objects, // objstore.Store already satisfies api.Uploader
		&authAdapter{svc: authService, cfg: cfg},
		logger)

	accountApp, err := account.New(cfg, st, &authAdapter{svc: authService, cfg: cfg})
	if err != nil {
		return fmt.Errorf("account application: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/v1/", apiServer.Handler())
	mux.Handle("/", accountApp.Routes())

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      0, // media streaming sets its own pace
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}

	logger.Info("listening",
		"addr", cfg.HTTPAddr, "env", string(cfg.Env),
		"public_url", cfg.PublicURL.String(),
		"github_oauth", cfg.GitHubOAuthConfigured(),
		"version", version.Short())

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// objectReaderAdapter narrows objstore.Store to what the media gateway needs,
// translating the storage driver's Object into the API's own shape so the API
// package never imports the storage driver.
type objectReaderAdapter struct{ s objstore.Store }

func (a *objectReaderAdapter) Get(ctx context.Context, key, rangeHeader string) (*api.ObjectData, error) {
	obj, err := a.s.Get(ctx, key, rangeHeader)
	if err != nil {
		return nil, err
	}
	return &api.ObjectData{
		Body:          obj.Body,
		ContentType:   obj.ContentType,
		ContentLength: obj.ContentLength,
		ContentRange:  obj.ContentRange,
		ETag:          obj.ETag,
		StatusCode:    obj.StatusCode,
	}, nil
}

func (a *objectReaderAdapter) Healthy(ctx context.Context) error { return a.s.Healthy(ctx) }

// authAdapter maps the auth service onto the interfaces the API and account
// applications declare.
type authAdapter struct {
	svc *auth.Service
	cfg *config.Config
}

func (a *authAdapter) StartAuthorization(ctx context.Context, purpose, returnTo string, pendingID *uuid.UUID) (string, error) {
	return a.svc.StartAuthorization(ctx, purpose, returnTo, pendingID)
}

func (a *authAdapter) CompleteAuthorization(ctx context.Context, code, state string) (*api.AuthResult, error) {
	res, err := a.svc.CompleteAuthorization(ctx, code, state)
	if err != nil {
		return nil, err
	}
	return &api.AuthResult{
		User: res.User, Flow: res.Flow,
		IsNewAccount: res.IsNewAccount, UpstreamToken: res.UpstreamToken,
	}, nil
}

func (a *authAdapter) IssueSession(ctx context.Context, userID uuid.UUID,
	kind domain.ClientKind, label, userAgent string) (string, *domain.Session, error) {
	return a.svc.IssueSession(ctx, userID, kind, label, userAgent)
}

func (a *authAdapter) Authenticate(ctx context.Context, bearer string) (*domain.Session, *domain.User, error) {
	return a.svc.Authenticate(ctx, bearer)
}

func (a *authAdapter) Logout(ctx context.Context, userID, sessionID uuid.UUID) error {
	return a.svc.Logout(ctx, userID, sessionID)
}

func (a *authAdapter) CreatePendingLogin(ctx context.Context, kind domain.ClientKind, label string) (*api.PendingLoginResult, error) {
	res, err := a.svc.CreatePendingLogin(ctx, kind, label)
	if err != nil {
		return nil, err
	}
	return &api.PendingLoginResult{
		ID: res.ID, PollingSecret: res.PollingSecret, UserCode: res.UserCode,
		VerificationURL: res.VerificationURL, ExpiresAt: res.ExpiresAt,
		IntervalSeconds: res.IntervalSeconds,
	}, nil
}

func (a *authAdapter) ApprovePendingLogin(ctx context.Context, pendingID, userID uuid.UUID, userCode string) error {
	return a.svc.ApprovePendingLogin(ctx, pendingID, userID, userCode)
}

func (a *authAdapter) DenyPendingLogin(ctx context.Context, pendingID uuid.UUID) error {
	return a.svc.DenyPendingLogin(ctx, pendingID)
}

func (a *authAdapter) Poll(ctx context.Context, pendingID uuid.UUID, secret string) (*store.PollResult, error) {
	return a.svc.Poll(ctx, pendingID, secret)
}

func (a *authAdapter) StartDeviceLogin(ctx context.Context, kind domain.ClientKind, label string) (*api.DeviceLoginStart, error) {
	res, err := a.svc.StartDeviceLogin(ctx, kind, label)
	if err != nil {
		return nil, err
	}
	return &api.DeviceLoginStart{
		ID: res.ID, UserCode: res.UserCode, VerificationURI: res.VerificationURI,
		ExpiresAt: res.ExpiresAt, IntervalSeconds: res.IntervalSeconds,
	}, nil
}

func (a *authAdapter) PollDeviceLogin(ctx context.Context, id uuid.UUID) (*store.PollResult, error) {
	return a.svc.PollDeviceLogin(ctx, id)
}

func (a *authAdapter) ImportFollows(ctx context.Context, userID uuid.UUID,
	upstreamToken string, enabled, preview bool) (*api.ImportSummary, error) {
	sum, err := a.svc.ImportFollows(ctx, userID, upstreamToken, enabled, preview)
	if err != nil {
		return nil, err
	}
	return &api.ImportSummary{
		Enabled: enabled, Preview: preview,
		GitHubFollowingCount: sum.GitHubFollowingCount,
		Added:                sum.Added,
		AlreadyFollowing:     sum.AlreadyFollowing,
		SkippedUnfollowed:    sum.SkippedUnfollowed,
		SkippedBlocked:       sum.SkippedBlocked,
		Sample:               sum.Sample,
	}, nil
}

func (a *authAdapter) Configured() bool { return a.cfg.GitHubOAuthConfigured() }
