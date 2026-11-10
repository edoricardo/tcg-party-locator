package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Event represents a public TCG gathering or event.
type Event struct {
	ID      string `json:"id"`
	OwnerID string `json:"-"`
	Status  string `json:"status"`
	Name    string `json:"name"`
	Game    string `json:"game"`
	City    string `json:"city"`
	Venue   string `json:"venue"`
	Date    string `json:"date"`
	Format  string `json:"format"`
	Players int    `json:"players"`
	Spots   int    `json:"spots"`
	Host    string `json:"host"`
	Address string `json:"address,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

func (e Event) Validate() error {
	if strings.TrimSpace(e.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidEvent)
	}
	if strings.TrimSpace(e.Game) == "" {
		return fmt.Errorf("%w: game is required", ErrInvalidEvent)
	}
	if strings.TrimSpace(e.City) == "" {
		return fmt.Errorf("%w: city is required", ErrInvalidEvent)
	}
	if strings.TrimSpace(e.Venue) == "" {
		return fmt.Errorf("%w: venue is required", ErrInvalidEvent)
	}
	if strings.TrimSpace(e.Date) == "" {
		return fmt.Errorf("%w: date is required", ErrInvalidEvent)
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(e.Date)); err != nil {
		return fmt.Errorf("%w: date must be a valid RFC3339 timestamp", ErrInvalidEvent)
	}
	if strings.TrimSpace(e.Format) == "" {
		return fmt.Errorf("%w: format is required", ErrInvalidEvent)
	}
	if strings.TrimSpace(e.Host) == "" {
		return fmt.Errorf("%w: host is required", ErrInvalidEvent)
	}
	if e.Players < 0 || e.Spots < 0 {
		return fmt.Errorf("%w: players and spots must be non-negative", ErrInvalidEvent)
	}
	return nil
}

func normalizeEvent(evt Event) Event {
	if strings.TrimSpace(evt.ID) == "" {
		evt.ID = fmt.Sprintf("evt-%d", time.Now().UnixNano())
	} else {
		evt.ID = strings.TrimSpace(evt.ID)
	}

	cleaned := Event{
		ID:      evt.ID,
		OwnerID: evt.OwnerID,
		Status:  strings.TrimSpace(evt.Status),
		Name:    strings.TrimSpace(evt.Name),
		Game:    strings.TrimSpace(evt.Game),
		City:    strings.TrimSpace(evt.City),
		Venue:   strings.TrimSpace(evt.Venue),
		Date:    strings.TrimSpace(evt.Date),
		Format:  strings.TrimSpace(evt.Format),
		Players: evt.Players,
		Spots:   evt.Spots,
		Host:    strings.TrimSpace(evt.Host),
		Address: strings.TrimSpace(evt.Address),
		Notes:   strings.TrimSpace(evt.Notes),
	}
	return cleaned
}

// EventStore holds the collection of TCG events in SQLite.
type EventStore struct {
	mu sync.RWMutex
	db *sql.DB
}

// NewEventStore creates a default SQLite-backed event catalog for local use.
func NewEventStore() *EventStore {
	store, err := NewEventStoreForPath(filepath.Join("data", "events.db"))
	if err != nil {
		panic(err)
	}
	return store
}

// NewEventStoreForPath returns a ready-to-use SQLite event store in the supplied file path.
func NewEventStoreForPath(databasePath string) (*EventStore, error) {
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, err
	}

	store := &EventStore{db: db}
	if err := store.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.seedDefaults(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *EventStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *EventStore) ensureSchema() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS events (
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
			notes TEXT,
			owner_id TEXT,
			status TEXT NOT NULL DEFAULT 'published'
		);
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id),
			csrf_token TEXT NOT NULL,
			expires_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS event_participants (
			event_id TEXT NOT NULL REFERENCES events(id),
			user_id TEXT NOT NULL REFERENCES users(id),
			joined_at TEXT NOT NULL,
			PRIMARY KEY (event_id, user_id)
		);`)
	if err != nil {
		return err
	}

	rows, err := s.db.Query("PRAGMA table_info(events)")
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !columns["owner_id"] {
		if _, err := s.db.Exec("ALTER TABLE events ADD COLUMN owner_id TEXT"); err != nil {
			return err
		}
	}
	if !columns["status"] {
		if _, err := s.db.Exec("ALTER TABLE events ADD COLUMN status TEXT NOT NULL DEFAULT 'published'"); err != nil {
			return err
		}
	}
	return nil
}

