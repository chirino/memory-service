-- Add status and source_url columns to attachments table
ALTER TABLE attachments ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'ready';
ALTER TABLE attachments ADD COLUMN IF NOT EXISTS source_url VARCHAR(2048);
CREATE INDEX IF NOT EXISTS idx_attachments_status ON attachments(status) WHERE status != 'ready';
