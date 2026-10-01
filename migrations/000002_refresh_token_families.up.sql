-- Refresh-token families: a login starts a family and every refresh continues
-- it. Reusing an already-rotated token revokes the whole family.
-- Existing rows each get their own family.
ALTER TABLE refresh_tokens
    ADD COLUMN family_id UUID NOT NULL DEFAULT gen_random_uuid();

CREATE INDEX idx_refresh_tokens_family ON refresh_tokens(family_id);
