package app

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
)

//go:embed templates/*.html static/app.css
var webFiles embed.FS

type pageData struct {
	Title      string
	LoggedIn   bool
	CSRFToken  string
	Events     []Event
	Event      Event
	Joined     bool
	IsOwner    bool
	City       string
	Game       string
	Query      string
	Capacity   int
	FormAction string
	SubmitText string
	Error      string
}

func (h *Handler) pageData(r *http.Request, title string) pageData {
	data := pageData{Title: title}
	if session, ok := h.session(r); ok {
		data.LoggedIn = true
		data.CSRFToken = session.CSRFToken
	}
	return data
}

func (h *Handler) renderPage(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	h.renderPageStatus(w, r, name, data, http.StatusOK)
}

func (h *Handler) renderPageStatus(w http.ResponseWriter, r *http.Request, name string, data pageData, status int) {
	if data.Title == "" {
		data.Title = "TCG Party Locator"
	}
	page, err := template.New("layout.html").ParseFS(webFiles, "templates/layout.html", "templates/"+name)
	if err != nil {
		log.Printf("parse browser template %q: %v", name, err)
		http.Error(w, "could not render page", http.StatusInternalServerError)
		return
	}
	var body bytes.Buffer
	if err := page.ExecuteTemplate(&body, "layout.html", data); err != nil {
		log.Printf("execute browser template %q: %v", name, err)
		http.Error(w, "could not render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(body.Bytes()); err != nil {
		log.Printf("write browser page: %v", err)
	}
}

func (h *Handler) stylesheet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	content, err := webFiles.ReadFile("static/app.css")
	if err != nil {
		log.Printf("read browser stylesheet: %v", err)
		http.Error(w, "could not read stylesheet", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if _, err := w.Write(content); err != nil {
		log.Printf("write browser stylesheet: %v", err)
	}
}

func (h *Handler) eventsPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data := h.pageData(r, "Find TCG events")
	data.City = r.URL.Query().Get("city")
	data.Game = r.URL.Query().Get("game")
	data.Query = r.URL.Query().Get("q")
	data.Events = h.store.List(data.City, data.Game, data.Query)
	h.renderPage(w, r, "events.html", data)
}

func (h *Handler) webEvent(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/event/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 && parts[0] != "" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		event, ok := h.store.Get(parts[0])
		if !ok {
			http.NotFound(w, r)
			return
		}
		data := h.pageData(r, event.Name)
		data.Event = event
		if session, authenticated := h.session(r); authenticated {
			data.IsOwner = session.User.ID == event.OwnerID
			joined, err := h.store.HasJoined(session.User.ID, event.ID)
			if err != nil {
				log.Printf("read event membership for page: %v", err)
				http.Error(w, "could not read event", http.StatusInternalServerError)
				return
			}
			data.Joined = joined
		}
		h.renderPage(w, r, "event.html", data)
		return
	}
	if len(parts) != 2 || parts[0] == "" || (parts[1] != "join" && parts[1] != "leave") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, ok := h.session(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !validCSRFToken(r, session.CSRFToken) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	var err error
	if parts[1] == "join" {
		err = h.store.JoinEvent(session.User.ID, parts[0])
	} else {
		err = h.store.LeaveEvent(session.User.ID, parts[0])
	}
	if err != nil {
		h.writeBrowserEventError(w, r, err)
		return
	}
	http.Redirect(w, r, "/event/"+parts[0], http.StatusSeeOther)
}

func (h *Handler) hostNewEvent(w http.ResponseWriter, r *http.Request) {
	session, ok := h.requireBrowserSession(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data := h.pageData(r, "Create event")
	data.CSRFToken = session.CSRFToken
	data.Capacity = 8
	data.FormAction = "/host/events"
	data.SubmitText = "Create event"
	h.renderPage(w, r, "host-event-form.html", data)
}

func (h *Handler) hostCreateEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, ok := h.requireBrowserSession(w, r)
	if !ok {
		return
	}
	if !validCSRFToken(r, session.CSRFToken) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	event, err := eventFromForm(r, 0)
	if err != nil {
		data := h.pageData(r, "Create event")
		data.Error = err.Error()
		data.Capacity = 8
		data.FormAction = "/host/events"
		data.SubmitText = "Create event"
		h.renderPageStatus(w, r, "host-event-form.html", data, http.StatusBadRequest)
		return
	}
	event, err = h.store.Add(session.User.ID, event)
	if err != nil {
		log.Printf("create browser event: %v", err)
		data := h.pageData(r, "Create event")
		data.Error = "Could not create event."
		data.Capacity = 8
		data.FormAction = "/host/events"
		data.SubmitText = "Create event"
		h.renderPageStatus(w, r, "host-event-form.html", data, http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/event/"+event.ID, http.StatusSeeOther)
}

func (h *Handler) hostEvent(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/host/events/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != "edit" && parts[1] != "cancel") {
		http.NotFound(w, r)
		return
	}
	session, ok := h.requireBrowserSession(w, r)
	if !ok {
		return
	}
	event, found := h.store.Get(parts[0])
	if !found {
		http.NotFound(w, r)
		return
	}
	if event.OwnerID != session.User.ID {
		http.Error(w, "event owner required", http.StatusForbidden)
		return
	}
	if parts[1] == "cancel" {
		h.cancelBrowserEvent(w, r, session, event)
		return
	}
	h.editBrowserEvent(w, r, session, event)
}

func (h *Handler) editBrowserEvent(w http.ResponseWriter, r *http.Request, session Session, event Event) {
	if r.Method == http.MethodGet {
		if event.Status != "published" {
			http.Error(w, "event is cancelled", http.StatusConflict)
			return
		}
		data := h.pageData(r, "Edit event")
		data.Event = event
		data.CSRFToken = session.CSRFToken
		data.Capacity = event.Players + event.Spots
		data.FormAction = "/host/events/" + event.ID + "/edit"
		data.SubmitText = "Save changes"
		h.renderPage(w, r, "host-event-form.html", data)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRFToken(r, session.CSRFToken) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	updated, err := eventFromForm(r, event.Players)
	if err == nil {
		capacity := updated.Players + updated.Spots
		updated, err = h.store.UpdateWithCapacity(session.User.ID, event.ID, updated, capacity)
	}
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrEventCancelled) {
			status = http.StatusConflict
		}
		data := h.pageData(r, "Edit event")
		data.Event = event
		data.CSRFToken = session.CSRFToken
		data.Capacity = event.Players + event.Spots
		data.FormAction = "/host/events/" + event.ID + "/edit"
		data.SubmitText = "Save changes"
		data.Error = err.Error()
		h.renderPageStatus(w, r, "host-event-form.html", data, status)
		return
	}
	http.Redirect(w, r, "/event/"+updated.ID, http.StatusSeeOther)
}

func (h *Handler) cancelBrowserEvent(w http.ResponseWriter, r *http.Request, session Session, event Event) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRFToken(r, session.CSRFToken) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := h.store.Cancel(session.User.ID, event.ID); err != nil {
		h.writeBrowserEventError(w, r, err)
		return
	}
	http.Redirect(w, r, "/event/"+event.ID, http.StatusSeeOther)
}

