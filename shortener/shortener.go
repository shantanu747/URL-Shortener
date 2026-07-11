package shortener

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/lib/pq"
)

const (
	// MaxURLLength is a conservative limit for broad client compatibility.
	// Can be raised to 8192 for specific use cases.
	MaxURLLength = 2048
	// MaxRetries is the number of collision retry attempts before giving up.
	MaxRetries = 5
)

// URLRepository defines the data access contract for the shortener.
// Using an interface decouples business logic from the database driver,
// enabling easy unit testing with a mock implementation.
type URLRepository interface {
	// FindByLongURL returns the existing short key for a long URL,
	// or an empty string if none exists.
	FindByLongURL(ctx context.Context, longURL string) (string, error)

	// Save inserts a new short_key → long_url mapping.
	// Returns an error on collision (unique constraint violation) or other DB errors.
	Save(ctx context.Context, shortKey, longURL string) error

	// FindByShortKey returns the long URL for a given short key
	// and atomically increments its click count.
	FindByShortKey(ctx context.Context, shortKey string) (string, error)
}

// DBStore is the production implementation of URLRepository backed by PostgreSQL.
type DBStore struct {
	DB *sql.DB
}

// FindByLongURL queries the database for an existing short key for the given long URL.
// Returns an empty string (no error) if the URL has not been shortened before.
func (s *DBStore) FindByLongURL(ctx context.Context, longURL string) (string, error) {
	var shortKey string
	query := "SELECT short_key FROM urls WHERE long_url = $1"
	err := s.DB.QueryRowContext(ctx, query, longURL).Scan(&shortKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("error querying database for long URL: %w", err)
	}
	return shortKey, nil
}

// Save inserts a new short_key → long_url mapping into the database.
// Returns an error if the short key already exists (collision) or on any other DB failure.
func (s *DBStore) Save(ctx context.Context, shortKey, longURL string) error {
	query := `INSERT INTO urls (short_key, long_url) VALUES ($1, $2)`
	_, err := s.DB.ExecContext(ctx, query, shortKey, longURL)
	if err != nil {
		return fmt.Errorf("database insert failed: %w", err)
	}
	return nil
}

// FindByShortKey atomically increments the click count and returns the long URL
// for the given short key. Returns an error if the key is not found.
func (s *DBStore) FindByShortKey(ctx context.Context, shortKey string) (string, error) {
	var longURL string
	query := `
		UPDATE urls
		SET click_count = click_count + 1
		WHERE short_key = $1
		RETURNING long_url
	`
	err := s.DB.QueryRowContext(ctx, query, shortKey).Scan(&longURL)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("short URL not found")
		}
		return "", fmt.Errorf("database query failed: %w", err)
	}
	return longURL, nil
}

// ValidateLongURL checks whether the provided URL is valid and safe for shortening.
//
// Validations performed:
//   - Length: must not exceed MaxURLLength (2048) characters
//   - Format: must be a parsable URL
//   - Scheme: must be "http" or "https" (blocks javascript:, ftp:, etc.)
//   - SSRF prevention: blocks localhost, loopback addresses, and private network ranges
func ValidateLongURL(longURL string) error {
	if len(longURL) > MaxURLLength {
		return fmt.Errorf("url exceeds maximum length of %d characters", MaxURLLength)
	}

	parsedURL, err := url.Parse(longURL)
	if err != nil {
		return fmt.Errorf("invalid url format: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL must use http or https scheme")
	}

	// SSRF protection: block internal/private network URLs
	host := strings.ToLower(parsedURL.Hostname())
	if host == "localhost" ||
		host == "127.0.0.1" ||
		host == "0.0.0.0" ||
		host == "::1" ||
		strings.HasPrefix(host, "127.") ||
		strings.HasPrefix(host, "10.") ||
		strings.HasPrefix(host, "192.168.") ||
		strings.HasPrefix(host, "172.16.") {
		return fmt.Errorf("internal or private URLs are not allowed")
	}

	return nil
}

// HandleShortURLRequest processes a URL shortening request.
//
// It validates the URL, checks for an existing short key (deduplication),
// and generates a new short key with collision retry if needed.
// The caller's context is propagated to all database operations.
//
// Parameters:
//   - ctx: request context for cancellation and timeout propagation
//   - repo: data access layer (use DBStore in production, mockRepo in tests)
//   - longUrl: the original URL to shorten
func HandleShortURLRequest(ctx context.Context, repo URLRepository, longUrl string) (string, error) {
	if err := ValidateLongURL(longUrl); err != nil {
		return "", fmt.Errorf("validation failed: %w", err)
	}

	// Check for existing short key (deduplication)
	shortKey, err := repo.FindByLongURL(ctx, longUrl)
	if err != nil {
		return "", fmt.Errorf("database lookup failed: %w", err)
	}

	if shortKey != "" {
		return generateFullShortURL(shortKey)
	}

	// Generate a new short key with collision retry
	for attempt := 0; attempt < MaxRetries; attempt++ {
		shortKey = generateShortURLKey(longUrl, attempt)
		err = repo.Save(ctx, shortKey, longUrl)
		if err == nil {
			break
		}
		if isCollisionError(err) {
			continue
		}
		return "", fmt.Errorf("failed to save url: %w", err)
	}

	if err != nil {
		return "", fmt.Errorf("failed to save url after %d attempts: %w", MaxRetries, err)
	}

	return generateFullShortURL(shortKey)
}

// generateShortURLKey creates a 7-character URL-safe key from a long URL and a salt.
//
// Algorithm: SHA256(longUrl + ":" + salt) → Base64URL → first 7 chars
// This is deterministic: same URL and salt always produce the same key.
// The salt parameter enables collision retry by producing different hashes.
// 7 characters of base64URL encoding provides 64^7 ≈ 4.4 trillion possible keys.
func generateShortURLKey(longUrl string, salt int) string {
	hasher := sha256.New()
	hasher.Write([]byte(longUrl))
	hasher.Write([]byte(fmt.Sprintf(":%d", salt)))
	hashBytes := hasher.Sum(nil)

	encoded := base64.URLEncoding.EncodeToString(hashBytes)

	if len(encoded) < 7 {
		return "encoded string too short"
	}

	return encoded[:7]
}

// generateFullShortURL constructs the complete shortened URL from a short key.
//
// The base domain is read from the BASE_URL environment variable.
// Falls back to "http://localhost:8080" if BASE_URL is not set.
// Uses url.JoinPath for correct slash handling.
func generateFullShortURL(shortKey string) (string, error) {
	baseDomain := os.Getenv("BASE_URL")
	if baseDomain == "" {
		baseDomain = "http://localhost:8080"
	}

	fullURL, err := url.JoinPath(baseDomain, shortKey)
	if err != nil {
		return "", fmt.Errorf("failed to construct full short URL: %w", err)
	}
	return fullURL, nil
}

// HandleRedirectRequest retrieves the long URL for a short key and increments its click count.
//
// Uses an atomic UPDATE...RETURNING query to ensure accurate analytics
// without a separate SELECT. The caller's context is propagated to the DB operation.
//
// Parameters:
//   - ctx: request context for cancellation and timeout propagation
//   - repo: data access layer
//   - shortKey: the 7-character identifier to look up
func HandleRedirectRequest(ctx context.Context, repo URLRepository, shortKey string) (string, error) {
	if len(shortKey) != 7 {
		return "", fmt.Errorf("invalid short key length")
	}
	return repo.FindByShortKey(ctx, shortKey)
}

// isCollisionError reports whether err is a PostgreSQL unique constraint violation (code 23505).
func isCollisionError(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}
