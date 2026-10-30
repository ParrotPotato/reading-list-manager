package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ErrBadLogin is returned for both an unknown username and a wrong password,
// so a caller cannot use the difference to discover which accounts exist.
var ErrBadLogin = errors.New("that email and password do not match")

// invalidInput marks an error caused by what someone typed, rather than by a
// fault. Handlers show these on the form instead of turning them into a 500.
type invalidInput struct{ msg string }

func (e invalidInput) Error() string { return e.msg }

func badInput(msg string) error { return invalidInput{msg} }

// ErrWrongPassword is returned when a password change fails its own check.
var ErrWrongPassword = invalidInput{"your current password is not right"}

// ErrUserExists is returned when an email already has an account.
var ErrUserExists = invalidInput{"that email already has an account"}

// sessionLife is how long a login lasts before it has to be repeated.
const sessionLife = 30 * 24 * time.Hour

// User is an account. The password hash never leaves this file.
type User struct {
	ID        int64
	Email     string
	CreatedAt time.Time
}

// CreateUser adds an account. The first account to exist also adopts every
// item left unowned by the upgrade from a single-user catalogue.
func (s *Store) CreateUser(email, password string) (User, bool, error) {
	email = normalizeEmail(email)
	if err := checkEmail(email); err != nil {
		return User{}, false, err
	}
	if err := checkPassword(password); err != nil {
		return User{}, false, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, false, err
	}

	var (
		user    User
		adopted bool
	)
	err = s.tx(func(tx *sql.Tx) error {
		var taken int
		switch err := tx.QueryRow(`SELECT 1 FROM users WHERE email = ? COLLATE NOCASE`,
			email).Scan(&taken); {
		case err == nil:
			return ErrUserExists
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}

		now := time.Now().Unix()
		res, err := tx.Exec(`INSERT INTO users (email, password_hash, created_at)
			VALUES (?, ?, ?)`, email, string(hash), now)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		user = User{ID: id, Email: email, CreatedAt: time.Unix(now, 0)}

		// Items predating accounts have user_id 0. Whoever registers first gets
		// them, which is what makes upgrading a personal catalogue seamless.
		var users int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
			return err
		}
		if users != 1 {
			return nil
		}
		res, err = tx.Exec(`UPDATE items SET user_id = ? WHERE user_id = 0`, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		adopted = n > 0
		return nil
	})
	return user, adopted, err
}

// SetPassword replaces an account's password, found by email. This is the
// command-line reset, which does not need the old password.
func (s *Store) SetPassword(email, password string) error {
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	return s.touch(`UPDATE users SET password_hash = ? WHERE email = ? COLLATE NOCASE`,
		hash, normalizeEmail(email))
}

// ChangePassword is the signed-in version: it proves the old password first.
func (s *Store) ChangePassword(userID int64, current, next string) error {
	var hash string
	if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return ErrWrongPassword
	}
	if current == next {
		return badInput("that is already your password")
	}
	fresh, err := hashPassword(next)
	if err != nil {
		return err
	}
	return s.touch(`UPDATE users SET password_hash = ? WHERE id = ?`, fresh, userID)
}

// hashPassword checks a new password and hashes it.
func hashPassword(password string) (string, error) {
	if err := checkPassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

// Users lists every account, oldest first.
func (s *Store) Users() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, email, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var (
			u         User
			createdAt int64
		)
		if err := rows.Scan(&u.ID, &u.Email, &createdAt); err != nil {
			return nil, err
		}
		u.CreatedAt = time.Unix(createdAt, 0)
		users = append(users, u)
	}
	return users, rows.Err()
}

// Authenticate checks a username and password, returning the account on match.
func (s *Store) Authenticate(email, password string) (User, error) {
	var (
		user      User
		hash      string
		createdAt int64
	)
	err := s.db.QueryRow(`SELECT id, email, password_hash, created_at
		FROM users WHERE email = ? COLLATE NOCASE`,
		normalizeEmail(email)).Scan(&user.ID, &user.Email, &hash, &createdAt)

	if errors.Is(err, sql.ErrNoRows) {
		// Hash anyway so that a missing account takes the same time as a wrong
		// password, leaving no timing signal for probing usernames.
		bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
		return User{}, ErrBadLogin
	}
	if err != nil {
		return User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return User{}, ErrBadLogin
	}
	user.CreatedAt = time.Unix(createdAt, 0)
	return user, nil
}

// dummyHash is a real bcrypt hash of an unguessable value, compared against
// when no account matched purely to keep the timing even.
const dummyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

// StartSession issues a new session and returns the token for the cookie. Only
// a hash of the token is stored, so the database never holds a usable key.
func (s *Store) StartSession(userID int64) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(sessionLife)

	_, err := s.db.Exec(`INSERT INTO sessions (token_hash, user_id, created_at, expires_at)
		VALUES (?, ?, ?, ?)`,
		hashToken(token), userID, time.Now().Unix(), expires.Unix())
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// SessionUser resolves a cookie token to its account, or reports no session.
func (s *Store) SessionUser(token string) (User, bool) {
	if token == "" {
		return User{}, false
	}
	var (
		user      User
		createdAt int64
	)
	err := s.db.QueryRow(`SELECT u.id, u.email, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`,
		hashToken(token), time.Now().Unix()).Scan(&user.ID, &user.Email, &createdAt)
	if err != nil {
		return User{}, false
	}
	user.CreatedAt = time.Unix(createdAt, 0)
	return user, true
}

// EndSession logs one session out and clears any that have expired.
func (s *Store) EndSession(token string) error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(token)); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
	return err
}

// EndUserSessions logs an account out everywhere, used after a reset.
func (s *Store) EndUserSessions(email string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id IN
		(SELECT id FROM users WHERE email = ? COLLATE NOCASE)`, normalizeEmail(email))
	return err
}

// EndOtherSessions signs an account out everywhere except the session doing
// the asking, so changing your password boots anyone else off.
func (s *Store) EndOtherSessions(userID int64, keep string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`,
		userID, hashToken(keep))
	return err
}

// hashToken is what actually lives in the sessions table.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// normalizeEmail trims and lower-cases, so the address you log in with does
// not depend on how you happened to capitalise it.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func checkEmail(email string) error {
	if email == "" {
		return badInput("email cannot be empty")
	}
	if len(email) > 254 {
		return badInput("email is too long")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || !strings.Contains(email, ".") {
		return badInput("that does not look like an email address")
	}
	return nil
}

func checkPassword(password string) error {
	if len(password) < 10 {
		return badInput("password must be at least 10 characters")
	}
	// bcrypt silently ignores anything past 72 bytes, so refuse rather than
	// accept a password whose tail does nothing.
	if len(password) > 72 {
		return badInput("password must be at most 72 characters")
	}
	return nil
}
