-- GitHub Stories :: initial schema
-- Conventions:
--   * GitHub's numeric user id is the authoritative identity key.
--   * A github_identities row may exist without a users row: that is an
--     imported identity (someone followed, who has not signed up).
--   * All timestamps are timestamptz in authoritative server time (UTC).

SET client_min_messages = warning;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------- identities

CREATE TABLE github_identities (
    github_user_id  BIGINT PRIMARY KEY,
    login           TEXT        NOT NULL,
    login_lower     TEXT        NOT NULL,
    avatar_url      TEXT        NOT NULL DEFAULT '',
    profile_url     TEXT        NOT NULL DEFAULT '',
    account_type    TEXT        NOT NULL DEFAULT 'User',
    refreshed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT github_identities_type_ck CHECK (account_type IN ('User','Organization','Bot'))
);
-- Logins are renameable, so this index is NOT unique-by-login-forever; it is
-- kept consistent by refreshing identity attributes on every authorization.
CREATE UNIQUE INDEX github_identities_login_lower_uq ON github_identities (login_lower);

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    github_user_id  BIGINT      NOT NULL UNIQUE REFERENCES github_identities (github_user_id) ON DELETE RESTRICT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    is_moderator    BOOLEAN     NOT NULL DEFAULT false,
    suspended_at    TIMESTAMPTZ,
    suspended_reason TEXT,
    deleted_at      TIMESTAMPTZ,
    -- account preferences
    default_visibility     TEXT    NOT NULL DEFAULT 'followers_of_author',
    default_audience_list_id UUID,
    default_allow_replies  BOOLEAN NOT NULL DEFAULT true,
    default_allow_reactions BOOLEAN NOT NULL DEFAULT true,
    follow_import_completed_at TIMESTAMPTZ,
    CONSTRAINT users_default_visibility_ck CHECK (default_visibility IN
        ('followers_of_author','author_follows','mutuals','custom_list','public'))
);
CREATE INDEX users_moderator_idx ON users (is_moderator) WHERE is_moderator;

-- ------------------------------------------------------------ auth/sessions

CREATE TABLE sessions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash    BYTEA       NOT NULL UNIQUE,      -- sha256 of the opaque bearer token
    client_kind   TEXT        NOT NULL,             -- browser_extension | cli | web
    client_label  TEXT        NOT NULL DEFAULT '',
    user_agent    TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ,
    CONSTRAINT sessions_client_kind_ck CHECK (client_kind IN ('browser_extension','cli','web'))
);
CREATE INDEX sessions_user_idx ON sessions (user_id) WHERE revoked_at IS NULL;

-- OAuth authorization-code flows (state + PKCE verifier live server side only).
CREATE TABLE oauth_flows (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    state_hash        BYTEA       NOT NULL UNIQUE,
    code_verifier     TEXT        NOT NULL,
    redirect_uri      TEXT        NOT NULL,
    purpose           TEXT        NOT NULL,        -- web_login | cli_approval
    pending_login_id  UUID,
    return_to         TEXT        NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ NOT NULL,
    consumed_at       TIMESTAMPTZ,
    CONSTRAINT oauth_flows_purpose_ck CHECK (purpose IN ('web_login','cli_approval'))
);

-- Service-mediated CLI login: unguessable polling secret + short user code.
CREATE TABLE pending_logins (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    polling_secret_hash BYTEA       NOT NULL UNIQUE,
    user_code           TEXT        NOT NULL UNIQUE,
    client_kind         TEXT        NOT NULL,
    client_label        TEXT        NOT NULL DEFAULT '',
    state               TEXT        NOT NULL DEFAULT 'pending',  -- pending|approved|denied|consumed|expired
    approved_user_id    UUID REFERENCES users (id) ON DELETE CASCADE,
    issued_session_id   UUID REFERENCES sessions (id) ON DELETE SET NULL,
    issued_token        TEXT,                    -- one-shot delivery; cleared on consume
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    approved_at         TIMESTAMPTZ,
    consumed_at         TIMESTAMPTZ,
    poll_count          INTEGER     NOT NULL DEFAULT 0,
    last_poll_at        TIMESTAMPTZ,
    CONSTRAINT pending_logins_state_ck CHECK (state IN ('pending','approved','denied','consumed','expired')),
    CONSTRAINT pending_logins_client_kind_ck CHECK (client_kind IN ('browser_extension','cli','web'))
);
CREATE INDEX pending_logins_expiry_idx ON pending_logins (expires_at);

