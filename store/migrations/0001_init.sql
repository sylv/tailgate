-- ids are random base32 from crypto/rand.Text, times are unix seconds and
-- hashes are hex sha256, since keys and tokens are random enough not to need a slow hash

CREATE TABLE users (
	id TEXT PRIMARY KEY,
	-- the username, see userName in web.go
	name TEXT NOT NULL UNIQUE,
	note TEXT NOT NULL DEFAULT '',
	email TEXT NOT NULL DEFAULT '',
	is_admin INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
	-- every service, including ones added later, instead of the user_services rows
	has_all_services INTEGER NOT NULL DEFAULT 0 CHECK (has_all_services IN (0, 1))
) STRICT;

CREATE TABLE user_services (
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	-- a tailgate.id label, which may have no container right now
	service TEXT NOT NULL,
	PRIMARY KEY (user_id, service)
) STRICT;

-- tailnet logins that sign a user in automatically
CREATE TABLE user_logins (
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	-- tailscale's UserProfile.ID, which never changes
	login_id INTEGER PRIMARY KEY,
	-- only for display, it changes when an account is renamed
	login_name TEXT NOT NULL
) STRICT;

CREATE TABLE keys (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	-- the tg_ key itself is only shown once
	hash TEXT NOT NULL UNIQUE,
	label TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	last_used_at INTEGER
) STRICT;

CREATE TABLE sessions (
	-- the hash of the cookie's token
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	-- the key the user signed in with, so revoking it ends the session
	key_id TEXT NOT NULL REFERENCES keys(id) ON DELETE CASCADE,
	-- the service the cookie is for, '' for the tailgate node
	host TEXT NOT NULL,
	-- the tailgate node session a service session was made from, so signing out there ends this too
	parent TEXT REFERENCES sessions(id) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
) STRICT;

CREATE TABLE rules (
	id TEXT PRIMARY KEY,
	section TEXT NOT NULL CHECK (section IN ('pre', 'post')),
	-- the order rules run in within the whole list
	position INTEGER NOT NULL,
	name TEXT NOT NULL,
	-- an expr expression
	condition TEXT NOT NULL,
	-- a json array of rules.Effect
	effects TEXT NOT NULL,
	mode TEXT NOT NULL CHECK (mode IN ('on', 'log', 'off'))
) STRICT;

CREATE TABLE oidc_clients (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	-- only users with access to this service get a code
	service TEXT NOT NULL,
	-- a json array, matched exactly
	redirect_uris TEXT NOT NULL,
	secret_hash TEXT NOT NULL,
	created_at INTEGER NOT NULL
) STRICT;

-- the RSA key that signs OIDC id tokens, made on first use. there's only ever one row
CREATE TABLE oidc_signing_key (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	-- PKCS#8 in DER, the binary form of a PEM private key
	private_key BLOB NOT NULL
) STRICT;
