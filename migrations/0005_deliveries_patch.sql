-- Feature 003 US4 (T041/T043): store the verified patch bytes on the deliveries row so
-- /diff <id> can attach it and the merge gate can bind its approval to the exact diff hash
-- (deliveries.diff_sha256, SC-002) without re-deriving it from anywhere else.
ALTER TABLE deliveries ADD COLUMN patch BLOB;