-- ------------------------------------------------------------ social graph

CREATE TABLE relationships (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    follower_user_id        UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    followee_github_user_id BIGINT NOT NULL REFERENCES github_identities (github_user_id) ON DELETE CASCADE,
    -- 'unfollowed' is a tombstone so a later GitHub re-import does not silently
    -- resurrect a relationship the user explicitly removed.
    state                   TEXT        NOT NULL DEFAULT 'active',
    provenance              TEXT        NOT NULL,   -- github_import | stories_native
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    unfollowed_at           TIMESTAMPTZ,
    CONSTRAINT relationships_state_ck CHECK (state IN ('active','unfollowed')),
    CONSTRAINT relationships_provenance_ck CHECK (provenance IN ('github_import','stories_native')),
    CONSTRAINT relationships_no_self_ck CHECK (true),
    UNIQUE (follower_user_id, followee_github_user_id)
);
CREATE INDEX relationships_followee_idx ON relationships (followee_github_user_id) WHERE state = 'active';
CREATE INDEX relationships_follower_idx ON relationships (follower_user_id) WHERE state = 'active';

CREATE TABLE mutes (
    muter_user_id        UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    muted_github_user_id BIGINT NOT NULL REFERENCES github_identities (github_user_id) ON DELETE CASCADE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (muter_user_id, muted_github_user_id)
);

CREATE TABLE blocks (
    blocker_user_id        UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    blocked_github_user_id BIGINT NOT NULL REFERENCES github_identities (github_user_id) ON DELETE CASCADE,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker_user_id, blocked_github_user_id)
);
CREATE INDEX blocks_blocked_idx ON blocks (blocked_github_user_id);

-- "Hide my Stories from this person" (author-side rule, independent of block).
CREATE TABLE hide_rules (
    author_user_id        UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    hidden_github_user_id BIGINT NOT NULL REFERENCES github_identities (github_user_id) ON DELETE CASCADE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (author_user_id, hidden_github_user_id)
);

CREATE TABLE audience_lists (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id UUID     NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    UNIQUE (owner_user_id, name)
);

CREATE TABLE audience_list_members (
    list_id        UUID   NOT NULL REFERENCES audience_lists (id) ON DELETE CASCADE,
    github_user_id BIGINT NOT NULL REFERENCES github_identities (github_user_id) ON DELETE CASCADE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (list_id, github_user_id)
);

ALTER TABLE users ADD CONSTRAINT users_default_audience_fk
    FOREIGN KEY (default_audience_list_id) REFERENCES audience_lists (id) ON DELETE SET NULL;

-- ------------------------------------------------------------ media pipeline

CREATE TABLE upload_intents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    object_key      TEXT        NOT NULL UNIQUE,   -- server-chosen private key
    declared_mime   TEXT        NOT NULL,
    declared_size   BIGINT      NOT NULL,
    state           TEXT        NOT NULL DEFAULT 'issued', -- issued|finalized|aborted|expired
    actual_size     BIGINT,
    checksum_sha256 TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    finalized_at    TIMESTAMPTZ,
    CONSTRAINT upload_intents_state_ck CHECK (state IN ('issued','finalized','aborted','expired'))
);
CREATE INDEX upload_intents_gc_idx ON upload_intents (state, expires_at);

CREATE TABLE media_jobs (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    upload_intent_id UUID        NOT NULL UNIQUE REFERENCES upload_intents (id) ON DELETE CASCADE,
    user_id          UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    state            TEXT        NOT NULL DEFAULT 'queued', -- queued|leased|succeeded|failed
    -- Immutable worker input frozen at finalize time.
    input            JSONB       NOT NULL,
    attempts         INTEGER     NOT NULL DEFAULT 0,
    max_attempts     INTEGER     NOT NULL DEFAULT 3,
    lease_owner      TEXT,
    lease_expires_at TIMESTAMPTZ,
    run_after        TIMESTAMPTZ NOT NULL DEFAULT now(),
    error_code       TEXT,
    error_message    TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at      TIMESTAMPTZ,
    CONSTRAINT media_jobs_state_ck CHECK (state IN ('queued','leased','succeeded','failed'))
);
CREATE INDEX media_jobs_claim_idx ON media_jobs (state, run_after) WHERE state IN ('queued','leased');

