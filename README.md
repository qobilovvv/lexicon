# Lexicon — Go + HTMX

The `master` branch of Lexicon uses Go `html/template` and HTMX 2.0.11. The existing Go backend serves both the website and JSON API; React and Vite are not used. The React version remains on the other branches.

## Run

Requires Go 1.24 or newer. From this repository:

```sh
cd backend
go run .
```

Open http://127.0.0.1:8080. To use another port:

```sh
LEXICON_ADDR=127.0.0.1:8081 go run .
```

There is no Node dependency, frontend build, CDN dependency, or separate frontend server. Templates, styles, JavaScript, and the vendored HTMX library are embedded into the Go binary:

```sh
cd backend
go build -o lexicon .
./lexicon
```

## Features

- Register, sign in with email or username, sign out, and edit your profile.
- Create folders; create, read, edit, move, and delete words.
- Search titles and descriptions across folders and sort by date or alphabetically.
- Read documents with safe Markdown headings, paragraphs, and fenced code blocks.
- Read news; the first account can create, edit, and delete posts.
- Upload, center-crop, and resize JPEG/PNG avatars on the server.
- Responsive navigation, inline form errors, and preserved fields after validation failures.

HTMX boosts navigation and forms; live search swaps the results region. Pages are rendered by Go. All core flows also work with regular links and forms when JavaScript is disabled; search then requires submitting the form. Small vanilla JavaScript listeners handle mobile navigation and connection feedback.

The JSON API remains available at `/api/*`. HTML form actions translate fields into requests dispatched in-process to the original API handlers, preserving account isolation, validation, origin checks, rate limiting, password hashing, and session cookies. HTML is escaped by Go templates; the document renderer emits only fixed tags with escaped text. HTMX history snapshots are disabled for account pages.

## Data and existing accounts

Private data and accounts are **not committed** to this branch. By default this version creates an independent `backend/data/lexicon.json`. The first account owns the initial sample library and news; subsequent accounts start empty.

To migrate an existing installation, stop its server and copy the complete data set to this version's `backend/data/`, preserving names:

- `lexicon.json`
- `lexicon.json.accounts.json`
- `lexicon.json.workspaces/`
- `lexicon.json.news.json`, if present

Alternatively, set `LEXICON_DATA_FILE` to the base file's absolute path. Do not run both servers against the same data set: storage uses in-process locking. Data and secrets are ignored by Git. Keep data outside public web directories.

For deployment, run this Go service on a host supporting Go binaries or containers and HTTPS. Preserve the Host header through your reverse proxy and set `LEXICON_COOKIE_SECURE=true` when TLS terminates there. Sites' static/Worker hosting cannot run this Go service. Password reset, email verification, and password changes remain outside the existing backend's features.

## Checks

```sh
cd backend
go test -race ./...
go vet ./...
```

The copied backend account tests remain, with additional tests exercising HTML authentication, forms, search, escaping, isolation, origin checks, owner permissions, profile updates, avatar resizing, and session expiry. Tests use temporary data directories.

## Layout

- `backend/main.go`, `accounts.go`, `news.go`: copied Go backend; main now serves the website and API.
- `backend/website.go`: HTML routes, view models, form adapter, safe document rendering, image resizing.
- `backend/web/templates/`: server-rendered pages.
- `backend/web/static/`: original styles with HTMX layout additions, small UI script, and vendored HTMX with its license.

HTMX documentation: https://htmx.org/docs/
