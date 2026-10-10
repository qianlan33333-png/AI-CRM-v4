-- Owner: Customer. Preserve mixed-source display_name unchanged and leave
-- historical WeChat nicknames unknown; only trusted WeChat OAuth profile
-- observations populate this separate field. Forward-only.
ALTER TABLE customer_directory_projection
  ADD COLUMN wechat_nickname TEXT NOT NULL DEFAULT '';
