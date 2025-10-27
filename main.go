package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/shantanu747/URL-Shortener/shortener"

	"github.com/joho/godotenv"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
)

// Struct to hold our database connection
type Store struct {
	db *sql.DB
}

type ShortenRequest struct {
	LongURL string `json:"long_url"`
}

type ShortenResponse struct {
	ShortURL string `json:"short_url"`
	Error    string `json:"error,omitempty"`
}

func (s *Store) handleShorten(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse the JSON request body
	var req ShortenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ShortenResponse{
			Error: "Invalid JSON format",
		})
		return
	}

	// Validate that long_url field is not empty
	if req.LongURL == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ShortenResponse{
			Error: "long_url field is required",
		})
		return
	}

	// Call the shortener logic
	shortURL, err := shortener.HandleShortURLRequest(req.LongURL, s.db)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ShortenResponse{
			Error: err.Error(),
		})
		return
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(ShortenResponse{
		ShortURL: shortURL,
	})
}

func (s *Store) handleRedirect(w http.ResponseWriter, r *http.Request) {
	//path validation
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}

	// Extract the short key from the URL path
	shortKey := r.URL.Path[1:] // Remove leading "/"

	//validate shortKey before database lookup
	if shortKey == "" {
		http.Error(w, "short key required", http.StatusNotFound)
		return
	}

	// Validate characters (should only be base64 URL-safe characters)
	for _, char := range shortKey {
		if !((char >= 'A' && char <= 'Z') ||
			(char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_') {
			http.Error(w, "invalid short key format", http.StatusBadRequest)
			return
		}
	}

	// Call HandleRedirectRequest with proper arguments
	longURL, err := shortener.HandleRedirectRequest(r.Context(), s.db, shortKey)
	if err != nil {
		//Check error type to determine proper status code
		if strings.Contains(err.Error(), "invalid short key length") {
			http.Error(w, err.Error(), http.StatusBadRequest)
		} else if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	// Redirect to the long URL
	http.Redirect(w, r, longURL, http.StatusFound)
}

func main() {
	// Load environment variables from .env file
	err := godotenv.Load()
	if err != nil {
		log.Println("Note: .env file not found, reading from system environment variables")
	}

	// Read database configuration from environment variables
	host := os.Getenv("DB_HOST")
	db_port := os.Getenv("DB_PORT")
	user := os.Getenv("DB_USER")
	password := os.Getenv("DB_PASSWORD")
	dbname := os.Getenv("DB_NAME")

	// Construct the connection string
	psqlInfo := fmt.Sprintf("host=%s port=%s user=%s "+
		"password=%s dbname=%s sslmode=disable",
		host, db_port, user, password, dbname)

	// Open a connection to the database
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()

	// Ping the database to verify the connection is alive
	err = db.Ping()
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) {
			log.Printf("PostgreSQL Error Detected:")
			log.Printf("Error Code: %s", pqErr.Code)
			log.Printf("Error Message: %s", pqErr.Message)
			log.Printf("Details: %s", pqErr.Detail)

			// Provide user friendly error messages for common issues
			switch pqErr.Code {
			case "28P01":
				log.Fatalf("FATAL: Invalid password for user '%s'. Please check your .env file.", user)
			case "3D000":
				log.Fatalf("FATAL: Database '%s' does not exist. Please create it.", dbname)
			default:
				log.Fatalf("FATAL: Unhandled PostgreSQL error: %v", pqErr)
			}
		} else {
			// This is a different kind of issue (e.g. network error)
			log.Fatalf("Error connecting to the database: %v", err)
		}
	}
	fmt.Println("Successfully connected to the PostgreSQL database!")
	// API Server Setup
	store := &Store{db: db}
	mux := http.NewServeMux()

	// Handle the API endpoint for creating a short URL
	mux.HandleFunc("/api/v1/shorten", store.handleShorten)

	// Handle the API endpoint for redirecting to the long URL
	mux.HandleFunc("/", store.handleRedirect)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	serverAddr := fmt.Sprintf(":%s", port)
	log.Printf("Starting server on %s", serverAddr)
	log.Fatal(http.ListenAndServe(serverAddr, mux))

}