-- -------------------------------------------------------------- story items

CREATE TABLE story_items (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_user_id   UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    media_job_id     UUID        NOT NULL REFERENCES media_jobs (id) ON DELETE RESTRICT,
    state            TEXT        NOT NULL DEFAULT 'processing', -- processing|published|failed|deleted|removed
    caption          TEXT        NOT NULL DEFAULT '',
    alt_text         TEXT        NOT NULL DEFAULT '',
    visibility       TEXT        NOT NULL DEFAULT 'followers_of_author',
    audience_list_id UUID REFERENCES audience_lists (id) ON DELETE SET NULL,
    allow_replies    BOOLEAN     NOT NULL DEFAULT true,
    allow_reactions  BOOLEAN     NOT NULL DEFAULT true,
    -- published_at is set when normalized media becomes ready and the item
    -- becomes visible; expires_at is exactly published_at + 24h in server time.
    published_at     TIMESTAMPTZ,
    expires_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ,
    removed_by_user_id UUID REFERENCES users (id) ON DELETE SET NULL,
    removal_reason   TEXT,
    failure_code     TEXT,
    failure_message  TEXT,
    CONSTRAINT story_items_state_ck CHECK (state IN ('processing','published','failed','deleted','removed')),
    CONSTRAINT story_items_visibility_ck CHECK (visibility IN
        ('followers_of_author','author_follows','mutuals','custom_list','public')),
    CONSTRAINT story_items_custom_list_ck CHECK (visibility <> 'custom_list' OR audience_list_id IS NOT NULL),
    CONSTRAINT story_items_published_pair_ck CHECK ((published_at IS NULL) = (expires_at IS NULL))
);
-- Each item expires on its own clock; posting another item never touches this.
CREATE INDEX story_items_active_idx ON story_items (author_user_id, published_at DESC)
    WHERE state = 'published';
CREATE INDEX story_items_expiry_idx ON story_items (expires_at) WHERE state = 'published';

CREATE TABLE media_variants (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    story_item_id   UUID        NOT NULL REFERENCES story_items (id) ON DELETE CASCADE,
    kind            TEXT        NOT NULL, -- image | video | poster | thumb | terminal
    object_key      TEXT        NOT NULL,
    mime            TEXT        NOT NULL,
    width           INTEGER     NOT NULL DEFAULT 0,
    height          INTEGER     NOT NULL DEFAULT 0,
    duration_ms     INTEGER     NOT NULL DEFAULT 0,
    byte_size       BIGINT      NOT NULL DEFAULT 0,
    checksum_sha256 TEXT        NOT NULL DEFAULT '',
    has_audio       BOOLEAN     NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT media_variants_kind_ck CHECK (kind IN ('image','video','poster','thumb','terminal')),
    UNIQUE (story_item_id, kind)
);

-- ------------------------------------------------- interactions & read state

CREATE TABLE story_views (
    story_item_id   UUID        NOT NULL REFERENCES story_items (id) ON DELETE CASCADE,
    viewer_user_id  UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    delivered_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    acknowledged_at TIMESTAMPTZ,
    PRIMARY KEY (story_item_id, viewer_user_id)
);
CREATE INDEX story_views_viewer_idx ON story_views (viewer_user_id);

CREATE TABLE reactions (
    story_item_id UUID        NOT NULL REFERENCES story_items (id) ON DELETE CASCADE,
    user_id       UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    emoji         TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (story_item_id, user_id),
    CONSTRAINT reactions_emoji_ck CHECK (emoji IN ('❤️','😂','🔥','😭','💀'))
);

-- Replies outlive their Story as private text for a limited retention period.
CREATE TABLE replies (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    story_item_id     UUID        NOT NULL REFERENCES story_items (id) ON DELETE CASCADE,
    sender_user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    recipient_user_id UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body              TEXT        NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    read_at           TIMESTAMPTZ,
    deleted_at        TIMESTAMPTZ,
    purge_after       TIMESTAMPTZ NOT NULL,
    CONSTRAINT replies_body_len_ck CHECK (char_length(body) BETWEEN 1 AND 500)
);
CREATE INDEX replies_recipient_idx ON replies (recipient_user_id, created_at DESC);
CREATE INDEX replies_sender_idx ON replies (sender_user_id, created_at DESC);
CREATE INDEX replies_purge_idx ON replies (purge_after);

