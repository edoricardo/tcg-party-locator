package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidAccount     = errors.New("invalid account")
	ErrDuplicateEmail     = errors.New("email already registered")
	ErrInvalidCredentials = errors.New("invalid email or password")
)

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type Session struct {
	User      User
	CSRFToken string
}

func (s *EventStore) Register(email, password string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	parsedEmail, err := mail.ParseAddress(email)
	if err != nil || parsedEmail.Address != email || utf8.RuneCountInString(password) < 12 || len(password) > 72 {
		return User{}, ErrInvalidAccount
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash account password: %w", err)
	}
	id, err := newRandomToken(16)
	if err != nil {
		return User{}, fmt.Errorf("generate account id: %w", err)
	}
	user := User{ID: "usr_" + id, Email: email}

	s.mu.Lock()
	defer s.mu.Unlock()

	var existingID string
	err = s.db.QueryRow("SELECT id FROM users WHERE email = ?", email).Scan(&existingID)
	if err == nil {
		return User{}, ErrDuplicateEmail
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("check account email: %w", err)
	}

	_, err = s.db.Exec(
		"INSERT INTO users (id, email, password_hash, created_at) VALUES (?, ?, ?, ?)",
		user.ID,
		user.Email,
		string(passwordHash),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return User{}, fmt.Errorf("create account: %w", err)
	}
	return user, nil
}

func (s *EventStore) Authenticate(email, password string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	s.mu.RLock()
	defer s.mu.RUnlock()

	var user User
	var passwordHash string
	err := s.db.QueryRow(
		"SELECT id, email, password_hash FROM users WHERE email = ?",
		email,
	).Scan(&user.ID, &user.Email, &passwordHash)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return User{}, fmt.Errorf("load account: %w", err)
		}
		return User{}, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

func (s *EventStore) CreateSession(userID string, expiresAt time.Time) (sessionToken, csrfToken string, err error) {
	sessionToken, err = newRandomToken(32)
	if err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	csrfToken, err = newRandomToken(32)
	if err != nil {
		return "", "", fmt.Errorf("generate CSRF token: %w", err)
	}

	tokenHash := hashSessionToken(sessionToken)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.Exec(
		"INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at) VALUES (?, ?, ?, ?)",
		tokenHash,
		userID,
		csrfToken,
		expiresAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return "", "", fmt.Errorf("create session: %w", err)
	}
	return sessionToken, csrfToken, nil
}

func (s *EventStore) ResolveSession(sessionToken string) (Session, bool) {
	if sessionToken == "" {
		return Session{}, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var session Session
	var expiresAt string
	err := s.db.QueryRow(`
		SELECT users.id, users.email, sessions.csrf_token, sessions.expires_at
		FROM sessions
		JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ?
	`, hashSessionToken(sessionToken)).Scan(
		&session.User.ID,
		&session.User.Email,
		&session.CSRFToken,
		&expiresAt,
	)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("resolve session: %v", err)
		}
		return Session{}, false
	}

	expiry, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		log.Printf("resolve session expiry: %v", err)
		return Session{}, false
	}
	if !expiry.After(time.Now()) {
		return Session{}, false
	}
	return session, true
}

func (s *EventStore) DeleteSession(sessionToken string) error {
	if sessionToken == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM sessions WHERE token_hash = ?", hashSessionToken(sessionToken))
	return err
}

func newRandomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func hashSessionToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func (h *Handler) session(r *http.Request) (Session, bool) {
	cookie, err := r.Cookie("tcg_session")
	if err != nil {
		return Session{}, false
	}
	return h.store.ResolveSession(cookie.Value)
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.renderPage(w, r, "register.html", h.pageData(r, "Create account"))
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid registration form", http.StatusBadRequest)
		return
	}
	user, err := h.store.Register(r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		switch {
		case errors.Is(err, ErrDuplicateEmail):
			page := h.pageData(r, "Create account")
			page.Error = "An account with that email already exists."
			h.renderPageStatus(w, r, "register.html", page, http.StatusConflict)
		case errors.Is(err, ErrInvalidAccount):
			page := h.pageData(r, "Create account")
			page.Error = "Enter a valid email and a password of at least 12 characters."
			h.renderPageStatus(w, r, "register.html", page, http.StatusBadRequest)
		default:
			log.Printf("register account: %v", err)
			page := h.pageData(r, "Create account")
			page.Error = "Could not create account."
			h.renderPageStatus(w, r, "register.html", page, http.StatusInternalServerError)
		}
		return
	}
	h.createSession(w, r, user)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.renderPage(w, r, "login.html", h.pageData(r, "Sign in"))
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid login form", http.StatusBadRequest)
		return
	}
	user, err := h.store.Authenticate(r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			page := h.pageData(r, "Sign in")
			page.Error = ErrInvalidCredentials.Error()
			h.renderPageStatus(w, r, "login.html", page, http.StatusUnauthorized)
			return
		}
		log.Printf("authenticate account: %v", err)
		page := h.pageData(r, "Sign in")
		page.Error = "Could not sign in."
		h.renderPageStatus(w, r, "login.html", page, http.StatusInternalServerError)
		return
	}
	h.createSession(w, r, user)
}

func (h *Handler) createSession(w http.ResponseWriter, r *http.Request, user User) {
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	sessionToken, _, err := h.store.CreateSession(user.ID, expiresAt)
	if err != nil {
		log.Printf("create account session: %v", err)
		http.Error(w, "could not create session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "tcg_session",
		Value:    sessionToken,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, ok := h.session(r)
	if !ok {
		h.clearSessionCookie(w, r)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !validCSRFToken(r, session.CSRFToken) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	cookie, _ := r.Cookie("tcg_session")
	if err := h.store.DeleteSession(cookie.Value); err != nil {
		log.Printf("delete account session: %v", err)
		http.Error(w, "could not sign out", http.StatusInternalServerError)
		return
	}
	h.clearSessionCookie(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) currentAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	session, ok := h.session(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": session.User})
}

func (h *Handler) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "tcg_session",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

func validCSRFToken(r *http.Request, expected string) bool {
	candidate := r.Header.Get("X-CSRF-Token")
	if candidate == "" {
		if err := r.ParseForm(); err != nil {
			return false
		}
		candidate = r.FormValue("_csrf")
	}
	return len(candidate) == len(expected) && subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) == 1
}
