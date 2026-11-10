package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
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
	earlier := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Hour)
	later := earlier.Add(time.Hour).In(time.FixedZone("PDT", -7*60*60))

	events := []Event{
		{ID: "evt-9001", Name: "Earlier in UTC", Game: "Commander", City: "Seattle", Venue: "Northside", Date: earlier.Format(time.RFC3339), Format: "Casual", Players: 8, Spots: 12, Host: "A"},
		{ID: "evt-9002", Name: "Later by local offset", Game: "Commander", City: "Seattle", Venue: "Southside", Date: later.Format(time.RFC3339), Format: "Casual", Players: 8, Spots: 12, Host: "B"},
	}
	for _, event := range events {
		if _, err := store.Add("", event); err != nil {
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
	cookie := registerTestAccount(t, handler, "host@example.com", "correct horse battery staple")
	csrfToken := csrfTokenForTest(t, store, "host@example.com")

	event := newTestEvent("Battle for the Bay", "Tacoma")
	event.Game = "Yu-Gi-Oh!"
	event.Format = "Double Elimination"
	event.Host = "Ruth"
	bodyBytes, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to encode event: %v", err)
	}
	body := string(bodyBytes)
	req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
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

	handler.ServeHTTP(res, req)
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

	_, err := store.Add("", Event{
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
	cookie := registerTestAccount(t, handler, "host@example.com", "correct horse battery staple")
	csrfToken := csrfTokenForTest(t, store, "host@example.com")

	body := `{"name":"","game":"Commander","city":"Seattle","venue":"The Dragon's Den","date":"2026-11-08T18:30:00Z","format":"Casual Draft","players":8,"spots":12,"host":"Avery"}`
	req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
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
	cookie := registerTestAccount(t, handler, "host@example.com", "correct horse battery staple")
	csrfToken := csrfTokenForTest(t, store, "host@example.com")

	body := `{"id":"evt-1001","name":"Duplicate Event","game":"Commander","city":"Seattle","venue":"The Dragon's Den","date":"2026-11-08T18:30:00Z","format":"Casual Draft","players":8,"spots":12,"host":"Avery"}`
	req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
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
	eventColumns := make(map[string]bool)
	columns, err := store.db.Query("PRAGMA table_info(events)")
	if err != nil {
		t.Fatalf("failed to inspect migrated event columns: %v", err)
	}
	for columns.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := columns.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = columns.Close()
			t.Fatalf("failed to scan migrated event columns: %v", err)
		}
		eventColumns[name] = true
		if name == "status" && strings.Trim(defaultValue.String, "'") != "published" {
			t.Fatalf("expected old event status to default to published, got %q", defaultValue.String)
		}
	}
	if err := columns.Close(); err != nil {
		t.Fatalf("failed to close migrated event columns: %v", err)
	}
	if !eventColumns["owner_id"] || !eventColumns["status"] {
		t.Fatalf("expected owner_id and status columns after migration, got %v", eventColumns)
	}
	event, ok := store.Get("legacy-event")
	if !ok || event.Name != "Legacy Commander" {
		t.Fatalf("expected legacy event to remain readable, got %+v (found=%t)", event, ok)
	}
	var migratedStatus string
	if err := store.db.QueryRow("SELECT status FROM events WHERE id = ?", "legacy-event").Scan(&migratedStatus); err != nil {
		t.Fatalf("failed to read migrated event status: %v", err)
	}
	if migratedStatus != "published" {
		t.Fatalf("expected old event status to default to published, got %q", migratedStatus)
	}
}

func TestCreateEventRequiresSession(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	body, err := json.Marshal(newTestEvent("Unauthorized event", "Tacoma"))
	if err != nil {
		t.Fatalf("failed to encode event: %v", err)
	}
	res := requestJSON(handler, http.MethodPost, "/events", body, nil, "")
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected event creation without a session to return 401, got %d: %s", res.Code, res.Body.String())
	}
}

