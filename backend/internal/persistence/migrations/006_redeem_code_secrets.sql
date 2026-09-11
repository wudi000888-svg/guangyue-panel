-- Redemption codes remain non-reversible by default.  When a new code is
-- generated, the application stores an encrypted copy using master.key so an
-- authenticated owner can recover it after hiding the plaintext in the UI.
-- Existing rows intentionally remain NULL: their plaintext was never stored.
ALTER TABLE redeem_codes ADD COLUMN code_secret BLOB;
