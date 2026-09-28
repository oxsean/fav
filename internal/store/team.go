package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Roles of a user in the instance.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// LocalUser is the server host's user: the first admin, and the owner of the tokens tend-server token makes.
const LocalUser = "local"

// Credential kinds.
const (
	KindWeb   = "web"   // a browser's session
	KindToken = "token" // a personal token of a CLI or TUI
	KindNode  = "node"  // a machine's node, named by the machine
)

// Admission rule kinds.
const (
	AdmitEmail  = "email"  // a verified email
	AdmitDomain = "domain" // any verified email of the domain
	AdmitLogin  = "login"  // provider:username
)

var (
	ErrNotAdmitted  = errors.New("not admitted")
	ErrDisabled     = errors.New("user disabled")
	ErrExists       = errors.New("exists")
	ErrOtherMachine = errors.New("bound to another machine")
	ErrNotFound     = errors.New("not found")
)

type User struct {
	ID       string    `json:"id"`
	Email    string    `json:"email,omitempty"`
	Name     string    `json:"name,omitempty"`
	Username string    `json:"username,omitempty"`
	Role     string    `json:"role"`
	Disabled bool      `json:"disabled,omitempty"`
	Created  time.Time `json:"created,omitzero"`
}

// Identity is an account at a login provider: (provider, issuer, subject) is its key.
type Identity struct {
	Provider      string `json:"provider"`
	Issuer        string `json:"issuer"`
	Subject       string `json:"subject"`
	Username      string `json:"username,omitempty"`
	Email         string `json:"email,omitempty"`
	EmailVerified bool   `json:"email_verified,omitempty"`
	Name          string `json:"name,omitempty"`
}

type Admit struct {
	Kind    string    `json:"kind"`
	Value   string    `json:"value"`
	Role    string    `json:"role"`
	AddedBy string    `json:"added_by,omitempty"`
	Created time.Time `json:"created,omitzero"`
}

// Credential is a secret someone signs in with; only its hash (Sum) is kept.
type Credential struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Name     string    `json:"name,omitempty"`
	Owner    string    `json:"owner"`
	Sum      string    `json:"-"`
	NodeID   string    `json:"node_id,omitempty"`
	Host     string    `json:"host,omitempty"`
	Created  time.Time `json:"created,omitzero"`
	LastUsed time.Time `json:"last_used,omitzero"`
	Expires  time.Time `json:"expires,omitzero"`
}

