ALTER TABLE oauth_flows DROP CONSTRAINT IF EXISTS oauth_flows_purpose_ck;
ALTER TABLE oauth_flows ADD CONSTRAINT oauth_flows_purpose_ck
    CHECK (purpose IN ('web_login', 'cli_approval'));
