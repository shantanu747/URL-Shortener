package shortener

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/lib/pq"
)

const (
	// Conservative limit from broad compatibility, can be configured to 8192 or higher
	// based on client needs and server configuration
	MaxURLLength = 2048
	// Max retries in the case of collisions or server issues
	MaxRetries = 5
)

// ValidateLongURL checks whether the provided longURL is a valid and safe URL for use in the URL shortener service.
// It performs the following validations:
//   - Ensures the URL does not exceed 2048 characters.
//   - Checks that the URL is properly formatted and parsable.
//   - Verifies that the URL uses either the "http" or "https" scheme.
//   - Prevents Server-Side Request Forgery (SSRF) by disallowing URLs pointing to localhost, 127.0.0.1, or 0.0.0.0.
//
// Returns an error if any validation fails, or nil if the URL is valid.
func ValidateLongURL(longURL string) error {
	// Length check - prevent extremely long URLs
	if len(longURL) > MaxURLLength {
		return fmt.Errorf("url exceeds maximum length of %d characters", MaxURLLength)
	}

	// Validate URL structure
	parsedURL, err := url.Parse(longURL)
	if err != nil {
		return fmt.Errorf("invalid url format %w", err)
	}

	// Check if longURL has a valid scheme for XSS protection
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL must use http or https scheme")
	}

	// SSRF Protection
	host := strings.ToLower(parsedURL.Hostname())
	if host == "localhost" ||
		host == "127.0.0.1" ||
		host == "0.0.0.0" ||
		host == "::1" || // IPv6 localhost
		strings.HasPrefix(host, "127.") || // Entire 127.x.x.x range
		strings.HasPrefix(host, "10.") || // Private network
		strings.HasPrefix(host, "192.168.") || // Private network
		strings.HasPrefix(host, "172.16.") { // Private network (simplified)
		return fmt.Errorf("internal or private URLs are not allowed")
	}

	return nil
}

// HandleShortURLRequest processes a long URL by validating it, checking for an existing shortened version in the database,
// and generating the full shortened URL if it exists. It helps prevent XSS and SSRF attacks by validating the input.
// If the long URL has already been shortened, it returns the existing shortened URL. Otherwise, it returns an error.
//
// Parameters:
//   - longUrl: The original URL to be shortened.
//   - db: A pointer to the SQL database connection.
//
// Returns:
//   - string: The full shortened URL if found.
//   - error: An error if validation fails, the database lookup fails, or the shortened URL cannot be constructed.
func HandleShortURLRequest(longUrl string, db *sql.DB) (string, error) {
	// Validate the input to catch and prevent XSS and SSRF attacks
	if err := ValidateLongURL(longUrl); err != nil {
		return "", fmt.Errorf("validation failed: %w", err)
	}

	// Check if the longURL has already been shortened (dedup)
	shortKey, err := CheckDbForLongURL(context.Background(), db, longUrl)
	if err != nil {
		return "", fmt.Errorf("database lookup failed: %w", err)
	}

	//If exists, return existing shortened URL
	if shortKey != "" {
		return generateFullShortURL(shortKey)
	}

	for attempt := 0; attempt < MaxRetries; attempt++ {
		shortKey = generateShortURLKey(longUrl, attempt)
		err = saveURLToDatabase(context.Background(), db, shortKey, longUrl)

		if err == nil {
			// Success, no collision and shortKey was saved to DB
			break
		}

		//Check of this is a retriable collision
		if isCollisionError(err) {
			// Hash collision occurred, retry with next salt value
			continue
		}

		//non collision error, fail immediately
		return "", fmt.Errorf("failed to save url: %w", err)
	}

	if err != nil {
		// We exhausted all retries
		return "", fmt.Errorf("failed to save url after %d attempts: %w", MaxRetries, err)
	}

	return generateFullShortURL(shortKey)
}

// GenerateShortURLKey creates a short, URL-safe key from a long URL.
// It uses SHA256 to hash the long URL and then Base64 URL encoding to create a string.
// It returns the first 7 characters of the encoded string as the key.
// This approach is deterministic, meaning the same long URL will always produce the same short key.
func generateShortURLKey(longUrl string, salt int) string {
	// Hash the long URL with salt using SHA256
	hasher := sha256.New()
	hasher.Write([]byte(longUrl))
	// Add salt to generate different hashes
	hasher.Write([]byte(fmt.Sprintf(":%d", salt)))
	hashBytes := hasher.Sum(nil)

	// Encode the hash to a URL-safe base64 string
	encoded := base64.URLEncoding.EncodeToString(hashBytes)

	// Ensure the encoded string is long enough
	if len(encoded) < 7 {
		return "encoded string too short"
	}

	// Return the first 7 characters as the key. This provides 64^7 possible keys.
	return encoded[:7]
}

