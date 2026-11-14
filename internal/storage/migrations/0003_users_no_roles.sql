-- ADR-011: no roles; every authenticated user is equal. Drop the unused column (its own CHECK
-- constraint goes with it). No code ever wrote users before this migration, so nothing is lost.
ALTER TABLE users DROP COLUMN role;
