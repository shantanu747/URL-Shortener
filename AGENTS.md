# AGENTS.md — AI Agent Guide

This file is for AI coding agents (Claude Code, Copilot, etc.) working on the URL Shortener codebase. Read it before making any changes.

---

## Project Overview

- **Language**: Go 1.25
- **Module**: `github.com/shantanu747/URL-Shortener`
- **Database**: PostgreSQL (driver: `github.com/lib/pq`)
- **Config**: `.env` file loaded via `github.com/joho/godotenv`

### Key Files

| File | Purpose |
|---|---|
| `main.go` | HTTP server, routing, middleware, `Store` struct |
| `shortener/shortener.go` | Core business logic: validation, key generation, DB access |
| `shortener/shortener_test.go` | Unit tests for `shortener` package |
| `main_test.go` | Integration tests for HTTP handlers |
| `sql/create_table.sql` | PostgreSQL schema |
| `.env` | Local environment variables (never commit) |

---

## How to Run

```bash
# Prerequisite: .env file must exist with DB credentials and BASE_URL
go run .
# Server starts on :8080 by default (configurable via PORT env var)
```

---

## Running Tests

```bash
# Run all tests (no database required — tests use mocks)
go test ./...

# Verbose output
go test -v ./...

# With race detector (run before committing)
go test -race ./...

# Run a single test by name
go test -v -run TestValidateLongURL ./shortener/

# Run all handler tests
go test -v -run TestHandle ./...
```

---

## Go Conventions in This Project

### Context is always the first parameter

All exported functions that touch the database accept `context.Context` as their first argument:

```go
// Correct
func HandleShortURLRequest(ctx context.Context, repo URLRepository, longUrl string) (string, error)

// Wrong — never use context.Background() inside a handler
shortener.HandleShortURLRequest(context.Background(), repo, url)
```

Always propagate `r.Context()` from HTTP handlers. This ensures the request timeout and client disconnects cancel in-flight database queries.

### Error wrapping

Always wrap errors with `%w` to preserve the error chain:

```go
return "", fmt.Errorf("database lookup failed: %w", err)
```

This allows callers to use `errors.As` and `errors.Is` to inspect the underlying error type (e.g., `*pq.Error`).

### Database access goes through URLRepository, not *sql.DB

Business logic in `shortener/shortener.go` must use the `URLRepository` interface, never `*sql.DB` directly. This is what enables mock-based testing.

```go
// Correct — accepts the interface
func HandleShortURLRequest(ctx context.Context, repo URLRepository, longUrl string) (string, error)

// Wrong — couples to the concrete driver
func HandleShortURLRequest(ctx context.Context, db *sql.DB, longUrl string) (string, error)
```

### HTTP handlers are methods on *Store

All handlers live in `main.go` as methods on `*Store`. The `Store` holds the `URLRepository` and `Pinger` dependencies injected at startup.

---

## Debugging Go + PostgreSQL

### PostgreSQL Error Codes

Unwrap errors with `errors.As(err, &pqErr)` to access `pqErr.Code`:

| Code | Meaning | Where Handled |
|---|---|---|
| `23505` | Unique constraint violation (collision) | `isCollisionError()` in shortener.go |
| `28P01` | Invalid password | `main()` startup check |
| `3D000` | Database does not exist | `main()` startup check |

### Common Debugging Steps

1. **SSL errors locally**: Add `sslmode=disable` to the connection string (already done in `main.go`).

2. **Query logging**: Enable verbose query logging in PostgreSQL for development:
   ```sql
   ALTER SYSTEM SET log_statement = 'all';
   SELECT pg_reload_conf();
   ```

3. **Connection refused**: Verify PostgreSQL is running and `DB_HOST`/`DB_PORT` match.

4. **Wrapped pq errors**: The error returned by `repo.Save()` wraps the pq error with `fmt.Errorf("database insert failed: %w", err)`. Use `errors.As` — not string matching — to check the type:
   ```go
   var pqErr *pq.Error
   if errors.As(err, &pqErr) {
       fmt.Println(pqErr.Code)
   }
   ```

