# TCG Party Locator

TCG Party Locator is a Go service for finding local tabletop gaming groups and events.

## Features

- Search and filter upcoming TCG events by city, game, and free-text query
- View individual event details by ID
- Create new events through the HTTP API
- SQLite-backed storage with seeded demo data for local development

## Prerequisites

- Go 1.26 or newer

## Run locally

```bash
go run ./cmd/server
```

The server listens on port 8080 by default. You can override it with the `PORT` environment variable:

```bash
PORT=3000 go run ./cmd/server
```

## Endpoints

- `GET /` — returns a welcome message
- `GET /health` — returns service health information
- `GET /events` — lists events with optional `city`, `game`, and `q` filters
- `POST /events` — creates a new event
- `GET /events/{id}` — returns a specific event

## Example requests

```bash
curl http://localhost:8080/health
curl "http://localhost:8080/events?city=Seattle&game=Commander"
curl -X POST http://localhost:8080/events \
  -H "Content-Type: application/json" \
  -d '{"name":"Commander Night","game":"Commander","city":"Seattle","venue":"The Dragon\'s Den","date":"2026-11-08T18:30:00Z","format":"Casual Draft","players":10,"spots":6,"host":"Avery"}'
```

## Verify

```bash
go test ./...
```

## Notes

The app stores event data in `data/events.db` and creates the database automatically when it starts.
