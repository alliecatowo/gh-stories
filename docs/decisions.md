# Decisions log

Short entries. Why, not just what. Newest last.

| # | Decision | Why |
|---|---|---|
| 1 | Owner `alliecatowo`, monorepo `gh-stories` | The authenticated account; the repo name is what makes `gh extension install alliecatowo/gh-stories` produce the `gh stories` command. |
| 2 | PostgreSQL 18.6, MinIO (quay.io) for local object storage | Same engine locally and in production. MinIO's Docker Hub repo no longer serves `minio/minio`; the images now live at `quay.io/minio/minio`. |
| 3 | Postgres data volume mounted at `/var/lib/postgresql`, not `/data` | PostgreSQL 18 images place data in a version subdirectory and refuse to start with the old mount point. Found by starting the stack, not by reading about it. |
| 4 | Hand-written Go handlers + generated **TypeScript** types from OpenAPI | Drift hurts most at the Go↔TS boundary. Go route coverage is checked against the spec by a contract test instead of generating server stubs, which keeps handler code readable. |
| 5 | TypeScript 5.9.3, not 7.x | WXT 0.21 and Astro 7 are tested against TS 5; TS 7 is too fresh to pin a release on. |
| 6 | Account application is Go `html/template`, not another Node app | It only needs OAuth handoff, approval, settings, moderation and external viewing. Avoiding a fourth JS build keeps the dependency and infrastructure count modest, as the brief asks. Documented deviation. |
| 7 | Custom ~100-line migration runner over `embed.FS` instead of golang-migrate | Versioned, checksummed, transactional, and one fewer dependency. Migrations stay plain SQL files under `infra/migrations`. |
| 8 | Media authorization gateway, not signed URLs | A signed URL keeps working after a block, an audience narrowing or a deletion. Streaming through an authenticated handler makes revocation exact and is simpler to reason about. |
| 9 | Relationship "unfollowed" tombstone rows | A re-import must not silently resurrect a relationship the user explicitly removed; a deleted row cannot express that. |
| 10 | `inbox_events` table unifying replies, reactions and new followers | One cursor, one unread count, one sync surface for both clients. |