---

## Testing Patterns

### Unit Tests (`shortener/shortener_test.go`)

- Package is `package shortener` (same package), giving access to unexported functions like `generateShortURLKey` and `generateFullShortURL`.
- Use `mockRepo` to inject fake database behavior without a real connection.
- Use `t.Setenv("BASE_URL", "...")` for environment variable tests — it auto-restores the original value after the test.
- **Do not combine `t.Parallel()` with `t.Setenv()`** — Go panics at runtime if you do.

```go
// Correct: sequential test with env var
func TestGenerateFullShortURL_EnvVarBaseURL(t *testing.T) {
    t.Setenv("BASE_URL", "https://myshortener.io")
    // ...
}

// Wrong: parallel + setenv panics
func TestFoo(t *testing.T) {
    t.Parallel()
    t.Setenv("KEY", "value") // panic!
}
```

### Integration Tests (`main_test.go`)

- Package is `package main` for access to `Store`, `ShortenRequest`, `ShortenResponse`, etc.
- Use `httptest.NewRequest` and `httptest.NewRecorder` — never start a real HTTP server.
- Inject `mockURLRepo` and `mockPinger` into `Store` directly.

```go
store := &Store{repo: &mockURLRepo{...}, pinger: &mockPinger{}}
req := httptest.NewRequest(http.MethodPost, "/api/v1/shorten", body)
rr := httptest.NewRecorder()
store.handleShorten(rr, req)
```

---

## Logical Concerns

### Deduplication

`FindByLongURL` must be called before generating a new short key. The same long URL always returns the same short URL. Do not skip the dedup check.

### Collision Handling

Short key generation is **deterministic**: the same URL + salt always produces the same key. Collisions are resolved by incrementing the salt (`:0`, `:1`, ..., `:4`). Do not make key generation random — it would break deduplication.

### SSRF Prevention

`ValidateLongURL` blocks the following address ranges. Do not remove or weaken these checks:
- `localhost`, `127.0.0.0/8`, `0.0.0.0`, `::1` (loopback)
- `10.0.0.0/8`, `192.168.0.0/16`, `172.16.0.0/12` (RFC 1918 private)

If you add new address validation, add a corresponding test case to the `TestValidateLongURL` table-driven test.

### Click Count Atomicity

The redirect handler uses `UPDATE urls SET click_count = click_count + 1 WHERE short_key = $1 RETURNING long_url`. This single atomic query both increments the counter and retrieves the URL. Do not split this into separate SELECT + UPDATE — it introduces a race condition.

### click_count Default

The schema sets `click_count DEFAULT 0`. The first redirect increments it to 1. Existing rows created before this default was fixed may have a starting value of 1 — they will increment correctly.

---

## Common Pitfalls

| Pitfall | Correct Approach |
|---|---|
| Using `context.Background()` inside an HTTP handler | Always use `r.Context()` |
| Adding a handler but forgetting to register it in `main()` | Register in `main()` via `mux.HandleFunc(...)` |
| Making `generateShortURLKey` non-deterministic | Keep it deterministic; salt handles retries |
| String-matching pq error codes | Use `errors.As(err, &pqErr)` and check `pqErr.Code` |
| Trailing slash in `BASE_URL` env var | `url.JoinPath` handles it correctly either way, but document it |
| Combining `t.Parallel()` and `t.Setenv()` | Remove `t.Parallel()` from tests that set env vars |
| Importing `github.com/lib/pq` twice (named + blank) | Use only the named import; named imports also trigger `init()` |

---

## Adding a New Endpoint

1. Add a handler method to `*Store` in `main.go`.
2. Register it in `main()` with `mux.HandleFunc(path, store.handlerName)`.
3. Write integration tests in `main_test.go` — test at minimum: wrong method, success, and at least one error case.
4. If the endpoint requires new database operations, add them to the `URLRepository` interface in `shortener/shortener.go`, implement them on `DBStore`, and add mock support to `mockRepo` in the test files.