// generateFullShortURL constructs the full shortened URL by joining the base domain
// with the provided shortKey. It uses url.JoinPath to ensure the URL is formed
// correctly, handling any trailing or leading slashes. Returns the complete short URL
// as a string, or an error if URL construction fails.
func generateFullShortURL(shortKey string) (string, error) {
	baseDomain := "http://shan747.urs/"

	// Use url.JoinPath for robust URL construction. This correctly handles
	// joining the domain and key, regardless of trailing slashes.
	fullURL, err := url.JoinPath(baseDomain, shortKey)
	if err != nil {
		return "", fmt.Errorf("Failed to construct full short URL: %w", err)
	}
	return fullURL, nil
}

// CheckDbForLongURL queries the database for an existing long URL.
// If found, it returns the associated short key.
// If not found, it returns an empty string and no error.
// If a database error occurs, it returns an empty string and the error.
func CheckDbForLongURL(ctx context.Context, db *sql.DB, longURL string) (string, error) {
	var shortKey string
	query := "SELECT short_key FROM urls WHERE long_url = $1"

	// QueryRowContext is used because we expect at most one result.
	err := db.QueryRowContext(ctx, query, longURL).Scan(&shortKey)
	if err != nil {
		// If no rows are found, it's not an application error.
		// It simply means the URL isn't in the database yet.
		if err == sql.ErrNoRows {
			return "", nil // Return empty string and nil error as requested.
		}
		// For any other error, wrap it and return for the caller to handle.
		return "", fmt.Errorf("error querying database for long URL: %w", err)
	}

	return shortKey, nil
}

// saveURLToDatabase inserts a new URL mapping into the database.
//
// It stores the short key and its corresponding long URL in the urls table.
// If a collision occurs (the short key already exists), it returns a specific
// error indicating a unique constraint violation.
//
// Parameters:
//   - ctx: Context for controlling the database operation lifecycle
//   - db: Database connection pool
//   - shortKey: The generated short identifier for the URL
//   - longURL: The original long URL to be shortened
//
// Returns:
//   - nil on success
//   - error if the short key already exists (collision) or database insert fails
func saveURLToDatabase(ctx context.Context, db *sql.DB, shortKey string, longURL string) error {
	query := `INSERT INTO urls (short_key, long_url) VALUES ($1, $2)`

	_, err := db.ExecContext(ctx, query, shortKey, longURL)
	if err != nil {
		return fmt.Errorf("database insert failed: %w", err)
	}

	return nil
}

// isCollisionError checks if the provided error is a PostgreSQL unique constraint violation error (code "23505").
// It returns true if the error indicates a collision (e.g., duplicate key), and false otherwise.
func isCollisionError(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

// HandleRedirectRequest retrieves the long URL associated with a short key and increments its click count.
//
// This function performs an atomic UPDATE operation that both increments the click counter
// and returns the associated long URL in a single database query. This ensures accurate
// analytics tracking while serving redirects.
//
// Parameters:
//   - ctx: Context for controlling the database operation lifecycle and enabling timeouts/cancellation
//   - db: Database connection pool
//   - shortKey: The 7-character short identifier to look up
//
// Returns:
//   - string: The original long URL if found
//   - error: If the short key is invalid format, not found in database, or database query fails
func HandleRedirectRequest(ctx context.Context, db *sql.DB, shortKey string) (string, error) {
	// Validate short key format (security)
	if len(shortKey) != 7 {
		return "", fmt.Errorf("invalid short key length")
	}

	var longURL string
	query := `
        UPDATE urls
        SET click_count = click_count + 1
        WHERE short_key = $1
        RETURNING long_url
    `

	err := db.QueryRowContext(ctx, query, shortKey).Scan(&longURL)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("short URL not found")
		}
		return "", fmt.Errorf("database query failed: %w", err)
	}

	return longURL, nil
}
