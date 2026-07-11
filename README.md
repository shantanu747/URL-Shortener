# URL Shortener

A production-quality URL shortening service built with **Go** and **PostgreSQL**. Accepts long URLs via a REST API, generates compact 7-character short keys using SHA-256 + Base64URL encoding, and serves redirect traffic with atomic click analytics. Designed with testability and security as first-class concerns.

---

## Architecture

```
┌─────────────────────────┐        ┌──────────────────────────────────────┐
│      HTTP Client        │        │          Go HTTP Server              │
│  (browser / curl / app) │──────▶ │                                      │
└─────────────────────────┘        │  POST /api/v1/shorten                │
                                   │    → validate → dedup → generate key │
                                   │                                      │
                                   │  GET  /{shortKey}                    │
                                   │    → lookup → increment click count  │
                                   │    → HTTP 302 redirect               │
                                   │                                      │
                                   │  GET  /health                        │
                                   │    → ping DB → return status         │
                                   └──────────────┬───────────────────────┘
                                                  │
                                                  │ lib/pq (PostgreSQL driver)
                                                  ▼
                                   ┌──────────────────────────────────────┐
                                   │           PostgreSQL                  │
                                   │                                      │
                                   │  urls table                         │
                                   │  ├── short_key  VARCHAR(7) UNIQUE   │
                                   │  ├── long_url   TEXT                │
                                   │  ├── created_at TIMESTAMPTZ         │
                                   │  └── click_count INTEGER            │
                                   └──────────────────────────────────────┘
```

---

## Features

- **URL shortening** — 7-character keys from SHA-256 + Base64URL, yielding 64⁷ ≈ 4.4 trillion possible keys
- **Deduplication** — the same long URL always returns the same short URL (idempotent)
- **Collision handling** — up to 5 salt-based retry attempts before failing gracefully
- **SSRF prevention** — blocks requests to localhost, loopback addresses, and private network ranges (10.x, 192.168.x, 172.16.x, 127.x, ::1)
- **XSS protection** — only `http` and `https` schemes are accepted; `javascript:`, `ftp:`, etc. are rejected
- **Atomic click analytics** — single `UPDATE ... RETURNING` query prevents double-counting
- **Health check endpoint** — live database ping for use with load balancers and monitoring
- **Request timeout middleware** — all requests cancelled after 10 seconds to protect against slow clients
- **Interface-based design** — `URLRepository` interface enables full test coverage without a real database

---

## Technology Stack

| Component | Technology |
|---|---|
| Language | Go 1.25 |
| Database | PostgreSQL |
| DB Driver | `github.com/lib/pq` v1.10.9 |
| Config | `github.com/joho/godotenv` v1.5.1 |
| Frontend | React 19 (TypeScript) |
| Testing | Go standard library (`testing`, `net/http/httptest`) |

---

## API Reference

### `POST /api/v1/shorten`

Shortens a long URL. Returns the same short URL if the long URL has been shortened before.

**Request**
```bash
curl -X POST http://localhost:8080/api/v1/shorten \
  -H 'Content-Type: application/json' \
  -d '{"long_url": "https://example.com/some/very/long/path?q=1"}'
```

**Response — 201 Created**
```json
{"short_url": "http://shan747.urs/aB3cD4e"}
```

**Response — 400 Bad Request**
```json
{"error": "validation failed: internal or private URLs are not allowed"}
```

---

### `GET /{shortKey}`

Redirects to the original long URL and increments the click counter.

**Request**
```bash
curl -v http://localhost:8080/aB3cD4e
```

**Response — 302 Found**
```
Location: https://example.com/some/very/long/path?q=1
```

| Status | Cause |
|---|---|
| 302 | Success — redirect to long URL |
| 400 | Short key contains invalid characters or wrong length |
| 404 | Short key not found in database |
| 500 | Unexpected database error |

---

### `GET /health`

Reports service health by pinging the database. Suitable for use with load balancer health checks.

