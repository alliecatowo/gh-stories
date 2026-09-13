DROP TABLE IF EXISTS idempotency_keys, cleanup_jobs, moderation_actions, reports,
    inbox_events, replies, reactions, story_views, media_variants, story_items,
    media_jobs, upload_intents, audience_list_members, hide_rules, blocks, mutes,
    relationships, pending_logins, oauth_flows, sessions CASCADE;
ALTER TABLE IF EXISTS users DROP CONSTRAINT IF EXISTS users_default_audience_fk;
DROP TABLE IF EXISTS audience_lists, users, github_identities CASCADE;
