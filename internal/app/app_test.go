package app

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *EventStore {
	t.Helper()

	dir := t.TempDir()
	store, err := NewEventStoreForPath(filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatalf("failed to create test event store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Logf("failed to close test database: %v", err)
		}
	})
	return store
}

func TestListEvents(t *testing.T) {
	store := newTestStore(t)
	results := store.List("Seattle", "", "")
	if len(results) != 2 {
		t.Fatalf("expected 2 Seattle events, got %d", len(results))
	}

	filtered := store.List("", "Commander", "")
	if len(filtered) != 1 || filtered[0].Name != "Commander Night" {
		t.Fatalf("expected Commander Night event, got %+v", filtered)
	}
}

func TestListEventsSortsByActualTime(t *testing.T) {
	store := newTestStore(t)

	events := []Event{
		{ID: "evt-9001", Name: "Earlier in UTC", Game: "Commander", City: "Seattle", Venue: "Northside", Date: "2026-11-08T11:00:00Z", Format: "Casual", Players: 8, Spots: 12, Host: "A"},
		{ID: "evt-9002", Name: "Later by local offset", Game: "Commander", City: "Seattle", Venue: "Southside", Date: "2026-11-08T05:00:00-07:00", Format: "Casual", Players: 8, Spots: 12, Host: "B"},
	}
	for _, event := range events {
		if _, err := store.Add(event); err != nil {
			t.Fatalf("failed to add test event: %v", err)
		}
	}

	results := store.List("Seattle", "Commander", "")
	if len(results) < 2 {
		t.Fatalf("expected at least 2 Seattle Commander events, got %d", len(results))
	}
	if results[0].Name != "Earlier in UTC" {
		t.Fatalf("expected earliest time first, got %+v", results[:2])
	}
}

func TestCreateEvent(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)

	body := `{"name":"Battle for the Bay","game":"Yu-Gi-Oh!","city":"Tacoma","venue":"Game Room","date":"2026-11-15T18:00:00Z","format":"Double Elimination","players":8,"spots":12,"host":"Ruth"}`
	req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(body))
	res := httptest.NewRecorder()

	handler.events(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", res.Code, res.Body.String())
	}

	results := store.List("Tacoma", "", "")
	if len(results) != 1 {
		t.Fatalf("expected 1 Tacoma event after creation, got %d", len(results))
	}
}

func TestGetEventByID(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/events/evt-1001", nil)
	res := httptest.NewRecorder()

	handler.eventDetail(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", res.Code, res.Body.String())
	}
	d := res.Body.String()
	if !strings.Contains(d, "Commander Night") {
		t.Fatalf("expected event details in response, got %s", d)
	}
}

func TestValidateEventRejectsInvalidPayload(t *testing.T) {
	event := Event{
		Name:    "",
		Game:    "Commander",
		City:    "Seattle",
		Venue:   "The Dragon's Den",
		Date:    "2026-11-08T18:30:00Z",
		Format:  "Casual Draft",
		Players: 8,
		Spots:   12,
		Host:    "Avery",
	}

	if err := event.Validate(); err == nil {
		t.Fatal("expected invalid event to fail validation")
	}
}

func TestStoreAddRejectsInvalidEvent(t *testing.T) {
	store := newTestStore(t)

	_, err := store.Add(Event{
		Name:    "",
		Game:    "Commander",
		City:    "Seattle",
		Venue:   "The Dragon's Den",
		Date:    "2026-11-08T18:30:00Z",
		Format:  "Casual Draft",
		Players: 8,
		Spots:   12,
		Host:    "Avery",
	})
	if err == nil {
		t.Fatal("expected store.Add to reject invalid event")
	}
}

func TestNormalizeWhitespaceIDGeneratesFreshID(t *testing.T) {
	evt := normalizeEvent(Event{ID: "   ", Name: "Commander Night", Game: "Commander", City: "Seattle", Venue: "The Dragon's Den", Date: "2026-11-08T18:30:00Z", Format: "Casual Draft", Players: 8, Spots: 12, Host: "Avery"})
	if evt.ID == "" || strings.TrimSpace(evt.ID) == "" {
		t.Fatalf("expected generated non-empty event id, got %q", evt.ID)
	}
	if evt.ID == "   " {
		t.Fatalf("expected whitespace-only id to be replaced, got %q", evt.ID)
	}
}

