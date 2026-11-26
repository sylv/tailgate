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
	SessionLifetime = 60 * 24 * time.Hour
	// sliding expiry and last used times are only written when this much older, so busy pages don't write on every request
	touchEvery = 5 * time.Minute
)

var (
	ErrNotEmpty = errors.New("tailgate already has users")
	ErrNotFound = errors.New("not found")
	// ErrExists is a user name or tailnet login that's already taken
	ErrExists = errors.New("already exists")
)

type Store struct {
	db *sql.DB
}

type User struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Note        string   `json:"note"`
	Email       string   `json:"email"`
	Admin       bool     `json:"admin"`
	AllServices bool     `json:"allServices"`
	Services    []string `json:"services"`
}

func (u *User) HasAccess(service string) bool {
	return u.AllServices || slices.Contains(u.Services, service)
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
	Keys   []Key   `json:"keys"`
	Logins []Login `json:"logins"`
}

type Session struct {
	ID    string
	KeyID string
	User  *User
	// Refreshed is set when the expiry moved, so the cookie should be sent again
	Refreshed bool
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
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

func (s *Store) HasUsers() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	return n > 0, err
}

// CreateFirstAdmin creates an admin with access to every service and returns
// their key. It fails with ErrNotEmpty once any user exists
func (s *Store) CreateFirstAdmin(name string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
		return "", err
	}
	if n > 0 {
		return "", ErrNotEmpty
	}
	id := rand.Text()
	if _, err := tx.Exec(`INSERT INTO users (id, name, is_admin, has_all_services) VALUES (?, ?, 1, 1)`, id, name); err != nil {
		return "", err
	}
	key, err := addKey(tx, id, "")
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
	err := q.QueryRow(`SELECT name, note, email, is_admin, has_all_services FROM users WHERE id = ?`, id).Scan(&u.Name, &u.Note, &u.Email, &u.Admin, &u.AllServices)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(`SELECT service FROM user_services WHERE user_id = ? ORDER BY service`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	u.Services = []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		u.Services = append(u.Services, name)
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
	err := s.db.QueryRow(`SELECT id, user_id FROM keys WHERE hash = ?`, hash(key)).Scan(&keyID, &userID)
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
// tailgate node, and parent is the tailgate node session a service session
// was made from
func (s *Store) CreateSession(userID, keyID, host, parent string) (string, error) {
	now := time.Now()
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now.Unix()); err != nil {
		return "", err
	}
	token := rand.Text()
	_, err := s.db.Exec(`INSERT INTO sessions (id, user_id, key_id, host, parent, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		hash(token), userID, keyID, host, sql.Null[string]{V: parent, Valid: parent != ""}, now.Add(SessionLifetime).Unix())
	return token, err
}

// Session looks up a session token for host and pushes its expiry back,
// along with its parent's. It returns ErrNotFound for unknown and expired ones
func (s *Store) Session(token, host string) (*Session, error) {
	now := time.Now()
	sess := &Session{ID: hash(token)}
	var userID string
	err := s.db.QueryRow(`SELECT user_id, key_id FROM sessions WHERE id = ? AND host = ? AND expires_at > ?`, sess.ID, host, now.Unix()).Scan(&userID, &sess.KeyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	expires := now.Add(SessionLifetime)
	res, err := s.db.Exec(`UPDATE sessions SET expires_at = ?1 WHERE id IN (?2, (SELECT parent FROM sessions WHERE id = ?2)) AND expires_at < ?3`,
		expires.Unix(), sess.ID, expires.Add(-touchEvery).Unix())
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		sess.Refreshed = true
		if err := s.touchKey(sess.KeyID); err != nil {
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
		u, err := s.User(id)
		if err != nil {
			return nil, err
		}
		d := UserDetail{User: *u, Keys: []Key{}, Logins: []Login{}}
		rows, err := s.db.Query(`SELECT id, label, created_at, last_used_at FROM keys WHERE user_id = ? ORDER BY created_at`, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k Key
			if err := rows.Scan(&k.ID, &k.Label, &k.CreatedAt, &k.LastUsedAt); err != nil {
				rows.Close()
				return nil, err
			}
			d.Keys = append(d.Keys, k)
		}
		rows.Close()
		rows, err = s.db.Query(`SELECT login_id, login_name FROM user_logins WHERE user_id = ? ORDER BY login_name`, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var l Login
			if err := rows.Scan(&l.ID, &l.Name); err != nil {
				rows.Close()
				return nil, err
			}
			d.Logins = append(d.Logins, l)
		}
		rows.Close()
		users = append(users, d)
	}
	return users, nil
}

// SaveUser creates the user when u.ID is "" and updates it otherwise. It
// returns the user's ID. u.Admin is ignored, only SetAdmin changes it
func (s *Store) SaveUser(u User) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if u.ID == "" {
		u.ID = rand.Text()
		_, err = tx.Exec(`INSERT INTO users (id, name, note, email, has_all_services) VALUES (?, ?, ?, ?, ?)`, u.ID, u.Name, u.Note, u.Email, u.AllServices)
	} else {
		var res sql.Result
		res, err = tx.Exec(`UPDATE users SET name = ?, note = ?, email = ?, has_all_services = ? WHERE id = ?`, u.Name, u.Note, u.Email, u.AllServices, u.ID)
		if n, _ := rowsAffected(res, err); err == nil && n == 0 {
			return "", ErrNotFound
		}
	}
	if err != nil {
		return "", uniqueErr(err)
	}
	if _, err := tx.Exec(`DELETE FROM user_services WHERE user_id = ?`, u.ID); err != nil {
		return "", err
	}
	for _, name := range u.Services {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO user_services (user_id, service) VALUES (?, ?)`, u.ID, name); err != nil {
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
	key, err := addKey(s.db, userID, label)
	if isConstraint(err, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY) {
		return "", ErrNotFound
	}
	return key, err
}

func addKey(q querier, userID, label string) (string, error) {
	key := "tg_" + rand.Text()
	_, err := q.Exec(`INSERT INTO keys (id, user_id, hash, label, created_at) VALUES (?, ?, ?, ?, ?)`, rand.Text(), userID, hash(key), label, time.Now().Unix())
	return key, err
}

// RevokeKey deletes a key, which ends every session made with it
func (s *Store) RevokeKey(id string) error {
	res, err := s.db.Exec(`DELETE FROM keys WHERE id = ?`, id)
	return found(res, err)
}

// ResetKeys revokes all of a user's keys and returns one new key
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
	key, err := addKey(tx, userID, "")
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
	Service      string   `json:"service"`
	RedirectURIs []string `json:"redirectURIs"`
	CreatedAt    int64    `json:"createdAt"`
}