func TestCreateEventAssignsOwnerAndIgnoresClientPlayerCount(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	cookie := registerTestAccount(t, handler, "host@example.com", "correct horse battery staple")
	csrfToken := csrfTokenForTest(t, store, "host@example.com")
	event := newTestEvent("Owned event", "Tacoma")
	event.Players = 7
	event.Spots = 4
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to encode event: %v", err)
	}

	res := requestJSON(handler, http.MethodPost, "/events", body, cookie, csrfToken)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected event creation status 201, got %d: %s", res.Code, res.Body.String())
	}
	var response struct {
		Event Event `json:"event"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode event response: %v", err)
	}
	if response.Event.Players != 0 || response.Event.Spots != 4 {
		t.Fatalf("expected new event to start with zero players and four open spots, got %+v", response.Event)
	}
	var ownerID string
	if err := store.db.QueryRow("SELECT owner_id FROM events WHERE id = ?", response.Event.ID).Scan(&ownerID); err != nil {
		t.Fatalf("failed to read event owner: %v", err)
	}
	var userID string
	if err := store.db.QueryRow("SELECT id FROM users WHERE email = ?", "host@example.com").Scan(&userID); err != nil {
		t.Fatalf("failed to read host account: %v", err)
	}
	if ownerID != userID {
		t.Fatalf("expected event owner %q, got %q", userID, ownerID)
	}
}

func TestEventManagementRejectsNonOwner(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	event, ownerCookie, _ := createOwnedTestEvent(t, handler, store, "owner@example.com")
	otherCookie := registerTestAccount(t, handler, "other@example.com", "correct horse battery staple")
	otherCSRF := csrfTokenForTest(t, store, "other@example.com")
	updated := event
	updated.Name = "Unauthorized edit"
	body, err := json.Marshal(updated)
	if err != nil {
		t.Fatalf("failed to encode event update: %v", err)
	}

	updateRes := requestJSON(handler, http.MethodPut, "/events/"+event.ID, body, otherCookie, otherCSRF)
	if updateRes.Code != http.StatusForbidden {
		t.Errorf("expected non-owner edit to return 403, got %d: %s", updateRes.Code, updateRes.Body.String())
	}
	cancelRes := requestJSON(handler, http.MethodDelete, "/events/"+event.ID, nil, otherCookie, otherCSRF)
	if cancelRes.Code != http.StatusForbidden {
		t.Errorf("expected non-owner cancellation to return 403, got %d: %s", cancelRes.Code, cancelRes.Body.String())
	}
	detailReq := httptest.NewRequest(http.MethodGet, "/events/"+event.ID, nil)
	detailReq.AddCookie(ownerCookie)
	detailRes := httptest.NewRecorder()
	handler.ServeHTTP(detailRes, detailReq)
	if !strings.Contains(detailRes.Body.String(), "Owned test event") {
		t.Fatalf("expected unauthorized mutations to leave event unchanged, got %s", detailRes.Body.String())
	}
}

func TestEventOwnerCanUpdateAndCancel(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	event, cookie, csrfToken := createOwnedTestEvent(t, handler, store, "owner@example.com")
	updated := event
	updated.Name = "Updated event"
	body, err := json.Marshal(updated)
	if err != nil {
		t.Fatalf("failed to encode event update: %v", err)
	}

	updateRes := requestJSON(handler, http.MethodPut, "/events/"+event.ID, body, cookie, csrfToken)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("expected owner edit to return 200, got %d: %s", updateRes.Code, updateRes.Body.String())
	}
	cancelRes := requestJSON(handler, http.MethodDelete, "/events/"+event.ID, nil, cookie, csrfToken)
	if cancelRes.Code != http.StatusOK {
		t.Fatalf("expected owner cancellation to return 200, got %d: %s", cancelRes.Code, cancelRes.Body.String())
	}
	stored, ok := store.Get(event.ID)
	if !ok || stored.Name != "Updated event" {
		t.Fatalf("expected updated cancelled event to remain readable, got %+v (found=%t)", stored, ok)
	}
	var status string
	if err := store.db.QueryRow("SELECT status FROM events WHERE id = ?", event.ID).Scan(&status); err != nil {
		t.Fatalf("failed to read cancelled event status: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("expected event status to be cancelled, got %q", status)
	}
}

func TestListEventsExcludesCancelled(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	event, cookie, csrfToken := createOwnedTestEvent(t, handler, store, "host@example.com")

	res := requestJSON(handler, http.MethodDelete, "/events/"+event.ID, nil, cookie, csrfToken)
	if res.Code != http.StatusOK {
		t.Fatalf("failed to cancel event: %d: %s", res.Code, res.Body.String())
	}
	for _, listed := range store.List(event.City, "", "") {
		if listed.ID == event.ID {
			t.Fatalf("cancelled event appeared in discovery: %+v", listed)
		}
	}
}

func TestListEventsExcludesPast(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	cookie := registerTestAccount(t, handler, "host@example.com", "correct horse battery staple")
	csrfToken := csrfTokenForTest(t, store, "host@example.com")
	event := newTestEvent("Past event", "Tacoma")
	event.Date = time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to encode event: %v", err)
	}
	res := requestJSON(handler, http.MethodPost, "/events", body, cookie, csrfToken)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected past event to be storable, got %d: %s", res.Code, res.Body.String())
	}
	for _, listed := range store.List("Tacoma", "", "") {
		if listed.Name == "Past event" {
			t.Fatalf("past event appeared in discovery: %+v", listed)
		}
	}
}

func newTestEvent(name, city string) Event {
	return Event{
		Name:    name,
		Game:    "Commander",
		City:    city,
		Venue:   "Game Room",
		Date:    time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339),
		Format:  "Casual",
		Players: 0,
		Spots:   10,
		Host:    "Test host",
	}
}

func createOwnedTestEvent(t *testing.T, handler http.Handler, store *EventStore, email string) (Event, *http.Cookie, string) {
	t.Helper()
	cookie := registerTestAccount(t, handler, email, "correct horse battery staple")
	csrfToken := csrfTokenForTest(t, store, email)
	event := newTestEvent("Owned test event", "Tacoma")
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to encode test event: %v", err)
	}
	res := requestJSON(handler, http.MethodPost, "/events", body, cookie, csrfToken)
	if res.Code != http.StatusCreated {
		t.Fatalf("failed to create test event: %d: %s", res.Code, res.Body.String())
	}
	var response struct {
		Event Event `json:"event"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode test event: %v", err)
	}
	return response.Event, cookie, csrfToken
}

func requestJSON(handler http.Handler, method, path string, body []byte, cookie *http.Cookie, csrfToken string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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
