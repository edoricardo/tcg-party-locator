# TCG Party Locator

TCG Party Locator helps players find local trading card game events and gives hosts a place to manage events and attendance. It is a responsive Go web app backed by SQLite.

## Run locally

Requires Go 1.26 or newer.

```bash
go run ./cmd/server
```

The server listens on port 8080 by default. Set `PORT` to use another port:

```bash
PORT=3000 go run ./cmd/server
```

Open [http://localhost:8080](http://localhost:8080) to browse upcoming events. Create an account to join or leave events, or to host and manage your own events. Discovery supports city, game, and text filters. Hosts can edit or cancel their events; cancelled and full events cannot be joined.

## Scope

Accounts use email and password. One account can both play and host. Events are public, and players join immediately while capacity remains; there is no waitlist or host approval flow. Location search uses a city or region rather than browser geolocation.

Tournament matchmaking, Swiss or round-robin pairings, match reporting, standings, and tournament results are deferred.

## Browser routes

- `GET /` — discover and filter upcoming events
- `GET /event/{id}` — event details and join/leave actions
- `GET /login`, `POST /login` — sign in
- `GET /register`, `POST /register` — create an account
- `POST /logout` — sign out
- `GET /host/events/new`, `POST /host/events` — create an event
- `GET /host/events/{id}/edit`, `POST /host/events/{id}/edit` — edit an event
- `POST /host/events/{id}/cancel` — cancel an event
- `GET /static/app.css` — responsive stylesheet

## JSON API

- `GET /health` — service health
- `GET /api/account` — current account (401 when signed out)
- `GET /events` — list upcoming published events; accepts optional `city`, `game`, and `q` filters
- `POST /events` — create an event
- `GET /events/{id}` — event details
- `PUT /events/{id}` — update an owned event
- `DELETE /events/{id}` — cancel an owned event
- `POST /events/{id}/join` — join an event
- `DELETE /events/{id}/join` — leave an event
- `POST /register`, `POST /login`, `POST /logout` — account and session actions

Authenticated mutations require the session CSRF token, supplied as `_csrf` for form submissions or `X-CSRF-Token` for JSON requests. API event `spots` is the number of seats remaining; new events start with no players. In the host form, capacity is the total number of seats.

## Verify

```bash
go test ./...
```

The application stores data in `data/events.db` and creates the SQLite database automatically when it starts.
