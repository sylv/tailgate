CREATE TABLE passkeys (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	-- the authenticator's own id for the credential, which it sends when signing in
	credential_id BLOB NOT NULL UNIQUE,
	-- a json webauthn.Credential, with the public key and sign count
	credential TEXT NOT NULL,
	-- the authenticator model, named from the public aaguid list when shown
	aaguid TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	last_used_at INTEGER,
	-- the browser it was last used from, parsed when shown
	user_agent TEXT NOT NULL DEFAULT ''
) STRICT;

-- set by "don't ask again" on the prompt to add a passkey after a key sign in
ALTER TABLE users ADD COLUMN skip_passkey_prompt INTEGER NOT NULL DEFAULT 0 CHECK (skip_passkey_prompt IN (0, 1));

-- sessions are signed in with a key or a passkey, and get an absolute lifetime from created_at
CREATE TABLE sessions_new (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	-- what the user signed in with, so removing it ends the session
	key_id TEXT REFERENCES keys(id) ON DELETE CASCADE,
	passkey_id TEXT REFERENCES passkeys(id) ON DELETE CASCADE,
	-- the app the cookie is for, '' for the tailgate node
	host TEXT NOT NULL,
	-- the tailgate node session an app session was made from, so signing out there ends this too
	parent TEXT REFERENCES sessions_new(id) ON DELETE CASCADE,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	CHECK ((key_id IS NULL) != (passkey_id IS NULL))
) STRICT;
-- parents first, since the foreign keys are checked on every insert
INSERT INTO sessions_new (id, user_id, key_id, host, parent, created_at, expires_at)
	SELECT id, user_id, key_id, host, parent, unixepoch(), expires_at FROM sessions ORDER BY parent IS NOT NULL;
DROP TABLE sessions;
ALTER TABLE sessions_new RENAME TO sessions;

-- each app node tailgate started. its state dir is kept until the device is deleted in tailscale
CREATE TABLE nodes (
	app TEXT PRIMARY KEY,
	-- tailscale's stable node id, '' until the node has joined
	device_id TEXT NOT NULL DEFAULT ''
) STRICT;

-- the certs tailgate fetched, so a restart knows which ones tailscale already has and which have to wait their turn
CREATE TABLE certs (
	domain TEXT PRIMARY KEY,
	expires_at INTEGER NOT NULL
) STRICT;
