-- Public launch: aggregate anonymous view counting.
--
-- Privacy decision (docs/public-launch-plan.md): for public Stories viewed
-- without a session we count anonymous views only. No per-viewer row and no
-- IP-address history is retained for anonymous callers. Authenticated views
-- keep using story_views as before.
ALTER TABLE story_items
    ADD COLUMN IF NOT EXISTS anonymous_view_count BIGINT NOT NULL DEFAULT 0;
