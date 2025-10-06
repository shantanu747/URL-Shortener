package shortener

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/url"
)

func HandleShortneURLRequest(longUrl string, db *sql.DB) (string, error) {
	// First check if the long URL is already in the database
	shortKey, err := CheckDbForLongURL(context.Background(), db, longUrl)
	// if not generate the short URL and save it to the database

	// return the short URL
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

// GenerateFullShortURL constructs the complete short URL by prepending a base domain
// to the generated short key.
// It takes the original long URL as input and returns the full short URL string.
func generateFullShortURL(longUrl string) (string, error) {
	baseDomain := "http://shan747.urs/"
	shortKey := generateShortURLKey(longUrl)

	if shortKey == "encoded string too short" {
		return "", fmt.Errorf("failed to generate short key")
	}

	// Use url.JoinPath for robust URL construction. This correctly handles
	// joining the domain and key, regardless of trailing slashes.
	fullURL, err := url.JoinPath(baseDomain, shortKey)
	if err != nil {
		return "", fmt.Errorf("failed to construct full short URL: %w", err)
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
