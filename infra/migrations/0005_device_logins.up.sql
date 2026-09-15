-- GitHub Device Authorization Grant logins.
--
-- The device_code is a server-side credential with the same trust as the
-- one-shot issued_token: it is retained in the database and never exposed to
-- an HTTP caller. The polling secret pattern of pending_logins does not
-- apply here because the anonymous device-poll endpoint is rate limited
-- exactly like the CLI poll endpoint.
CREATE TABLE IF NOT EXISTS device_logins (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_code TEXT NOT NULL,
    client_kind TEXT NOT NULL,
    client_label TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'pending',
    user_id UUID REFERENCES users (id) ON DELETE SET NULL,
    issued_token TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    last_poll_at TIMESTAMPTZ,
    poll_count INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT device_logins_state_ck CHECK (state IN ('pending','approved','consumed','expired')),
    CONSTRAINT device_logins_client_kind_ck CHECK (client_kind IN ('browser_extension','cli','web'))
);
CREATE INDEX IF NOT EXISTS device_logins_expires_at_idx ON device_logins (expires_at);
