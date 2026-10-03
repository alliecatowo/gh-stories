-- A later re-import needs its own GitHub authorization, because the upstream
-- token is deliberately never retained. Allow that purpose.
ALTER TABLE oauth_flows DROP CONSTRAINT IF EXISTS oauth_flows_purpose_ck;
ALTER TABLE oauth_flows ADD CONSTRAINT oauth_flows_purpose_ck
    CHECK (purpose IN ('web_login', 'cli_approval', 'import'));