func (h *Handler) requireBrowserSession(w http.ResponseWriter, r *http.Request) (Session, bool) {
	session, ok := h.session(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return Session{}, false
	}
	return session, true
}

func eventFromForm(r *http.Request, players int) (Event, error) {
	if err := r.ParseForm(); err != nil {
		return Event{}, errors.New("invalid event form")
	}
	capacity, err := strconv.Atoi(strings.TrimSpace(r.FormValue("capacity")))
	if err != nil || capacity < 1 || capacity < players {
		return Event{}, errors.New("capacity must be at least the current player count")
	}
	event := normalizeEvent(Event{
		Name:    r.FormValue("name"),
		Game:    r.FormValue("game"),
		City:    r.FormValue("city"),
		Venue:   r.FormValue("venue"),
		Date:    r.FormValue("date"),
		Format:  r.FormValue("format"),
		Players: players,
		Spots:   capacity - players,
		Host:    r.FormValue("host"),
		Address: r.FormValue("address"),
		Notes:   r.FormValue("notes"),
	})
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (h *Handler) writeBrowserEventError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrEventNotFound):
		http.NotFound(w, r)
	case errors.Is(err, ErrNotEventOwner):
		http.Error(w, "event owner required", http.StatusForbidden)
	case errors.Is(err, ErrEventCancelled), errors.Is(err, ErrEventFull):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		log.Printf("browser event action: %v", err)
		http.Error(w, "could not update event", http.StatusInternalServerError)
	}
}