func TestCreateEventRejectsBadPayload(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)

	body := `{"name":"","game":"Commander","city":"Seattle","venue":"The Dragon's Den","date":"2026-11-08T18:30:00Z","format":"Casual Draft","players":8,"spots":12,"host":"Avery"}`
	req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(body))
	res := httptest.NewRecorder()

	handler.events(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "name is required") {
		t.Fatalf("expected validation error, got %s", res.Body.String())
	}
}

func TestCreateEventHandlesDatabaseFailure(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)

	body := `{"id":"evt-1001","name":"Duplicate Event","game":"Commander","city":"Seattle","venue":"The Dragon's Den","date":"2026-11-08T18:30:00Z","format":"Casual Draft","players":8,"spots":12,"host":"Avery"}`
	req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(body))
	res := httptest.NewRecorder()

	handler.events(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "could not create event") {
		t.Fatalf("expected storage error response, got %s", res.Body.String())
	}
}

func TestRegisterCreatesAccountAndSessionCookie(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	form := url.Values{"email": {"player@example.com"}, "password": {"correct horse battery staple"}}
	req := httptest.NewRequest(http.MethodPost, "https://tcg.example/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("expected registration redirect, got %d: %s", res.Code, res.Body.String())
	}

	var cookie *http.Cookie
	for _, candidate := range res.Result().Cookies() {
		if candidate.Name == "tcg_session" {
			cookie = candidate
			break
		}
	}
	if cookie == nil {
		t.Fatal("expected a session cookie after registration")
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || !cookie.Secure {
		t.Fatalf("session cookie missing security attributes: %+v", cookie)
	}
	if cookie.Expires.Before(time.Now().Add(6*24*time.Hour)) || cookie.Expires.After(time.Now().Add(8*24*time.Hour)) {
		t.Fatalf("expected session cookie to expire in seven days, got %v", cookie.Expires)
	}

	var passwordHash, storedTokenHash string
	if err := store.db.QueryRow("SELECT password_hash FROM users WHERE email = ?", "player@example.com").Scan(&passwordHash); err != nil {
		t.Fatalf("failed to read stored password hash: %v", err)
	}
	if passwordHash == "correct horse battery staple" || !strings.HasPrefix(passwordHash, "$2a$") {
		t.Fatalf("expected a bcrypt password hash, got %q", passwordHash)
	}
	if err := store.db.QueryRow("SELECT token_hash FROM sessions LIMIT 1").Scan(&storedTokenHash); err != nil {
		t.Fatalf("failed to read stored session token hash: %v", err)
	}
	if storedTokenHash == cookie.Value {
		t.Fatal("expected the raw session token not to be stored in SQLite")
	}
}

func TestRegisterRejectsDuplicateEmailCaseInsensitive(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	registerTestAccount(t, handler, "player@example.com", "correct horse battery staple")

	res := submitForm(handler, http.MethodPost, "/register", url.Values{
		"email":    {"PLAYER@EXAMPLE.COM"},
		"password": {"another correct password"},
	}, nil, "")
	if res.Code != http.StatusConflict {
		t.Fatalf("expected duplicate email to be rejected with 409, got %d: %s", res.Code, res.Body.String())
	}
}

func TestRegisterRejectsShortPassword(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	res := submitForm(handler, http.MethodPost, "/register", url.Values{
		"email":    {"player@example.com"},
		"password": {"short"},
	}, nil, "")
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected short password to be rejected with 400, got %d: %s", res.Code, res.Body.String())
	}
}

func TestLoginRejectsInvalidCredentials(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	registerTestAccount(t, handler, "player@example.com", "correct horse battery staple")

	wrongPassword := submitForm(handler, http.MethodPost, "/login", url.Values{
		"email":    {"player@example.com"},
		"password": {"incorrect password"},
	}, nil, "")
	unknownEmail := submitForm(handler, http.MethodPost, "/login", url.Values{
		"email":    {"missing@example.com"},
		"password": {"incorrect password"},
	}, nil, "")
	if wrongPassword.Code != http.StatusUnauthorized || unknownEmail.Code != http.StatusUnauthorized {
		t.Fatalf("expected invalid login to return 401, got %d and %d", wrongPassword.Code, unknownEmail.Code)
	}
	if wrongPassword.Body.String() != unknownEmail.Body.String() {
		t.Fatalf("expected generic invalid-login responses, got %q and %q", wrongPassword.Body.String(), unknownEmail.Body.String())
	}
}

