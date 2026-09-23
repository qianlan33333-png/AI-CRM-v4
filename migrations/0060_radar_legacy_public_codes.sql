-- Owner: Radar. Forward-only compatibility for immutable production v1 share codes.
-- New Radar writes continue to generate rd_ codes; this alternative only keeps
-- already-distributed v1 /r/{code} URLs resolvable after the one-time import.

ALTER TABLE radar_links DROP CONSTRAINT IF EXISTS radar_links_public_code_check;
ALTER TABLE radar_links
  ADD CONSTRAINT radar_links_public_code_check
  CHECK(public_code ~ '^(rd_[A-Za-z0-9_-]{16,64}|[A-Za-z0-9]{1,8})$');
