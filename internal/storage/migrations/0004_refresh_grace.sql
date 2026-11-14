-- Refresh-token reuse grace window (docs/design/architecture.md §10.1). When a refresh token is
-- rotated, the token pair issued in exchange is kept on the *used* row, sealed with the master
-- key (AES-GCM, AAD "refresh_tokens.successor"), so that a second request presenting the same
-- token within the grace window (about 10 s, two browser tabs racing) can receive the same pair
-- instead of triggering family revocation. The column is set to NULL as soon as the window has
-- passed (inline on the next refresh and by the periodic auth maintenance job), so a plaintext
-- refresh token is never recoverable from the database for longer than the window.
ALTER TABLE refresh_tokens ADD COLUMN successor_sealed BLOB;
