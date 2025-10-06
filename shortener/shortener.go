package shortener

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

const (
	// Conservative limit from broad compatibility, can be configured to 8192 or higher
	// based on client needs and server configuration
	MaxURLLength = 2048
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
		return fmt.Errorf("URL exceeds maximum length of %d characters", MaxLength)
	}

	// Validate URL structure
	parsedURL, err := url.Parse(longURL)
	if err != nil {
		return fmt.Errorf("Invalid URL format %w", err)
	}

	// Check if longURL has a valid scheme for XSS protection
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL must use http or https scheme")
	}

	//SSRF Portection
	host := strings.ToLower(parsedURL.Host)
	if strings.Contains(host, "localhost") ||
		strings.Contains(host, "127.0.0.1") ||
		strings.Contains(host, "0.0.0.0") {
		return fmt.Errorf("Internal URLs are not allowed")
	}

	return nil
}

func HandleShortURLRequest(longUrl string, db *sql.DB) (string, error) {
	// Validate the input to catch and prevent XSS and SSRF attacks
	if err := ValidateLongURL(longUrl); err != nil {
		return "", fmt.Errorf("Validation failed: %w", err)
	}

	// Check if the longURL has already been shortened (dedup)
	shortKey, err := CheckDbForLongURL(context.Background(), db, longUrl)
	if err != nil {
		return "", fmt.Errorf("Database lookup failed: %w", err)
	}

	//If exists, return existing shortened URL
	if shortKey != "" {
		shortenedURL, err := generateFullShortURL(shortKey)

		if err == nil {
			return shortenedURL, err
		}
	}
	return "", fmt.Errorf("Failed to construct full short URL.")
}

// GenerateShortURLKey creates a short, URL-safe key from a long URL.
// It uses SHA256 to hash the long URL and then Base64 URL encoding to create a string.
// It returns the first 7 characters of the encoded string as the key.
// This approach is deterministic, meaning the same long URL will always produce the same short key.
func generateShortURLKey(longUrl string) string {
	// Hash the long URL using SHA256
	hasher := sha256.New()
	hasher.Write([]byte(longUrl))
	hashBytes := hasher.Sum(nil)

	// Encode the hash to a URL-safe base64 string
	encoded := base64.URLEncoding.EncodeToString(hashBytes)

	// Ensure the encoded string is long enough
	if len(encoded) < 7 {
		return "encoded string too short"
	}

	// Return the first 7 characters as the key. This provides 64^7 possible keys.
	// TODO(shantanu): Check for and handle collisions in a real-world production system
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
	// Note: I'm assuming your table is named 'urls' and has columns 'short_key' and 'long_url'.
	// Please adjust the query if your schema is different.
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

func HandleRedirectRequest(shortUrl string) {
	// Look up the short URL in the database

	// if found, redirect to the long URL

	// if not found, return a 404 error
}