func (s *EventStore) seedDefaults() error {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	now := time.Now().UTC()
	defaultEvents := []Event{
		{ID: "evt-1001", Name: "Commander Night", Game: "Commander", City: "Seattle", Venue: "The Dragon's Den", Date: now.AddDate(0, 0, 2).Format(time.RFC3339), Format: "Casual Draft", Players: 14, Spots: 6, Host: "Avery", Address: "120 Pine St", Notes: "Bring your own sleeves."},
		{ID: "evt-1002", Name: "Modern Clash", Game: "Modern", City: "Seattle", Venue: "Northside Tabletop", Date: now.AddDate(0, 0, 3).Format(time.RFC3339), Format: "Competitive Standard", Players: 16, Spots: 8, Host: "Milo", Address: "45 Lakeview Ave", Notes: "Prize support included."},
		{ID: "evt-1003", Name: "Pokémon League Cup", Game: "Pokémon", City: "Portland", Venue: "Arcade & Co.", Date: now.AddDate(0, 0, 4).Format(time.RFC3339), Format: "Swiss", Players: 10, Spots: 12, Host: "Sofia", Address: "88 River Blvd", Notes: "Junior division available."},
		{ID: "evt-1004", Name: "MTG Draft Social", Game: "Magic: The Gathering", City: "Portland", Venue: "Copper Lantern", Date: now.AddDate(0, 0, 6).Format(time.RFC3339), Format: "Draft", Players: 12, Spots: 4, Host: "Jen", Address: "411 Alder Way", Notes: "Food truck outside after round 2."},
	}

	for _, event := range defaultEvents {
		if _, err := s.db.Exec(`
			INSERT INTO events (id, name, game, city, venue, date, format, players, spots, host, address, notes)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, event.ID, event.Name, event.Game, event.City, event.Venue, event.Date, event.Format, event.Players, event.Spots, event.Host, event.Address, event.Notes); err != nil {
			return err
		}
	}
	return nil
}

func (s *EventStore) List(city, game, query string) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()

	queryBuilder := `SELECT id, name, game, city, venue, date, format, players, spots, host, address, notes, owner_id, status FROM events WHERE status = 'published'`
	args := make([]any, 0, 6)

	if city != "" {
		queryBuilder += " AND lower(city) = lower(?)"
		args = append(args, city)
	}
	if game != "" {
		queryBuilder += " AND lower(game) = lower(?)"
		args = append(args, game)
	}
	if query != "" {
		term := "%" + strings.ToLower(query) + "%"
		queryBuilder += " AND (lower(name) LIKE ? OR lower(city) LIKE ? OR lower(game) LIKE ? OR lower(venue) LIKE ? OR lower(format) LIKE ?)"
		args = append(args, term, term, term, term, term)
	}

	rows, err := s.db.Query(queryBuilder, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	results := make([]Event, 0)
	now := time.Now()
	for rows.Next() {
		var event Event
		var ownerID sql.NullString
		if err := rows.Scan(&event.ID, &event.Name, &event.Game, &event.City, &event.Venue, &event.Date, &event.Format, &event.Players, &event.Spots, &event.Host, &event.Address, &event.Notes, &ownerID, &event.Status); err != nil {
			continue
		}
		if ownerID.Valid {
			event.OwnerID = ownerID.String
		}
		eventTime, err := parseEventTime(event.Date)
		if err != nil || eventTime.Before(now) {
			continue
		}
		results = append(results, event)
	}

	sort.SliceStable(results, func(i, j int) bool {
		iTime, iErr := parseEventTime(results[i].Date)
		jTime, jErr := parseEventTime(results[j].Date)
		switch {
		case iErr == nil && jErr == nil:
			return iTime.Before(jTime)
		case iErr == nil:
			return true
		case jErr == nil:
			return false
		default:
			return results[i].Date < results[j].Date
		}
	})
	return results
}

func parseEventTime(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, errors.New("empty date")
	}
	return time.Parse(time.RFC3339, trimmed)
}

func (s *EventStore) Get(id string) (Event, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var event Event
	var ownerID sql.NullString
	if err := s.db.QueryRow(`SELECT id, name, game, city, venue, date, format, players, spots, host, address, notes, owner_id, status FROM events WHERE id = ?`, id).Scan(
		&event.ID,
		&event.Name,
		&event.Game,
		&event.City,
		&event.Venue,
		&event.Date,
		&event.Format,
		&event.Players,
		&event.Spots,
		&event.Host,
		&event.Address,
		&event.Notes,
		&ownerID,
		&event.Status,
	); err != nil {
		return Event{}, false
	}
	if ownerID.Valid {
		event.OwnerID = ownerID.String
	}
	return event, true
}

func (s *EventStore) Add(ownerID string, evt Event) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	evt = normalizeEvent(evt)
	evt.OwnerID = ownerID
	evt.Status = "published"
	evt.Players = 0
	if err := evt.Validate(); err != nil {
		return Event{}, err
	}

	var owner any
	if ownerID != "" {
		owner = ownerID
	}
	_, err := s.db.Exec(`
		INSERT INTO events (id, name, game, city, venue, date, format, players, spots, host, address, notes, owner_id, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, evt.ID, evt.Name, evt.Game, evt.City, evt.Venue, evt.Date, evt.Format, evt.Players, evt.Spots, evt.Host, evt.Address, evt.Notes, owner, evt.Status)
	if err != nil {
		return Event{}, err
	}
	return evt, nil
}

func (s *EventStore) Update(ownerID, eventID string, evt Event) (Event, error) {
	return s.update(ownerID, eventID, evt, nil)
}

func (s *EventStore) UpdateWithCapacity(ownerID, eventID string, evt Event, capacity int) (Event, error) {
	return s.update(ownerID, eventID, evt, &capacity)
}

func (s *EventStore) update(ownerID, eventID string, evt Event, capacity *int) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var storedOwner sql.NullString
	var players int
	var status string
	err := s.db.QueryRow("SELECT owner_id, players, status FROM events WHERE id = ?", eventID).Scan(&storedOwner, &players, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrEventNotFound
	}
	if err != nil {
		return Event{}, err
	}
	if !storedOwner.Valid || storedOwner.String != ownerID {
		return Event{}, ErrNotEventOwner
	}
	if status != "published" {
		return Event{}, ErrEventCancelled
	}

	evt = normalizeEvent(evt)
	evt.ID = eventID
	evt.OwnerID = ownerID
	evt.Status = status
	evt.Players = players
	if capacity != nil {
		if *capacity < players {
			return Event{}, fmt.Errorf("%w: capacity cannot be less than current player count", ErrInvalidEvent)
		}
		evt.Spots = *capacity - players
	}
	if err := evt.Validate(); err != nil {
		return Event{}, err
	}
	var result sql.Result
	if capacity == nil {
		result, err = s.db.Exec(`
			UPDATE events
			SET name = ?, game = ?, city = ?, venue = ?, date = ?, format = ?, spots = ?,
				host = ?, address = ?, notes = ?
			WHERE id = ? AND owner_id = ? AND status = 'published'
		`, evt.Name, evt.Game, evt.City, evt.Venue, evt.Date, evt.Format, evt.Spots,
			evt.Host, evt.Address, evt.Notes, eventID, ownerID)
	} else {
		result, err = s.db.Exec(`
			UPDATE events
			SET name = ?, game = ?, city = ?, venue = ?, date = ?, format = ?, spots = ? - players,
				host = ?, address = ?, notes = ?
			WHERE id = ? AND owner_id = ? AND status = 'published' AND players <= ?
		`, evt.Name, evt.Game, evt.City, evt.Venue, evt.Date, evt.Format, *capacity,
			evt.Host, evt.Address, evt.Notes, eventID, ownerID, *capacity)
	}
	if err != nil {
		return Event{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return Event{}, err
	}
	if updated != 1 {
		return Event{}, s.updateRejection(ownerID, eventID, capacity)
	}
	return evt, nil
}

func (s *EventStore) updateRejection(ownerID, eventID string, capacity *int) error {
	var storedOwner sql.NullString
	var players int
	var status string
	err := s.db.QueryRow("SELECT owner_id, players, status FROM events WHERE id = ?", eventID).Scan(&storedOwner, &players, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEventNotFound
	}
	if err != nil {
		return err
	}
	if !storedOwner.Valid || storedOwner.String != ownerID {
		return ErrNotEventOwner
	}
	if status != "published" {
		return ErrEventCancelled
	}
	if capacity != nil && players > *capacity {
		return fmt.Errorf("%w: capacity cannot be less than current player count", ErrInvalidEvent)
	}
	return errors.New("event changed while it was being updated")
}

func (s *EventStore) Cancel(ownerID, eventID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var storedOwner sql.NullString
	var status string
	err := s.db.QueryRow("SELECT owner_id, status FROM events WHERE id = ?", eventID).Scan(&storedOwner, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEventNotFound
	}
	if err != nil {
		return err
	}
	if !storedOwner.Valid || storedOwner.String != ownerID {
		return ErrNotEventOwner
	}
	if status != "published" {
		return ErrEventCancelled
	}
	_, err = s.db.Exec("UPDATE events SET status = 'cancelled' WHERE id = ? AND owner_id = ?", eventID, ownerID)
	return err
}

// Handler exposes the HTTP routes for the app.
type Handler struct {
	store *EventStore
}

func NewHandler(store *EventStore) *Handler {
	return &Handler{store: store}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.health)
	mux.HandleFunc("/register", h.register)
	mux.HandleFunc("/login", h.login)
	mux.HandleFunc("/logout", h.logout)
	mux.HandleFunc("/api/account", h.currentAccount)
	mux.HandleFunc("/static/app.css", h.stylesheet)
	mux.HandleFunc("/event/", h.webEvent)
	mux.HandleFunc("/host/events/new", h.hostNewEvent)
	mux.HandleFunc("/host/events", h.hostCreateEvent)
	mux.HandleFunc("/host/events/", h.hostEvent)
	mux.HandleFunc("/events", h.events)
	mux.HandleFunc("/events/", h.eventDetail)
	mux.HandleFunc("/", h.home)
	mux.ServeHTTP(w, r)
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		h.notFound(w)
		return
	}
	h.eventsPage(w, r)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "tcg-party-locator",
	})
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		city := r.URL.Query().Get("city")
		game := r.URL.Query().Get("game")
		query := r.URL.Query().Get("q")
		results := h.store.List(city, game, query)
		writeJSON(w, http.StatusOK, map[string]any{
			"events": results,
			"count":  len(results),
		})
	case http.MethodPost:
		session, ok := h.session(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
			return
		}
		if !validCSRFToken(r, session.CSRFToken) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "invalid CSRF token"})
			return
		}
		var event Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid event payload"})
			return
		}
		event = normalizeEvent(event)
		if err := event.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		stored, err := h.store.Add(session.User.ID, event)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create event"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"event": stored})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (h *Handler) eventDetail(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/join") {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/events/"), "/join")
		if id == "" || strings.Contains(id, "/") {
			h.notFound(w)
			return
		}
		h.eventParticipation(w, r, id)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/events/")
	if id == "" || strings.Contains(id, "/") {
		h.notFound(w)
		return
	}

	switch r.Method {
	case http.MethodGet:
		event, ok := h.store.Get(id)
		if !ok {
			h.notFound(w)
			return
		}
		joined := false
		if session, authenticated := h.session(r); authenticated {
			var err error
			joined, err = h.store.HasJoined(session.User.ID, id)
			if err != nil {
				log.Printf("read event membership: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not read event"})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"event": event, "joined": joined})
	case http.MethodPut:
		session, ok := h.session(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
			return
		}
		if !validCSRFToken(r, session.CSRFToken) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "invalid CSRF token"})
			return
		}
		var event Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid event payload"})
			return
		}
		updated, err := h.store.Update(session.User.ID, id, event)
		if err != nil {
			writeEventError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"event": updated})
	case http.MethodDelete:
		session, ok := h.session(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
			return
		}
		if !validCSRFToken(r, session.CSRFToken) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "invalid CSRF token"})
			return
		}
		if err := h.store.Cancel(session.User.ID, id); err != nil {
			writeEventError(w, err)
			return
		}
		event, ok := h.store.Get(id)
		if !ok {
			h.notFound(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"event": event})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (h *Handler) eventParticipation(w http.ResponseWriter, r *http.Request, eventID string) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	session, ok := h.session(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "authentication required"})
		return
	}
	if !validCSRFToken(r, session.CSRFToken) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "invalid CSRF token"})
		return
	}

	var err error
	joined := r.Method == http.MethodPost
	if joined {
		err = h.store.JoinEvent(session.User.ID, eventID)
	} else {
		err = h.store.LeaveEvent(session.User.ID, eventID)
	}
	if err != nil {
		writeEventError(w, err)
		return
	}
	event, ok := h.store.Get(eventID)
	if !ok {
		h.notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": event, "joined": joined})
}

func writeEventError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidEvent):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
	case errors.Is(err, ErrEventNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "resource not found"})
	case errors.Is(err, ErrNotEventOwner):
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "event owner required"})
	case errors.Is(err, ErrEventCancelled):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "event is cancelled"})
	case errors.Is(err, ErrEventFull):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "event is full"})
	default:
		log.Printf("event operation: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not update event"})
	}
}

func (h *Handler) notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "resource not found"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}

var ErrInvalidEvent = errors.New("invalid event")
var (
	ErrEventNotFound  = errors.New("event not found")
	ErrNotEventOwner  = errors.New("not event owner")
	ErrEventCancelled = errors.New("event is cancelled")
	ErrEventFull      = errors.New("event is full")
)