CREATE TABLE inbox_events (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind          TEXT        NOT NULL, -- reply | reaction | follow
    actor_user_id UUID REFERENCES users (id) ON DELETE CASCADE,
    story_item_id UUID REFERENCES story_items (id) ON DELETE CASCADE,
    reply_id      UUID REFERENCES replies (id) ON DELETE CASCADE,
    emoji         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    read_at       TIMESTAMPTZ,
    CONSTRAINT inbox_events_kind_ck CHECK (kind IN ('reply','reaction','follow'))
);
CREATE INDEX inbox_events_user_idx ON inbox_events (user_id, created_at DESC);
CREATE UNIQUE INDEX inbox_events_reaction_uq ON inbox_events (user_id, story_item_id, actor_user_id)
    WHERE kind = 'reaction';
CREATE UNIQUE INDEX inbox_events_follow_uq ON inbox_events (user_id, actor_user_id)
    WHERE kind = 'follow';

-- ---------------------------------------------------------------- moderation

CREATE TABLE reports (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_user_id  UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    subject_kind      TEXT        NOT NULL, -- story | user
    story_item_id     UUID REFERENCES story_items (id) ON DELETE SET NULL,
    subject_user_id   UUID REFERENCES users (id) ON DELETE SET NULL,
    reason            TEXT        NOT NULL,
    details           TEXT        NOT NULL DEFAULT '',
    state             TEXT        NOT NULL DEFAULT 'open', -- open|actioned|dismissed
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at       TIMESTAMPTZ,
    resolved_by_user_id UUID REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT reports_subject_kind_ck CHECK (subject_kind IN ('story','user')),
    CONSTRAINT reports_state_ck CHECK (state IN ('open','actioned','dismissed')),
    CONSTRAINT reports_reason_ck CHECK (reason IN
        ('spam','harassment','nudity','violence','self_harm','illegal','other'))
);
CREATE INDEX reports_open_idx ON reports (state, created_at DESC);

CREATE TABLE moderation_actions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    moderator_user_id   UUID        NOT NULL REFERENCES users (id) ON DELETE SET NULL,
    kind                TEXT        NOT NULL,
    report_id           UUID REFERENCES reports (id) ON DELETE SET NULL,
    story_item_id       UUID REFERENCES story_items (id) ON DELETE SET NULL,
    target_user_id      UUID REFERENCES users (id) ON DELETE SET NULL,
    reason              TEXT        NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT moderation_actions_kind_ck CHECK (kind IN
        ('remove_story','suspend_user','unsuspend_user','dismiss_report'))
);

-- ------------------------------------------------------- background cleanup

CREATE TABLE cleanup_jobs (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind             TEXT        NOT NULL, -- expire_stories|delete_objects|purge_replies|purge_views|gc_uploads
    payload          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    state            TEXT        NOT NULL DEFAULT 'queued',
    attempts         INTEGER     NOT NULL DEFAULT 0,
    max_attempts     INTEGER     NOT NULL DEFAULT 8,
    lease_owner      TEXT,
    lease_expires_at TIMESTAMPTZ,
    run_after        TIMESTAMPTZ NOT NULL DEFAULT now(),
    error_message    TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT cleanup_jobs_kind_ck CHECK (kind IN
        ('expire_stories','delete_objects','purge_replies','purge_views','gc_uploads')),
    CONSTRAINT cleanup_jobs_state_ck CHECK (state IN ('queued','leased','succeeded','failed'))
);
CREATE INDEX cleanup_jobs_claim_idx ON cleanup_jobs (state, run_after) WHERE state IN ('queued','leased');

-- Idempotency for retried mutations (upload finalize, post, reply, react).
CREATE TABLE idempotency_keys (
    user_id       UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    scope         TEXT        NOT NULL,
    key           TEXT        NOT NULL,
    request_hash  BYTEA       NOT NULL,
    status_code   INTEGER     NOT NULL,
    response_body JSONB       NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, scope, key)
);
CREATE INDEX idempotency_keys_gc_idx ON idempotency_keys (created_at);
