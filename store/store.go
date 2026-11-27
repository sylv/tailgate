package store

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/sylv/tailgate/rules"
)

// migrations run in order by file name, and user_version counts how many have run.
// a migration never changes once it's released, a new file does
//
//go:embed migrations/*.sql
var migrations embed.FS

const (
	// sessions slide forward while they're used, up to their max age from when they were made
	SessionLifetime = 60 * 24 * time.Hour
	SessionMaxAge   = 180 * 24 * time.Hour
	// sliding expiry and last used times are only written when this much older, so busy pages don't write on every request
	touchEvery = 5 * time.Minute
)

var (
	ErrNotFound = errors.New("not found")
	// ErrExists is a user name or tailnet login that's already taken
	ErrExists = errors.New("already exists")
)

type Store struct {
	db *sql.DB
}

type User struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Note    string   `json:"note"`
	Email   string   `json:"email"`
	Admin   bool     `json:"admin"`
	AllApps bool     `json:"allApps"`
	Apps    []string `json:"apps"`
	// SkipPasskeyPrompt is set by "don't ask again" on the prompt to add a passkey
	SkipPasskeyPrompt bool `json:"-"`
}

func (u *User) HasAccess(app string) bool {
	return u.AllApps || slices.Contains(u.Apps, app)
}

type Key struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt *int64 `json:"lastUsedAt"`
}

// Login is a tailnet login, ID is its UserProfile.ID
type Login struct {
	ID   int64  `json:"id,string"`
	Name string `json:"name"`
}

type UserDetail struct {
	User
	Keys     []Key     `json:"keys"`
	Passkeys []Passkey `json:"passkeys"`
	Logins   []Login   `json:"logins"`
}

// Cred is what a session was signed in with, a key or a passkey
type Cred struct {
	KeyID, PasskeyID string
}

type Session struct {
	ID   string
	Cred Cred
	User *User
	// Refreshed is set when the expiry moved, so the cookie should be sent again until Expires
	Refreshed bool
	Expires   time.Time
}

func Open(path string) (*Store, error) {
	// _txlock=immediate takes the write lock when a transaction starts, so two writers wait on
	// busy_timeout instead of one failing when it upgrades its read lock
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=synchronous(normal)&_pragma=temp_store(memory)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var done int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&done); err != nil {
		return err
	}
	if done > len(files) {
		return fmt.Errorf("the database is from a newer tailgate, it has %d migrations and this one knows %d", done, len(files))
	}
	for _, name := range files[done:] {
		query, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(query)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(files))); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

// StartSetup creates the "admin" user when there are no users, and returns a
// new setup key while setup isn't done. It returns "" once setup is done
func (s *Store) StartSetup() (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var users int
	var userID string
	if err := tx.QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil {
		return "", err
	}
	if users == 0 {
		userID = rand.Text()
		if _, err := tx.Exec(`INSERT INTO users (id, name, is_admin, has_all_apps) VALUES (?, 'admin', 1, 1)`, userID); err != nil {
			return "", err
		}
	} else {
		err := tx.QueryRow(`DELETE FROM keys WHERE is_setup RETURNING user_id`).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
	}
	key, err := addKey(tx, userID, "", true)
	if err != nil {
		return "", err
	}
	return key, tx.Commit()
}

// SetupPending is whether a setup key hasn't been used yet
func (s *Store) SetupPending() (bool, error) {
	var pending bool
	err := s.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM keys WHERE is_setup)`).Scan(&pending)
	return pending, err
}

// FinishSetup names the setup key's user and swaps the setup key for a real
// one, which it returns. It fails with ErrNotFound for anything but a setup key
func (s *Store) FinishSetup(setupKey, name string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var userID string
	err = tx.QueryRow(`DELETE FROM keys WHERE hash = ? AND is_setup RETURNING user_id`, hash(setupKey)).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE users SET name = ? WHERE id = ?`, name, userID); err != nil {
		return "", uniqueErr(err)
	}
	key, err := addKey(tx, userID, "", false)
	if err != nil {
		return "", err
	}
	return key, tx.Commit()
}

type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

func (s *Store) User(id string) (*User, error) { return user(s.db, id) }

