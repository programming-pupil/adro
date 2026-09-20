-- Preserve an integrity proof for encrypted blob bytes after key destruction.
-- Existing rows receive an empty marker because PostgreSQL cannot derive the
-- application SHA-256 contract without requiring an extension; the adapter
-- fails closed for tombstoned rows until they are rewritten with a digest.
ALTER TABLE runtime_blobs
  ADD COLUMN IF NOT EXISTS stored_digest text NOT NULL DEFAULT '';
