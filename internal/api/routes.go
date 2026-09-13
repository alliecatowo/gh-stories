package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alliecatowo/gh-stories/internal/httpx"
	"github.com/alliecatowo/gh-stories/internal/ratelimit"
)

// routes wires the /v1 contract.
//
// Grouping is by required authorization, not by feature, so it is visually
// obvious which routes are reachable anonymously. Everything under the
// authenticated group runs requireAuth before its handler.
func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(withRequestID)
	r.Use(s.recovery)
	r.Use(securityHeaders)
	r.Use(s.cors)
	r.Use(s.authenticate)
	r.Use(s.logging)
	r.Use(timeout(30 * time.Second))

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteError(w, req, httpx.NotFound())
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteError(w, req, httpx.Err(http.StatusMethodNotAllowed,
			httpx.CodeBadRequest, "That method is not allowed here."))
	})

	r.Route("/v1", func(r chi.Router) {
		// ---------------------------------------------------- anonymous
		r.Get("/health/live", h(s.getLive))
		r.Get("/health/ready", h(s.getReady))

		r.Group(func(r chi.Router) {
			r.Use(s.limit(ratelimit.RuleLoginStart, "login-start"))
			r.Get("/auth/github/start", h(s.getAuthStart))
			r.Get("/auth/github/callback", h(s.getAuthCallback))
			r.Post("/auth/cli/pending", h(s.postPendingLogin))
		})
		r.With(s.limit(ratelimit.RuleLoginPoll, "login-poll")).
			Post("/auth/cli/poll", h(s.postPollLogin))

		// Compiled in only under the `ghs_testidp` build tag, and refused at
		// runtime outside local/test. A no-op in every release build.
		s.registerTestRoutes(r)

		// ------------------------------------------------- authenticated
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)

			r.With(s.limit(ratelimit.RuleRead, "read")).Group(func(r chi.Router) {
				r.Get("/me", h(s.getMe))
				r.Get("/feed", h(s.getFeed))
				r.Get("/stories/mine", h(s.getOwnStories))
				r.Get("/users/lookup", h(s.getUserLookup))
				r.Get("/users/{login}/stories", h(s.getAuthorStories))
				r.Get("/stories/{storyId}", h(s.getStory))
				r.Get("/stories/{storyId}/viewers", h(s.getViewers))
				r.Get("/inbox", h(s.getInbox))
				r.Get("/following", h(s.listFollowing))
				r.Get("/followers", h(s.listFollowers))
				r.Get("/mutes", h(s.listSimple("mutes", "muter_user_id", "muted_github_user_id")))
				r.Get("/blocks", h(s.listSimple("blocks", "blocker_user_id", "blocked_github_user_id")))
				r.Get("/hides", h(s.listSimple("hide_rules", "author_user_id", "hidden_github_user_id")))
				r.Get("/settings", h(s.getSettings))
				r.Get("/audience-lists", h(s.listAudienceLists))
				r.Get("/auth/sessions", h(s.listAuthSessions))
			})

			// Ring status is called on every page of GitHub, so it gets its
			// own, more generous budget.
			r.With(s.limit(ratelimit.RuleStatus, "status")).
				Post("/stories/status", h(s.postStatus))

			// The media gateway re-checks authorization on every request,
			// including each range request of a video.
			r.With(s.limit(ratelimit.RuleMedia, "media")).Group(func(r chi.Router) {
				r.Get("/media/{storyId}/{variant}", h(s.getMedia))
				r.Head("/media/{storyId}/{variant}", h(s.getMedia))
			})

			r.With(s.limit(ratelimit.RuleUpload, "upload")).Group(func(r chi.Router) {
				r.Post("/uploads", h(s.postUpload))
				r.Post("/uploads/{uploadId}/finalize", h(s.postFinalize))
			})

			r.With(s.limit(ratelimit.RuleReply, "reply")).
				Post("/stories/{storyId}/replies", h(s.postReply))
			r.With(s.limit(ratelimit.RuleReaction, "reaction")).Group(func(r chi.Router) {
				r.Put("/stories/{storyId}/reaction", h(s.putReaction))
				r.Delete("/stories/{storyId}/reaction", h(s.deleteReaction))
			})
			r.With(s.limit(ratelimit.RuleReport, "report")).Post("/reports", h(s.postReport))

			r.With(s.limit(ratelimit.RuleMutation, "mutation")).Group(func(r chi.Router) {
				r.Patch("/stories/{storyId}", h(s.patchStory))
				r.Delete("/stories/{storyId}", h(s.deleteStory))
				r.Post("/stories/{storyId}/view", h(s.postView))
				r.Post("/inbox/read", h(s.postInboxRead))
				r.Post("/onboarding/import-follows", h(s.postImportFollows))

				r.Put("/following/{login}", h(s.putFollow))
				r.Delete("/following/{login}", h(s.deleteFollow))
				r.Put("/mutes/{login}", h(s.toggleRelation("mute", true)))
				r.Delete("/mutes/{login}", h(s.toggleRelation("mute", false)))
				r.Put("/blocks/{login}", h(s.toggleRelation("block", true)))
				r.Delete("/blocks/{login}", h(s.toggleRelation("block", false)))
				r.Put("/hides/{login}", h(s.toggleRelation("hide", true)))
				r.Delete("/hides/{login}", h(s.toggleRelation("hide", false)))

				r.Patch("/settings", h(s.patchSettings))
				r.Delete("/settings/account", h(s.deleteAccount))
				r.Post("/audience-lists", h(s.createAudienceList))
				r.Patch("/audience-lists/{listId}", h(s.patchAudienceList))
				r.Delete("/audience-lists/{listId}", h(s.deleteAudienceList))

				r.Post("/auth/logout", h(s.postLogout))
				r.Delete("/auth/sessions/{sessionId}", h(s.revokeSession))
			})

			// ------------------------------------------- moderators only
			// Deny-by-default, and answers 404 rather than 403 so the queue's
			// existence is not advertised to ordinary users.
			r.Group(func(r chi.Router) {
				r.Use(requireModerator)
				r.Get("/moderation/reports", h(s.listReports))
				r.Post("/moderation/actions", h(s.postModerationAction))
			})
		})
	})
	return r
}

// h adapts an error-returning handler to http.HandlerFunc.
func h(fn httpx.Handler) http.HandlerFunc { return fn.ServeHTTP }