func TestLoginSetsSessionCookie(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	registerTestAccount(t, handler, "player@example.com", "correct horse battery staple")

	res := submitForm(handler, http.MethodPost, "/login", url.Values{
		"email":    {"player@example.com"},
		"password": {"correct horse battery staple"},
	}, nil, "")
	if res.Code != http.StatusSeeOther {
		t.Fatalf("expected login redirect, got %d: %s", res.Code, res.Body.String())
	}
	if cookie := sessionCookie(res); cookie == nil || cookie.Value == "" {
		t.Fatal("expected login to set a session cookie")
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	cookie := registerTestAccount(t, handler, "player@example.com", "correct horse battery staple")
	csrfToken := csrfTokenForTest(t, store, "player@example.com")

	res := submitForm(handler, http.MethodPost, "/logout", url.Values{}, cookie, csrfToken)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("expected logout redirect, got %d: %s", res.Code, res.Body.String())
	}
	accountReq := httptest.NewRequest(http.MethodGet, "/api/account", nil)
	accountReq.AddCookie(cookie)
	accountRes := httptest.NewRecorder()
	handler.ServeHTTP(accountRes, accountReq)
	if accountRes.Code != http.StatusUnauthorized {
		t.Fatalf("expected invalidated session to return 401, got %d: %s", accountRes.Code, accountRes.Body.String())
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	cookie := registerTestAccount(t, handler, "player@example.com", "correct horse battery staple")

	expiredAt := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if _, err := store.db.Exec(`
		UPDATE sessions SET expires_at = ?
		WHERE user_id = (SELECT id FROM users WHERE email = ?)
	`, expiredAt, "player@example.com"); err != nil {
		t.Fatalf("failed to expire session: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/account", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected expired session to return 401, got %d: %s", res.Code, res.Body.String())
	}
}

func TestExistingDatabaseSchemaMigration(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "events.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("failed to open legacy database: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE events (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			game TEXT NOT NULL,
			city TEXT NOT NULL,
			venue TEXT NOT NULL,
			date TEXT NOT NULL,
			format TEXT NOT NULL,
			players INTEGER NOT NULL,
			spots INTEGER NOT NULL,
			host TEXT NOT NULL,
			address TEXT,
			notes TEXT
		);
		INSERT INTO events
			(id, name, game, city, venue, date, format, players, spots, host, address, notes)
		VALUES
			('legacy-event', 'Legacy Commander', 'Commander', 'Seattle', 'The Dragon''s Den',
			 '2030-01-02T18:00:00Z', 'Casual', 3, 5, 'Avery', '', '');
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("failed to create legacy schema and event: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close legacy database: %v", err)
	}

	store, err := NewEventStoreForPath(databasePath)
	if err != nil {
		t.Fatalf("failed to migrate legacy database: %v", err)
	}
	defer store.Close()

	tables := make(map[string]bool)
	rows, err := store.db.Query("SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		t.Fatalf("failed to inspect migrated schema: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			t.Fatalf("failed to scan migrated schema: %v", err)
		}
		tables[name] = true
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("failed to close schema rows: %v", err)
	}
	if !tables["users"] || !tables["sessions"] {
		t.Fatalf("expected users and sessions tables after migration, got %v", tables)
	}
	event, ok := store.Get("legacy-event")
	if !ok || event.Name != "Legacy Commander" {
		t.Fatalf("expected legacy event to remain readable, got %+v (found=%t)", event, ok)
	}
}

func registerTestAccount(t *testing.T, handler http.Handler, email, password string) *http.Cookie {
	t.Helper()
	res := submitForm(handler, http.MethodPost, "/register", url.Values{
		"email":    {email},
		"password": {password},
	}, nil, "")
	if res.Code != http.StatusSeeOther {
		t.Fatalf("failed to register test account: %d: %s", res.Code, res.Body.String())
	}
	cookie := sessionCookie(res)
	if cookie == nil {
		t.Fatal("registration did not set a session cookie")
	}
	return cookie
}

func submitForm(handler http.Handler, method, path string, values url.Values, cookie *http.Cookie, csrfToken string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func sessionCookie(res *httptest.ResponseRecorder) *http.Cookie {
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == "tcg_session" {
			return cookie
		}
	}
	return nil
}

func csrfTokenForTest(t *testing.T, store *EventStore, email string) string {
	t.Helper()
	var token string
	err := store.db.QueryRow(`
		SELECT csrf_token FROM sessions
		WHERE user_id = (SELECT id FROM users WHERE email = ?)
	`, email).Scan(&token)
	if err != nil {
		t.Fatalf("failed to read test CSRF token: %v", err)
	}
	return token
}