func (s *Store) OIDCClients() ([]OIDCClient, error) {
	rows, err := s.db.Query(`SELECT id, name, service, redirect_uris, created_at FROM oidc_clients ORDER BY name`)
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
	return scanClient(s.db.QueryRow(`SELECT id, name, service, redirect_uris, created_at FROM oidc_clients WHERE id = ?`, id))
}

// OIDCClientBySecret returns the client when the secret is its own, or ErrNotFound
func (s *Store) OIDCClientBySecret(id, secret string) (*OIDCClient, error) {
	return scanClient(s.db.QueryRow(`SELECT id, name, service, redirect_uris, created_at FROM oidc_clients WHERE id = ? AND secret_hash = ?`, id, hash(secret)))
}

func scanClient(row interface{ Scan(...any) error }) (*OIDCClient, error) {
	var c OIDCClient
	var uris string
	err := row.Scan(&c.ID, &c.Name, &c.Service, &uris, &c.CreatedAt)
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
		res, err := s.db.Exec(`UPDATE oidc_clients SET name = ?, service = ?, redirect_uris = ? WHERE id = ?`, c.Name, c.Service, string(uris), c.ID)
		return c.ID, "", found(res, err)
	}
	id, secret = rand.Text(), rand.Text()
	_, err = s.db.Exec(`INSERT INTO oidc_clients (id, name, service, redirect_uris, secret_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, c.Name, c.Service, string(uris), hash(secret), time.Now().Unix())
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
