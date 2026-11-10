# TCG Party Locator — Product Design

## Intent

Build a responsive web application that players and event hosts can use from a browser. Players should be able to find nearby TCG events; hosts should be able to publish and manage events. The first release should cover the player/host event lifecycle, with tournament matchmaking deferred.

## Goals

- Let players discover upcoming events by location and relevant TCG details.
- Let hosts create and manage events.
- Let players join and leave events, with attendance and capacity kept accurate.
- Work on both mobile and desktop browsers without requiring an app installation.
- Structure event participation so tournament formats can be added later.

## Non-goals

- Native iOS or Android applications.
- Swiss or round-robin pairings, match reporting, standings, or tournament results in the first release.
- A general-purpose social network unrelated to finding and joining TCG events.

## Anti-goals

- Delaying event discovery until tournament pairing logic is complete.
- Showing stale or misleading availability when a player joins or leaves.
- Requiring players to install an application to search for events.

## Current state and constraints

The repository is a Go HTTP service with a SQLite-backed event catalog. It supports event listing with city, game, and text filters, event details, and event creation. The `Event` model includes a host name and player/spot counts. There is no browser UI, account system, event ownership, or participant membership model.

The design builds on that service and its existing Go/SQLite stack. Implementers should account for the existing database and API when evolving the event model; migration and implementation details remain for planning.

## Approaches considered

1. **Discovery MVP:** browse and publish events, postponing accounts and participation. This is the quickest path to local discovery but provides limited party management.
2. **Player/host lifecycle (selected):** accounts, discovery, host event management, player joining/leaving, and capacity tracking. This supports both audiences end-to-end while keeping tournament pairing rules out of the first release.
3. **Tournament-complete:** add tournament registration, pairings, rounds, and results in the first release. This is broader and would make event discovery depend on more complex tournament rules.

The player/host lifecycle was selected because the primary value is helping players find nearby games, and hosts also need to publish and manage those events. Pairing algorithms can follow after the basic event and participation lifecycle works.

## Product behavior

- The user-facing product is a responsive browser application for phones and desktops.
- Players can search and view upcoming TCG events, including casual, competitive, and tournament events.
- Hosts have accounts and can create and manage their events.
- Players have accounts and can join or leave events.
- Event capacity and participant totals reflect membership changes.
- “Party” in this first release means an event and its participant roster; it does not mean tournament pairings.
- Matchmaking formats such as round robin and Swiss are future scope, not part of this release.

## Confirmed v1 decisions

- Accounts use app-managed email/password sign-in; no external identity provider is required for v1.
- One account can act as both a player and an event host.
- Events are public when published. Hosts can cancel them; players cannot join once capacity is reached. There is no approval flow or waitlist in v1.
- Players find events by entering a city or region. Browser geolocation and distance/radius search are out of scope for v1.

## Remaining implementation choices

The implementation plan should select secure credential storage and session handling appropriate to the existing Go service, and determine the least disruptive SQLite migration and API changes needed for accounts, event ownership, and participant membership.
