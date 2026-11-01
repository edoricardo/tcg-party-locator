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
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Event represents a public TCG gathering or event.
type Event struct {
	ID      string `json:"id"`
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
	if evt.ID == "" {
		evt.ID = fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	if evt.Name == "" {
		evt.Name = "Untitled Event"
	}
	if evt.City == "" {
		evt.City = "Unknown"
	}
	if evt.Date == "" {
		evt.Date = "2026-01-01T18:00:00Z"
	}
	if evt.Game == "" {
		evt.Game = "General TCG"
	}
	if evt.Venue == "" {
		evt.Venue = "TBD"
	}
	if evt.Format == "" {
		evt.Format = "Casual"
	}
	if evt.Host == "" {
		evt.Host = "Host TBD"
	}
	if evt.Players < 0 {
		evt.Players = 0
	}
	if evt.Spots < 0 {
		evt.Spots = 0
	}
	return evt
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
			notes TEXT
		);`)
	return err
}

func (s *EventStore) seedDefaults() error {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	defaultEvents := []Event{
		{ID: "evt-1001", Name: "Commander Night", Game: "Commander", City: "Seattle", Venue: "The Dragon's Den", Date: "2026-11-08T18:30:00Z", Format: "Casual Draft", Players: 14, Spots: 6, Host: "Avery", Address: "120 Pine St", Notes: "Bring your own sleeves."},
		{ID: "evt-1002", Name: "Modern Clash", Game: "Modern", City: "Seattle", Venue: "Northside Tabletop", Date: "2026-11-09T13:00:00Z", Format: "Competitive Standard", Players: 16, Spots: 8, Host: "Milo", Address: "45 Lakeview Ave", Notes: "Prize support included."},
		{ID: "evt-1003", Name: "Pokémon League Cup", Game: "Pokémon", City: "Portland", Venue: "Arcade & Co.", Date: "2026-11-10T17:00:00Z", Format: "Swiss", Players: 10, Spots: 12, Host: "Sofia", Address: "88 River Blvd", Notes: "Junior division available."},
		{ID: "evt-1004", Name: "MTG Draft Social", Game: "Magic: The Gathering", City: "Portland", Venue: "Copper Lantern", Date: "2026-11-12T19:00:00Z", Format: "Draft", Players: 12, Spots: 4, Host: "Jen", Address: "411 Alder Way", Notes: "Food truck outside after round 2."},
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

	queryBuilder := `SELECT id, name, game, city, venue, date, format, players, spots, host, address, notes FROM events WHERE 1 = 1`
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
	queryBuilder += " ORDER BY date ASC"

	rows, err := s.db.Query(queryBuilder, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	results := make([]Event, 0)
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.Name, &event.Game, &event.City, &event.Venue, &event.Date, &event.Format, &event.Players, &event.Spots, &event.Host, &event.Address, &event.Notes); err != nil {
			continue
		}
		results = append(results, event)
	}
	return results
}

func (s *EventStore) Get(id string) (Event, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var event Event
	if err := s.db.QueryRow(`SELECT id, name, game, city, venue, date, format, players, spots, host, address, notes FROM events WHERE id = ?`, id).Scan(
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
	); err != nil {
		return Event{}, false
	}
	return event, true
}

func (s *EventStore) Add(evt Event) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	evt = normalizeEvent(evt)

	_, err := s.db.Exec(`
		INSERT INTO events (id, name, game, city, venue, date, format, players, spots, host, address, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, evt.ID, evt.Name, evt.Game, evt.City, evt.Venue, evt.Date, evt.Format, evt.Players, evt.Spots, evt.Host, evt.Address, evt.Notes)
	if err != nil {
		return Event{}, err
	}
	return evt, nil
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
	writeJSON(w, http.StatusOK, map[string]any{
		"message":  "Welcome to TCG Party Locator!",
		"status":   "running",
		"features": []string{"events", "search", "event-details"},
	})
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
		var event Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid event payload"})
			return
		}
		if err := event.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		stored, err := h.store.Add(event)
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
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/events/")
	if id == "" || strings.Contains(id, "/") {
		h.notFound(w)
		return
	}

	event, ok := h.store.Get(id)
	if !ok {
		h.notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": event})
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
