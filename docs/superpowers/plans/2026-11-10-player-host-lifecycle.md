# TCG Player/Host Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a responsive browser app where players can find local TCG events and hosts can manage events and attendance.

**Architecture:** Extend the existing Go `net/http` and SQLite application with app-managed accounts, persisted sessions, event ownership, and participant membership. Render the browser experience with Go `html/template` and local CSS; keep the existing JSON event routes available while the root route becomes the browser discovery page.

**Tech Stack:** Go 1.26.5, `net/http`, `database/sql`, `modernc.org/sqlite`, `html/template`, `golang.org/x/crypto/bcrypt`, Go standard library.

**Spec:** `docs/superpowers/specs/2026-11-10-tcg-party-locator-design.md`

## Global Constraints

- The user-facing product is a responsive browser application for phones and desktops.
- Accounts use app-managed email/password sign-in; no external identity provider is required for v1.
- One account can act as both a player and an event host.
- Events are public when published. Hosts can cancel them; players cannot join once capacity is reached. There is no approval flow or waitlist in v1.
- Players find events by entering a city or region. Browser geolocation and distance/radius search are out of scope for v1.
- Swiss or round-robin pairings, match reporting, standings, and tournament results are not part of this release.
- Build on the existing Go/SQLite service and preserve the existing JSON event routes.

## Review Focus

- Case-insensitive duplicate email registration, invalid credentials, and expired sessions must not authenticate; cover with `TestRegisterRejectsDuplicateEmailCaseInsensitive`, `TestLoginRejectsInvalidCredentials`, and `TestExpiredSessionRejected` in Task 1.
- A non-owner must not edit or cancel an event; cover with `TestEventManagementRejectsNonOwner` in Task 2.
- Cancelled events must not appear in discovery and cannot be joined; cover with `TestListEventsExcludesCancelled` in Task 2 and `TestJoinCancelledEventRejected` in Task 3.
- Concurrent attempts for the final spot must not exceed capacity; cover with `TestConcurrentJoinsRespectCapacity` in Task 3.
- Event content supplied by a host must be escaped in HTML; cover with `TestEventPageEscapesUserContent` in Task 4.

---

### Task 1: Accounts and Sessions

**Files:**
- Modify: `go.mod`, `go.sum`
- Modify: `internal/app/app.go` (`EventStore.ensureSchema`, `NewHandler`, `ServeHTTP`)
- Create: `internal/app/accounts.go`
- Test: `internal/app/app_test.go`

**Interfaces:**
- Produces `User{ID string, Email string}`.
- Produces `(*EventStore).Register(email, password string) (User, error)`.
- Produces `(*EventStore).Authenticate(email, password string) (User, error)`.
- Produces `Session{User User, CSRFToken string}`, `(*EventStore).CreateSession(userID string, expiresAt time.Time) (sessionToken, csrfToken string, err error)`, `(*EventStore).ResolveSession(sessionToken string) (Session, bool)`, and `(*EventStore).DeleteSession(sessionToken string) error`.
- Produces handler helpers to set, read, and clear the `tcg_session` cookie. Store only a hash of the random session token; cookies are `HttpOnly`, `SameSite=Lax`, path `/`, expire after seven days, and set `Secure` on TLS requests.
- Authenticated state-changing requests validate a per-session CSRF token from a form field or `X-CSRF-Token` header.
- `POST /register` creates an account and establishes a session; `POST /login` establishes a session; `POST /logout` invalidates it. Invalid login returns a generic error. Email is normalized case-insensitively and unique. Passwords are stored with bcrypt hashes, never plaintext. No email verification or password-reset flow is in v1.

- [ ] **Step 1: Write failing tests** in `internal/app/app_test.go` for account registration and login, case-insensitive duplicate email, invalid login, session cookie attributes, logout invalidation, and expired-session rejection.
- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./internal/app -run 'Test(Register|Login|Session|Expired)' -count=1`
Expected: FAIL because account storage and session routes do not exist.

- [ ] **Step 3: Implement account/session storage and handlers** in `internal/app/accounts.go`; add bcrypt and create `users` and `sessions` tables in `EventStore.ensureSchema`.
- [ ] **Step 4: Add a migration test** that opens a database created with the current `events` schema and verifies its seeded event remains readable after the new schema is installed.
- [ ] **Step 5: Run focused account and migration tests**

Run: `go test ./internal/app -run 'Test(Register|Login|Session|Expired|ExistingDatabase)' -count=1`
Expected: PASS.

- [ ] **Step 6: Commit the account/session slice**

```bash
git add go.mod go.sum internal/app/app.go internal/app/accounts.go internal/app/app_test.go
git commit -m "feat: add player and host accounts" -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

### Task 2: Owned Event Management

**Files:**
- Modify: `internal/app/app.go` (`Event`, schema migration, `List`, `Get`, `Add`, event handlers, routes)
- Modify: `internal/app/app_test.go`

**Interfaces:**
- Event ownership is stored in a nullable `owner_id` column; old seeded events remain public and ownerless. Add a `status` column defaulting old rows to `published`.
- Add `Event.Status` to represent `published` or `cancelled`; keep ownership internal and do not expose `owner_id` in JSON.
- `(*EventStore).Add(ownerID string, evt Event) (Event, error)`, `(*EventStore).Update(ownerID, eventID string, evt Event) (Event, error)`, and `(*EventStore).Cancel(ownerID, eventID string) error` enforce ownership in the store.
- Existing `GET /events`, `POST /events`, and `GET /events/{id}` JSON routes remain available. Creating, updating, or cancelling requires the authenticated account. Add `PUT /events/{id}` for edits and `DELETE /events/{id}` to cancel; a delete changes status rather than erasing the event.
- New events start with `players = 0`; ignore client-supplied `players`, and treat API `spots` as remaining seats. The existing public `host` label remains event text and is not used for authorization. For HTML forms, capacity means total seats and is converted to remaining `spots` after accounting for registered players.
- `List` returns only future `published` events and preserves the existing case-insensitive exact city and game filters, free-text search, and date ordering. Seeded demo event dates are generated relative to startup so the demo remains discoverable.