func user(q querier, id string) (*User, error) {
	u := &User{ID: id}
	err := q.QueryRow(`SELECT name, note, email, is_admin, is_admin OR has_all_apps, skip_passkey_prompt FROM users WHERE id = ?`, id).Scan(&u.Name, &u.Note, &u.Email, &u.Admin, &u.AllApps, &u.SkipPasskeyPrompt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(`SELECT app FROM user_apps WHERE user_id = ? ORDER BY app`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	u.Apps = []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		u.Apps = append(u.Apps, name)
	}
	return u, rows.Err()
}

func (s *Store) UserIDByName(name string) (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM users WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// KeyUser returns the user a key belongs to, or ErrNotFound
func (s *Store) KeyUser(key string) (*User, string, error) {
	var keyID, userID string
	err := s.db.QueryRow(`SELECT id, user_id FROM keys WHERE hash = ? AND NOT is_setup`, hash(key)).Scan(&keyID, &userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if err := s.touchKey(keyID); err != nil {
		return nil, "", err
	}
	u, err := s.User(userID)
	return u, keyID, err
}

func (s *Store) touchKey(id string) error {
	now := time.Now()
	_, err := s.db.Exec(`UPDATE keys SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`, now.Unix(), id, now.Add(-touchEvery).Unix())
	return err
}

// LoginUser returns the user a tailnet login is linked to, or ErrNotFound
func (s *Store) LoginUser(loginID int64) (*User, error) {
	var userID string
	err := s.db.QueryRow(`SELECT user_id FROM user_logins WHERE login_id = ?`, loginID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.User(userID)
}

// CreateSession returns a new session token for host. host is "" for the
// tailgate node, and parent is the tailgate node session an app session
// was made from
func (s *Store) CreateSession(userID string, c Cred, host, parent string) (string, error) {
	now := time.Now()
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now.Unix()); err != nil {
		return "", err
	}
	token := rand.Text()
	_, err := s.db.Exec(`INSERT INTO sessions (id, user_id, key_id, passkey_id, host, parent, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		hash(token), userID, null(c.KeyID), null(c.PasskeyID), host, null(parent), now.Unix(), now.Add(SessionLifetime).Unix())
	return token, err
}

func null(s string) sql.Null[string] { return sql.Null[string]{V: s, Valid: s != ""} }

// Session looks up a session token for host and pushes its expiry back,
// along with its parent's, up to its max age. It returns ErrNotFound for unknown and expired ones
func (s *Store) Session(token, host string) (*Session, error) {
	now := time.Now()
	sess := &Session{ID: hash(token)}
	var userID string
	var keyID, passkeyID sql.Null[string]
	var created int64
	err := s.db.QueryRow(`SELECT user_id, key_id, passkey_id, created_at FROM sessions WHERE id = ? AND host = ? AND expires_at > ? AND created_at > ?`,
		sess.ID, host, now.Unix(), now.Add(-SessionMaxAge).Unix()).Scan(&userID, &keyID, &passkeyID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sess.Cred = Cred{KeyID: keyID.V, PasskeyID: passkeyID.V}
	sess.Expires = now.Add(SessionLifetime)
	if end := time.Unix(created, 0).Add(SessionMaxAge); end.Before(sess.Expires) {
		sess.Expires = end
	}
	res, err := s.db.Exec(`UPDATE sessions SET expires_at = min(?1, created_at + ?4) WHERE id IN (?2, (SELECT parent FROM sessions WHERE id = ?2)) AND expires_at < ?3 AND expires_at < created_at + ?4`,
		now.Add(SessionLifetime).Unix(), sess.ID, now.Add(SessionLifetime-touchEvery).Unix(), int64(SessionMaxAge.Seconds()))
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		sess.Refreshed = true
		if err := s.touchKey(sess.Cred.KeyID); err != nil {
			return nil, err
		}
	}
	sess.User, err = s.User(userID)
	return sess, err
}

// DeleteSession ends a session and every session made from it
func (s *Store) DeleteSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Store) Users() ([]UserDetail, error) {
	rows, err := s.db.Query(`SELECT id FROM users ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	users := []UserDetail{}
	for _, id := range ids {
		d, err := s.UserDetail(id)
		if err != nil {
			return nil, err
		}
		users = append(users, *d)
	}
	return users, nil
}

// UserDetail is a user with their keys, passkeys and tailnet logins
func (s *Store) UserDetail(id string) (*UserDetail, error) {
	u, err := s.User(id)
	if err != nil {
		return nil, err
	}
	d := &UserDetail{User: *u, Keys: []Key{}, Logins: []Login{}}
	if d.Passkeys, err = s.Passkeys(id); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, label, created_at, last_used_at FROM keys WHERE user_id = ? AND NOT is_setup ORDER BY created_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.ID, &k.Label, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		d.Keys = append(d.Keys, k)
	}
	rows, err = s.db.Query(`SELECT login_id, login_name FROM user_logins WHERE user_id = ? ORDER BY login_name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l Login
		if err := rows.Scan(&l.ID, &l.Name); err != nil {
			return nil, err
		}
		d.Logins = append(d.Logins, l)
	}
	return d, rows.Err()
}

// SaveUser creates the user when u.ID is "" and updates it otherwise. It
// returns the user's ID. u.Admin is ignored, only SetAdmin changes it. Admins
// reach every app, so their saved AllApps is kept for if they're demoted
func (s *Store) SaveUser(u User) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if u.ID == "" {
		u.ID = rand.Text()
		_, err = tx.Exec(`INSERT INTO users (id, name, note, email, has_all_apps) VALUES (?, ?, ?, ?, ?)`, u.ID, u.Name, u.Note, u.Email, u.AllApps)
	} else {
		var res sql.Result
		res, err = tx.Exec(`UPDATE users SET name = ?, note = ?, email = ?, has_all_apps = CASE WHEN is_admin THEN has_all_apps ELSE ? END WHERE id = ?`, u.Name, u.Note, u.Email, u.AllApps, u.ID)
		if n, _ := rowsAffected(res, err); err == nil && n == 0 {
			return "", ErrNotFound
		}
	}
	if err != nil {
		return "", uniqueErr(err)
	}
	if _, err := tx.Exec(`DELETE FROM user_apps WHERE user_id = ?`, u.ID); err != nil {
		return "", err
	}
	for _, name := range u.Apps {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO user_apps (user_id, app) VALUES (?, ?)`, u.ID, name); err != nil {
			return "", err
		}
	}
	return u.ID, tx.Commit()
}

func (s *Store) SetAdmin(id string, admin bool) error {
	res, err := s.db.Exec(`UPDATE users SET is_admin = ? WHERE id = ?`, admin, id)
	return found(res, err)
}

func (s *Store) DeleteUser(id string) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return found(res, err)
}

func (s *Store) AddKey(userID, label string) (string, error) {
	key, err := addKey(s.db, userID, label, false)
	if isConstraint(err, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY) {
		return "", ErrNotFound
	}
	return key, err
}

func addKey(q querier, userID, label string, setup bool) (string, error) {
	// setup keys don't start with tg_, so signing in skips them before looking them up
	key := "tg_" + rand.Text()
	if setup {
		key = "tgs_" + rand.Text()
	}
	_, err := q.Exec(`INSERT INTO keys (id, user_id, hash, label, created_at, is_setup) VALUES (?, ?, ?, ?, ?, ?)`, rand.Text(), userID, hash(key), label, time.Now().Unix(), setup)
	return key, err
}

// RevokeKey deletes a key, which ends every session made with it
func (s *Store) RevokeKey(id string) error {
	res, err := s.db.Exec(`DELETE FROM keys WHERE id = ?`, id)
	return found(res, err)
}

// ResetKeys revokes all of a user's keys and passkeys and returns one new key
func (s *Store) ResetKeys(userID string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := user(tx, userID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`DELETE FROM keys WHERE user_id = ?`, userID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`DELETE FROM passkeys WHERE user_id = ?`, userID); err != nil {
		return "", err
	}
	key, err := addKey(tx, userID, "", false)
	if err != nil {
		return "", err
	}
	return key, tx.Commit()
}

func (s *Store) LinkLogin(userID string, l Login) error {
	_, err := s.db.Exec(`INSERT INTO user_logins (user_id, login_id, login_name) VALUES (?, ?, ?)`, userID, l.ID, l.Name)
	if isConstraint(err, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY) {
		return ErrNotFound
	}
	return uniqueErr(err)
}

func (s *Store) UnlinkLogin(loginID int64) error {
	res, err := s.db.Exec(`DELETE FROM user_logins WHERE login_id = ?`, loginID)
	return found(res, err)
}

func rowsAffected(res sql.Result, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func found(res sql.Result, err error) error {
	n, err := rowsAffected(res, err)
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

func uniqueErr(err error) error {
	if isConstraint(err, sqlite3.SQLITE_CONSTRAINT_UNIQUE) || isConstraint(err, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY) {
		return ErrExists
	}
	return err
}

func isConstraint(err error, code int) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && e.Code() == code
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Rules returns the saved rules in order
func (s *Store) Rules() ([]rules.Rule, error) {
	rows, err := s.db.Query(`SELECT id, section, name, condition, effects, mode FROM rules ORDER BY position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []rules.Rule{}
	for rows.Next() {
		var r rules.Rule
		var effects string
		if err := rows.Scan(&r.ID, &r.Section, &r.Name, &r.Condition, &effects, &r.Mode); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(effects), &r.Effects); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

// SaveRules replaces every rule
func (s *Store) SaveRules(list []rules.Rule) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM rules`); err != nil {
		return err
	}
	for i, r := range list {
		effects, err := json.Marshal(r.Effects)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO rules (id, section, position, name, condition, effects, mode) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.Section, i, r.Name, r.Condition, string(effects), r.Mode); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type OIDCClient struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	App          string   `json:"app"`
	RedirectURIs []string `json:"redirectURIs"`
	CreatedAt    int64    `json:"createdAt"`
}

func (s *Store) OIDCClients() ([]OIDCClient, error) {
	rows, err := s.db.Query(`SELECT id, name, app, redirect_uris, created_at FROM oidc_clients ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []OIDCClient{}
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *c)
	}
	return list, rows.Err()
}

func (s *Store) OIDCClient(id string) (*OIDCClient, error) {
	return scanClient(s.db.QueryRow(`SELECT id, name, app, redirect_uris, created_at FROM oidc_clients WHERE id = ?`, id))
}

// OIDCClientBySecret returns the client when the secret is its own, or ErrNotFound
func (s *Store) OIDCClientBySecret(id, secret string) (*OIDCClient, error) {
	return scanClient(s.db.QueryRow(`SELECT id, name, app, redirect_uris, created_at FROM oidc_clients WHERE id = ? AND secret_hash = ?`, id, hash(secret)))
}

func scanClient(row interface{ Scan(...any) error }) (*OIDCClient, error) {
	var c OIDCClient
	var uris string
	err := row.Scan(&c.ID, &c.Name, &c.App, &uris, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, json.Unmarshal([]byte(uris), &c.RedirectURIs)
}

// SaveOIDCClient creates the client when c.ID is "" and returns its new
// secret, and otherwise updates it and keeps the secret
func (s *Store) SaveOIDCClient(c OIDCClient) (id, secret string, err error) {
	uris, err := json.Marshal(c.RedirectURIs)
	if err != nil {
		return "", "", err
	}
	if c.ID != "" {
		res, err := s.db.Exec(`UPDATE oidc_clients SET name = ?, app = ?, redirect_uris = ? WHERE id = ?`, c.Name, c.App, string(uris), c.ID)
		return c.ID, "", found(res, err)
	}
	id, secret = rand.Text(), rand.Text()
	_, err = s.db.Exec(`INSERT INTO oidc_clients (id, name, app, redirect_uris, secret_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, c.Name, c.App, string(uris), hash(secret), time.Now().Unix())
	return id, secret, err
}

func (s *Store) DeleteOIDCClient(id string) error {
	res, err := s.db.Exec(`DELETE FROM oidc_clients WHERE id = ?`, id)
	return found(res, err)
}

// SigningKey is the key ID tokens are signed with, made on first use
func (s *Store) SigningKey() (*rsa.PrivateKey, error) {
	var der []byte
	err := s.db.QueryRow(`SELECT private_key FROM oidc_signing_key`).Scan(&der)
	if errors.Is(err, sql.ErrNoRows) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}
		der, _ = x509.MarshalPKCS8PrivateKey(key)
		_, err = s.db.Exec(`INSERT INTO oidc_signing_key (id, private_key) VALUES (1, ?)`, der)
		return key, err
	}
	if err != nil {
		return nil, err
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	rk, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the signing key isn't an RSA key")
	}
	return rk, nil
}

type Passkey struct {
	ID string `json:"id"`
	// AAGUID names the authenticator model, all zeros when it doesn't say
	AAGUID     string `json:"aaguid"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt *int64 `json:"lastUsedAt"`
	UserAgent  string `json:"userAgent"`
	// Credential is the webauthn.Credential json
	Credential []byte `json:"-"`
}

func (s *Store) Passkeys(userID string) ([]Passkey, error) {
	rows, err := s.db.Query(`SELECT id, aaguid, created_at, last_used_at, user_agent, credential FROM passkeys WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Passkey{}
	for rows.Next() {
		var p Passkey
		if err := rows.Scan(&p.ID, &p.AAGUID, &p.CreatedAt, &p.LastUsedAt, &p.UserAgent, &p.Credential); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

func (s *Store) AddPasskey(userID string, credentialID, credential []byte, aaguid, userAgent string) error {
	_, err := s.db.Exec(`INSERT INTO passkeys (id, user_id, credential_id, credential, aaguid, created_at, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rand.Text(), userID, credentialID, string(credential), aaguid, time.Now().Unix(), userAgent)
	if isConstraint(err, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY) {
		return ErrNotFound
	}
	return uniqueErr(err)
}

// PasskeyUser returns the id and user of the passkey with this credential id, or ErrNotFound
func (s *Store) PasskeyUser(credentialID []byte) (string, string, error) {
	var id, userID string
	err := s.db.QueryRow(`SELECT id, user_id FROM passkeys WHERE credential_id = ?`, credentialID).Scan(&id, &userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return id, userID, err
}

// UsePasskey saves a passkey's credential after a sign in, which moves its sign count on
func (s *Store) UsePasskey(id string, credential []byte, userAgent string) error {
	res, err := s.db.Exec(`UPDATE passkeys SET credential = ?, last_used_at = ?, user_agent = ? WHERE id = ?`, string(credential), time.Now().Unix(), userAgent, id)
	return found(res, err)
}

// DeletePasskey removes a passkey, which ends every session made with it
func (s *Store) DeletePasskey(id string) error {
	res, err := s.db.Exec(`DELETE FROM passkeys WHERE id = ?`, id)
	return found(res, err)
}

func (s *Store) SkipPasskeyPrompt(userID string) error {
	res, err := s.db.Exec(`UPDATE users SET skip_passkey_prompt = 1 WHERE id = ?`, userID)
	return found(res, err)
}

// Nodes maps the app of each node tailgate started to its device id, "" until it joined
func (s *Store) Nodes() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT app, device_id FROM nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := map[string]string{}
	for rows.Next() {
		var app, id string
		if err := rows.Scan(&app, &id); err != nil {
			return nil, err
		}
		nodes[app] = id
	}
	return nodes, rows.Err()
}

func (s *Store) SaveNode(app, deviceID string) error {
	_, err := s.db.Exec(`INSERT INTO nodes (app, device_id) VALUES (?, ?) ON CONFLICT (app) DO UPDATE SET device_id = excluded.device_id`, app, deviceID)
	return err
}

func (s *Store) DeleteNode(app string) error {
	_, err := s.db.Exec(`DELETE FROM nodes WHERE app = ?`, app)
	return err
}

// CertValid is whether tailgate fetched a cert for domain that hasn't expired
func (s *Store) CertValid(domain string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM certs WHERE domain = ? AND expires_at > ?)`, domain, time.Now().Unix()).Scan(&ok)
	return ok, err
}

func (s *Store) SaveCert(domain string, expires time.Time) error {
	_, err := s.db.Exec(`INSERT INTO certs (domain, expires_at) VALUES (?, ?) ON CONFLICT (domain) DO UPDATE SET expires_at = excluded.expires_at`, domain, expires.Unix())
	return err
}
