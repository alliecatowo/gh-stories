-- The Story's intended metadata is captured when the upload is authorized, so
-- that finalize needs no client-supplied audience (which a hostile client
-- could otherwise widen between the two calls).
ALTER TABLE upload_intents
    ADD COLUMN draft    JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN filename TEXT  NOT NULL DEFAULT '';