**Response — 200 OK**
```json
{"status": "ok"}
```

**Response — 503 Service Unavailable**
```json
{"status": "unhealthy", "error": "dial tcp: connection refused"}
```

---

## Setup & Installation

### Prerequisites

- Go 1.21 or higher
- PostgreSQL 14 or higher

### Steps

1. **Clone the repository**
   ```bash
   git clone https://github.com/shantanu747/URL-Shortener.git
   cd URL-Shortener
   ```

2. **Create your environment file**
   ```bash
   cp .env.example .env   # or create .env manually
   ```
   Edit `.env` with your database credentials (see [Environment Variables](#environment-variables)).

3. **Create the database and table**
   ```bash
   createdb url_shortener
   psql -d url_shortener -f sql/create_table.sql
   ```

4. **Run the server**
   ```bash
   go run .
   # Starting server on :8080
   ```

---

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `DB_HOST` | Yes | — | PostgreSQL host |
| `DB_PORT` | Yes | — | PostgreSQL port (usually `5432`) |
| `DB_USER` | Yes | — | Database user |
| `DB_PASSWORD` | Yes | — | Database password |
| `DB_NAME` | Yes | — | Database name |
| `BASE_URL` | No | `http://localhost:8080` | Base domain used to construct short URLs |
| `PORT` | No | `8080` | Port the HTTP server listens on |

---

## Running Tests

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run with the race detector (recommended before committing)
go test -race ./...

# Run a specific test by name
go test -v -run TestValidateLongURL ./shortener/

# Run only integration tests
go test -v -run TestHandle ./...
```

The test suite uses only the Go standard library and the existing `github.com/lib/pq` dependency — no test-specific dependencies are required.

---

## Security Considerations

### SSRF Prevention

Any URL pointing to an internal or private network address is rejected before a short key is generated. Blocked ranges:

| Address / Range | Reason |
|---|---|
| `localhost` | Loopback hostname |
| `127.0.0.0/8` | Entire loopback range |
| `0.0.0.0` | Wildcard address |
| `::1` | IPv6 loopback |
| `10.0.0.0/8` | RFC 1918 private range |
| `192.168.0.0/16` | RFC 1918 private range |
| `172.16.0.0/12` | RFC 1918 private range (simplified check) |

### Scheme Enforcement

Only `http` and `https` schemes are accepted. This prevents `javascript:`, `ftp:`, `data:`, and other potentially dangerous schemes from being shortened.

### SQL Injection Prevention

All database queries use parameterized placeholders (`$1`, `$2`). User input is never interpolated into query strings.

---

## Key Design Decisions

**Why SHA-256 + Base64URL?**
SHA-256 produces a 256-bit hash that is then base64url-encoded. Taking the first 7 characters gives a key space of 64⁷ ≈ 4.4 trillion unique values — sufficient for large-scale use while keeping URLs short.

**Why salt-based collision retry instead of random keys?**
The same URL + salt=0 always produces the same key, enabling deduplication without an extra database query per request. Collisions (which are rare) are resolved by incrementing the salt and retrying.

**Why `UPDATE ... RETURNING` for redirects?**
Combining the click count increment and URL lookup into a single atomic query avoids a race condition that would exist with a separate `SELECT` followed by `UPDATE`.

**Why a `URLRepository` interface?**
Decoupling business logic from `*sql.DB` means every code path can be exercised in unit tests using a simple in-memory mock — no test database required.

---

## Future Improvements

- **Custom short keys** — allow users to specify their own alias
- **Expiration / TTL** — automatically expire short URLs after a configurable time
- **Rate limiting** — per-IP or per-API-key request throttling
- **Metrics endpoint** — expose Prometheus-compatible counters for monitoring
- **Redis caching** — cache hot redirect lookups to reduce database load
- **HTTPS enforcement** — redirect HTTP requests to HTTPS in production
- **Link-local address blocking** — extend SSRF prevention to cover `169.254.0.0/16`