type AuditEntry struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor,omitempty"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail,omitempty"`
	IP     string    `json:"ip,omitempty"`
}

// Team is the server's people and their credentials, in the same database as the log.
type Team struct {
	w, r *sql.DB
}

// OpenTeam opens the database at path for the team's tables, migrating it as Open does.
func OpenTeam(path string) (*Team, error) {
	if err := private(path); err != nil {
		return nil, err
	}
	w, err := sql.Open("sqlite", path+"?"+pragmas+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	ro, err := migrate(w, path)
	if err == nil && ro != nil {
		err = ro
	}
	if err != nil {
		w.Close()
		return nil, err
	}
	r, err := reader(path)
	if err != nil {
		w.Close()
		return nil, err
	}
	return &Team{w: w, r: r}, nil
}

func (t *Team) Close() error { return errors.Join(t.r.Close(), t.w.Close()) }

// Sum is how a secret is stored and looked up.
func Sum(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

func newSecret() string {
	var b [24]byte
	rand.Read(b[:])
	return "tend_" + hex.EncodeToString(b[:])
}

func newID(prefix string) string {
	var b [6]byte
	rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

func nanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func fromNanos(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

const userCols = `id, email, name, username, role, disabled, created`

type scanner interface{ Scan(...any) error }

func scanUser(s scanner) (User, error) {
	var u User
	var created int64
	err := s.Scan(&u.ID, &u.Email, &u.Name, &u.Username, &u.Role, &u.Disabled, &created)
	u.Created = fromNanos(created)
	return u, err
}

// Users are everyone, the server host first.
func (t *Team) Users() ([]User, error) {
	rows, err := t.r.Query(`SELECT ` + userCols + ` FROM users ORDER BY created, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (t *Team) User(id string) (User, bool, error) {
	u, err := scanUser(t.r.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	return u, err == nil, err
}

// SetUser changes a user's role or disables them (nil leaves it).
func (t *Team) SetUser(id string, role *string, disabled *bool) error {
	return inTx(t.w, func(tx *sql.Tx) error {
		if role != nil {
			if *role != RoleAdmin && *role != RoleMember {
				return fmt.Errorf("role %q", *role)
			}
			if err := affected(tx.Exec(`UPDATE users SET role = ? WHERE id = ?`, *role, id)); err != nil {
				return err
			}
		}
		if disabled != nil {
			if err := affected(tx.Exec(`UPDATE users SET disabled = ? WHERE id = ?`, *disabled, id)); err != nil {
				return err
			}
			if *disabled {
				_, err := tx.Exec(`UPDATE credentials SET revoked = 1 WHERE owner = ? AND kind != ?`, id, KindNode)
				return err
			}
		}
		return nil
	})
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Admit is the user an identity signs in as: the one it is linked to, or a new user an invite or an admission rule
// lets in. An email counts only when the provider verified it, and never links an identity to a user who already
// has one: linking is the user's own act (Link).
func (t *Team) Admit(id Identity, invite string) (User, error) {
	var out User
	err := inTx(t.w, func(tx *sql.Tx) error {
		u, err := scanUser(tx.QueryRow(`SELECT u.id, u.email, u.name, u.username, u.role, u.disabled, u.created FROM identities i
			JOIN users u ON u.id = i.user_id WHERE i.provider = ? AND i.issuer = ? AND i.subject = ?`, id.Provider, id.Issuer, id.Subject))
		switch {
		case err == nil && u.Disabled:
			return ErrDisabled
		case err == nil:
			out = u
			_, err = tx.Exec(`UPDATE identities SET username = ?, email = ?, email_verified = ? WHERE provider = ? AND issuer = ? AND subject = ?`,
				id.Username, id.Email, id.EmailVerified, id.Provider, id.Issuer, id.Subject)
			return err
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		role, err := admission(tx, id, invite)
		if err != nil {
			return err
		}
		email := ""
		if id.EmailVerified {
			email = strings.ToLower(id.Email)
		}
		out = User{ID: newID("u_"), Email: email, Name: id.Name, Username: id.Username, Role: role, Created: time.Now().UTC()}
		if _, err := tx.Exec(`INSERT INTO users (`+userCols+`) VALUES (?, ?, ?, ?, ?, 0, ?)`,
			out.ID, out.Email, out.Name, out.Username, out.Role, nanos(out.Created)); err != nil {
			return err
		}
		if invite != "" {
			if _, err := tx.Exec(`UPDATE invites SET used_by = ?, used_at = ? WHERE sum = ?`, out.ID, time.Now().UnixNano(), Sum(invite)); err != nil {
				return err
			}
		}
		return link(tx, out.ID, id)
	})
	return out, err
}

// admission is the role a new identity gets in: from an unused invite, else from the first rule it meets.
func admission(tx *sql.Tx, id Identity, invite string) (string, error) {
	if invite != "" {
		var role string
		err := tx.QueryRow(`SELECT role FROM invites WHERE sum = ? AND used_by = '' AND expires > ?`, Sum(invite), time.Now().UnixNano()).Scan(&role)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotAdmitted
		}
		return role, err
	}
	email := strings.ToLower(id.Email)
	if id.EmailVerified && email != "" {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM users WHERE email = ?`, email).Scan(&n); err != nil {
			return "", err
		}
		if n > 0 { // someone signs in with it already: another account of theirs is linked by them, not here
			return "", ErrNotAdmitted
		}
	}
	type rule struct{ kind, value string }
	var rules []rule
	if id.EmailVerified && email != "" {
		_, domain, _ := strings.Cut(email, "@")
		rules = append(rules, rule{AdmitEmail, email}, rule{AdmitDomain, domain})
	}
	if id.Username != "" {
		rules = append(rules, rule{AdmitLogin, id.Provider + ":" + strings.ToLower(id.Username)})
	}
	for _, r := range rules {
		var role string
		err := tx.QueryRow(`SELECT role FROM admits WHERE kind = ? AND value = ?`, r.kind, r.value).Scan(&role)
		if err == nil {
			return role, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return "", ErrNotAdmitted
}

func link(tx *sql.Tx, user string, id Identity) error {
	_, err := tx.Exec(`INSERT INTO identities (provider, issuer, subject, user_id, username, email, email_verified, created) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id.Provider, id.Issuer, id.Subject, user, id.Username, id.Email, id.EmailVerified, time.Now().UnixNano())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrExists
	}
	return err
}

// Link adds another account to user: they proved control of both by signing in with each.
func (t *Team) Link(user string, id Identity) error {
	return inTx(t.w, func(tx *sql.Tx) error { return link(tx, user, id) })
}

// Identities are user's linked accounts.
func (t *Team) Identities(user string) ([]Identity, error) {
	rows, err := t.r.Query(`SELECT provider, issuer, subject, username, email, email_verified FROM identities WHERE user_id = ? ORDER BY created`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Identity
	for rows.Next() {
		var id Identity
		if err := rows.Scan(&id.Provider, &id.Issuer, &id.Subject, &id.Username, &id.Email, &id.EmailVerified); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (t *Team) AddAdmit(a Admit) error {
	a.Value = strings.ToLower(strings.TrimSpace(a.Value))
	if a.Role != RoleAdmin && a.Role != RoleMember {
		return fmt.Errorf("role %q", a.Role)
	}
	if a.Value == "" || a.Kind != AdmitEmail && a.Kind != AdmitDomain && a.Kind != AdmitLogin {
		return fmt.Errorf("admit %s %q", a.Kind, a.Value)
	}
	_, err := t.w.Exec(`INSERT INTO admits (kind, value, role, added_by, created) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (kind, value) DO UPDATE SET role = excluded.role`, a.Kind, a.Value, a.Role, a.AddedBy, time.Now().UnixNano())
	return err
}

func (t *Team) RemoveAdmit(kind, value string) error {
	return affected(t.w.Exec(`DELETE FROM admits WHERE kind = ? AND value = ?`, kind, strings.ToLower(value)))
}

func (t *Team) Admits() ([]Admit, error) {
	rows, err := t.r.Query(`SELECT kind, value, role, added_by, created FROM admits ORDER BY kind, value`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Admit
	for rows.Next() {
		var a Admit
		var created int64
		if err := rows.Scan(&a.Kind, &a.Value, &a.Role, &a.AddedBy, &created); err != nil {
			return nil, err
		}
		a.Created = fromNanos(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

// NewInvite is a one-time secret that lets one person in with role until ttl passes.
func (t *Team) NewInvite(role, by string, ttl time.Duration) (string, error) {
	if role != RoleAdmin && role != RoleMember {
		return "", fmt.Errorf("role %q", role)
	}
	secret := newSecret()
	now := time.Now()
	_, err := t.w.Exec(`INSERT INTO invites (sum, role, created_by, created, expires) VALUES (?, ?, ?, ?, ?)`,
		Sum(secret), role, by, now.UnixNano(), now.Add(ttl).UnixNano())
	return secret, err
}

// InviteInfo is an invitation's public facts: who sent it, what role it grants, and when it stops working.
type InviteInfo struct {
	Role      string    `json:"role"`
	CreatedBy string    `json:"created_by"`
	Created   time.Time `json:"created,omitzero"`
	Expires   time.Time `json:"expires,omitzero"`
	Used      bool      `json:"used,omitempty"`
}

// Invite looks up an invitation by its secret, used or not: the sign-in page shows it before anyone signs in.
func (t *Team) Invite(secret string) (InviteInfo, error) {
	var out InviteInfo
	var created, expires, usedAt int64
	err := t.r.QueryRow(`SELECT role, created_by, created, expires, used_at FROM invites WHERE sum = ?`, Sum(secret)).
		Scan(&out.Role, &out.CreatedBy, &created, &expires, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return InviteInfo{}, ErrNotFound
	}
	if err != nil {
		return InviteInfo{}, err
	}
	out.Created, out.Expires = fromNanos(created), fromNanos(expires)
	out.Used = usedAt != 0
	return out, nil
}

// NewCredential makes a secret of kind for owner (a node token names its machine); ttl 0 never expires. The secret
// is shown once: only its hash is kept.
func (t *Team) NewCredential(kind, name, owner string, ttl time.Duration) (string, Credential, error) {
	secret := newSecret()
	c, err := t.ImportCredential(Credential{Kind: kind, Name: name, Owner: owner, Sum: Sum(secret)}, ttl)
	return secret, c, err
}

// ImportCredential keeps a credential whose hash is known (tokens.json of an older server).
func (t *Team) ImportCredential(c Credential, ttl time.Duration) (Credential, error) {
	if c.Kind != KindWeb && c.Kind != KindToken && c.Kind != KindNode {
		return Credential{}, fmt.Errorf("kind %q", c.Kind)
	}
	c.ID = newID("c_")
	if c.Created.IsZero() {
		c.Created = time.Now().UTC()
	}
	if ttl != 0 {
		c.Expires = time.Now().Add(ttl).UTC()
	}
	_, err := t.w.Exec(`INSERT INTO credentials (id, kind, name, owner, sum, node_id, host, created, expires) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Kind, c.Name, c.Owner, c.Sum, c.NodeID, c.Host, nanos(c.Created), nanos(c.Expires))
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return Credential{}, ErrExists
	}
	return c, err
}

const credCols = `id, kind, name, owner, sum, node_id, host, created, last_used, expires`

func scanCred(s scanner) (Credential, error) {
	var c Credential
	var created, used, expires int64
	err := s.Scan(&c.ID, &c.Kind, &c.Name, &c.Owner, &c.Sum, &c.NodeID, &c.Host, &created, &used, &expires)
	c.Created, c.LastUsed, c.Expires = fromNanos(created), fromNanos(used), fromNanos(expires)
	return c, err
}

// ActiveCredentials are those neither revoked nor expired, whose owners are not disabled.
func (t *Team) ActiveCredentials() ([]Credential, error) {
	rows, err := t.r.Query(`SELECT c.id, c.kind, c.name, c.owner, c.sum, c.node_id, c.host, c.created, c.last_used, c.expires
		FROM credentials c JOIN users u ON u.id = c.owner
		WHERE c.revoked = 0 AND (c.expires = 0 OR c.expires > ?) AND u.disabled = 0 ORDER BY c.created`, time.Now().UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		c, err := scanCred(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (t *Team) Revoke(id string) error {
	return affected(t.w.Exec(`UPDATE credentials SET revoked = 1 WHERE id = ? AND revoked = 0`, id))
}

// NodeCredential is the live node token of machine.
func (t *Team) NodeCredential(machine string) (Credential, error) {
	c, err := scanCred(t.r.QueryRow(`SELECT `+credCols+` FROM credentials WHERE kind = ? AND name = ? AND revoked = 0`, KindNode, machine))
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	return c, err
}

// Bind ties a node token to the first node that uses it; another node is refused until Rebind.
func (t *Team) Bind(id, nodeID, host string) error {
	return inTx(t.w, func(tx *sql.Tx) error {
		var bound string
		if err := tx.QueryRow(`SELECT node_id FROM credentials WHERE id = ?`, id).Scan(&bound); err != nil {
			return err
		}
		switch {
		case bound == nodeID:
			_, err := tx.Exec(`UPDATE credentials SET host = ? WHERE id = ?`, host, id)
			return err
		case bound != "":
			return ErrOtherMachine
		}
		_, err := tx.Exec(`UPDATE credentials SET node_id = ?, host = ? WHERE id = ?`, nodeID, host, id)
		return err
	})
}

// Rebind lets a node token move to another machine: the next node to connect is bound.
func (t *Team) Rebind(id string) error {
	return affected(t.w.Exec(`UPDATE credentials SET node_id = '', host = '' WHERE id = ?`, id))
}

// Touch records that a credential was used now.
func (t *Team) Touch(id string) error {
	_, err := t.w.Exec(`UPDATE credentials SET last_used = ? WHERE id = ?`, time.Now().UnixNano(), id)
	return err
}

// Audit appends to the security log; it holds ids and names, never a secret.
func (t *Team) Audit(e AuditEntry) error {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	_, err := t.w.Exec(`INSERT INTO audit (at, actor, kind, detail, ip) VALUES (?, ?, ?, ?, ?)`, e.At.UnixNano(), e.Actor, e.Kind, e.Detail, e.IP)
	return err
}

// AuditLog is the newest n entries, newest first.
func (t *Team) AuditLog(n int) ([]AuditEntry, error) {
	rows, err := t.r.Query(`SELECT at, actor, kind, detail, ip FROM audit ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var at int64
		if err := rows.Scan(&at, &e.Actor, &e.Kind, &e.Detail, &e.IP); err != nil {
			return nil, err
		}
		e.At = fromNanos(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetWebhook is where user's notices go ("" for nowhere).
func (t *Team) SetWebhook(user, url string) error {
	return affected(t.w.Exec(`UPDATE users SET webhook = ? WHERE id = ?`, url, user))
}

// Webhook is where user's notices go, "" for nowhere.
func (t *Team) Webhook(user string) (string, error) {
	var url string
	err := t.r.QueryRow(`SELECT webhook FROM users WHERE id = ? AND disabled = 0`, user).Scan(&url)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return url, err
}

// Claim takes the delivery of event seq to user: false when it was taken before.
func (t *Team) Claim(seq int64, user, event string) (bool, error) {
	res, err := t.w.Exec(`INSERT OR IGNORE INTO deliveries (seq, user_id, event, at) VALUES (?, ?, ?, ?)`, seq, user, event, time.Now().UnixNano())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Delivered records how a claimed delivery went.
func (t *Team) Delivered(seq int64, user, event, status string) error {
	_, err := t.w.Exec(`UPDATE deliveries SET status = ? WHERE seq = ? AND user_id = ? AND event = ?`, status, seq, user, event)
	return err
}

// RevokeAll ends every credential of user's, their machines' included.
func (t *Team) RevokeAll(user string) error {
	_, err := t.w.Exec(`UPDATE credentials SET revoked = 1 WHERE owner = ?`, user)
	return err
}