- [ ] **Step 1: Write failing tests** for event creation requiring a session, ownership assignment, owner-only edit/cancel, cancellation persistence, cancelled and past events excluded from `List`, and preservation of old ownerless seeded events.
- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./internal/app -run 'Test(CreateEvent|EventManagement|ListEventsExcludes(Cancelled|Past)|ExistingDatabase)' -count=1`
Expected: FAIL because events are not owned and have no lifecycle status.

- [ ] **Step 3: Implement event ownership, status migration, and owner-checked create/update/cancel operations** in `internal/app/app.go`; authenticate mutations from the session established in Task 1.
- [ ] **Step 4: Run event-management and regression tests**

Run: `go test ./internal/app -count=1`
Expected: PASS, including existing validation, filtering, and event-detail tests updated for authenticated creation.

- [ ] **Step 5: Commit the event-management slice**

```bash
git add internal/app/app.go internal/app/app_test.go
git commit -m "feat: add owned event management" -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

### Task 3: Join, Leave, and Capacity

**Files:**
- Modify: `internal/app/app.go` (event schema, event detail, routes)
- Create: `internal/app/participation.go`
- Modify: `internal/app/app_test.go`

**Interfaces:**
- Produces `(*EventStore).JoinEvent(userID, eventID string) error` and `(*EventStore).LeaveEvent(userID, eventID string) error`.
- Persist participants in `event_participants(event_id, user_id, joined_at)` with a unique `(event_id, user_id)` key.
- `POST /events/{id}/join` and `DELETE /events/{id}/join` require a session. Joining an already joined event is idempotent; joining a cancelled or full event returns a conflict. Leaving removes only the caller's membership.
- Update participant membership and `players`/`spots` counts in one SQLite transaction. A successful join increments `players` and decrements `spots`; leaving reverses both. Cancelled events cannot be joined.
- Public event detail includes current counts; only an authenticated viewer's own membership state is exposed.

- [ ] **Step 1: Write failing tests** for successful join/leave, duplicate join idempotence, full-event rejection, cancelled-event rejection, and concurrent joins competing for one remaining spot.
- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./internal/app -run 'Test(Join|Leave|ConcurrentJoins)' -count=1`
Expected: FAIL because participant membership and transactional capacity updates do not exist.

- [ ] **Step 3: Implement participant persistence and transactional join/leave** in `internal/app/participation.go`; add the membership table and route handling.
- [ ] **Step 4: Run participation tests and the full app package**

Run: `go test ./internal/app -count=1`
Expected: PASS; participant counts never exceed capacity and return to their prior values after leave.

- [ ] **Step 5: Commit the participation slice**

```bash
git add internal/app/app.go internal/app/participation.go internal/app/app_test.go
git commit -m "feat: add event participation" -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

### Task 4: Responsive Browser App and Handoff

**Files:**
- Modify: `internal/app/app.go` (root route and handler wiring)
- Create: `internal/app/web.go`
- Create: `internal/app/templates/layout.html`
- Create: `internal/app/templates/events.html`
- Create: `internal/app/templates/event.html`
- Create: `internal/app/templates/login.html`
- Create: `internal/app/templates/register.html`
- Create: `internal/app/templates/host-event-form.html`
- Create: `internal/app/static/app.css`
- Modify: `internal/app/app_test.go`
- Modify: `README.md`

**Interfaces:**
- `GET /` renders event discovery with city/region, game, and text filters.
- `GET /event/{id}` renders event details and available seats; authenticated players can join/leave from the page.
- `POST /event/{id}/join` and `POST /event/{id}/leave` are browser form actions that call the Task 3 membership operations and redirect back to the event.
- `GET`/`POST /login` and `/register`, and `POST /logout`, render/use the account flows from Task 1.
- `GET /host/events/new`, `POST /host/events`, `GET`/`POST /host/events/{id}/edit`, and `POST /host/events/{id}/cancel` provide owner-only host management.
- Render templates with `html/template`; serve local CSS without a JavaScript framework. Keep JSON event routes and `/health` behavior. All forms use POST-redirect-GET for successful mutations.
- Include the session CSRF token in every authenticated mutation form; validate it in form and JSON event mutation handlers.
- Browser pages show public event details and do not expose account email addresses. Event dates use the existing RFC3339 model.

- [ ] **Step 1: Write failing handler tests** for discovery HTML, filters, details, login/register forms, host-only management, join/leave actions, invalid CSRF tokens, and escaping of event-supplied HTML.
- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./internal/app -run 'Test(Home|EventPage|LoginPage|RegisterPage|HostPage|EventPageEscapes)' -count=1`
Expected: FAIL because browser routes and templates do not exist.

- [ ] **Step 3: Implement HTML handlers and templates** in `internal/app/web.go` and `internal/app/templates/`; use existing store and session interfaces.
- [ ] **Step 4: Add responsive styles** in `internal/app/static/app.css` for narrow and wide layouts, visible focus states, and readable forms and event cards.
- [ ] **Step 5: Update `README.md`** with browser usage, account/event flows, current scope and deferred tournament matchmaking, API routes, and `go test ./...`.
- [ ] **Step 6: Run the full test suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit the browser app and documentation**

```bash
git add internal/app/app.go internal/app/web.go internal/app/templates internal/app/static/app.css internal/app/app_test.go README.md
git commit -m "feat: add responsive event locator web app" -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```
