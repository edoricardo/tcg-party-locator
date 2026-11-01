package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
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
