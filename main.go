package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shantanu747/URL-Shortener/shortener"

	"github.com/joho/godotenv"
	"github.com/lib/pq"
)

// Pinger abstracts the database ping operation for health checks,
// allowing the health endpoint to be tested without a real database connection.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// Store holds the application's dependencies, injected at startup.
type Store struct {
	repo   shortener.URLRepository
	pinger Pinger
}

type ShortenRequest struct {
	LongURL string `json:"long_url"`
}

type ShortenResponse struct {
	ShortURL string `json:"short_url,omitempty"`
	Error    string `json:"error,omitempty"`
}

// writeJSON writes a JSON-encoded response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: failed to encode response: %v", err)
	}
}

func (s *Store) handleShorten(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ShortenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ShortenResponse{Error: "Invalid JSON format"})
		return
	}

	if req.LongURL == "" {
		writeJSON(w, http.StatusBadRequest, ShortenResponse{Error: "long_url field is required"})
		return
	}

	shortURL, err := shortener.HandleShortURLRequest(r.Context(), s.repo, req.LongURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ShortenResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, ShortenResponse{ShortURL: shortURL})
}

func (s *Store) handleRedirect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}

	shortKey := r.URL.Path[1:]

	if shortKey == "" {
		http.Error(w, "short key required", http.StatusNotFound)
		return
	}

	for _, char := range shortKey {
		if !((char >= 'A' && char <= 'Z') ||
			(char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_') {
			http.Error(w, "invalid short key format", http.StatusBadRequest)
			return
		}
	}

	longURL, err := shortener.HandleRedirectRequest(r.Context(), s.repo, shortKey)
	if err != nil {
		if strings.Contains(err.Error(), "invalid short key length") {
			http.Error(w, err.Error(), http.StatusBadRequest)
		} else if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	http.Redirect(w, r, longURL, http.StatusFound)
}

// handleHealth reports the service health by pinging the database.
// Returns 200 {"status":"ok"} if healthy, 503 {"status":"unhealthy",...} otherwise.
func (s *Store) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.pinger.PingContext(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "unhealthy",
			"error":  err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// timeoutMiddleware wraps every request with a context deadline.
// This ensures long-running database queries are cancelled if the timeout elapses.
func timeoutMiddleware(next http.Handler, timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Println("Note: .env file not found, reading from system environment variables")
	}

	host := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	user := os.Getenv("DB_USER")
	password := os.Getenv("DB_PASSWORD")
	dbname := os.Getenv("DB_NAME")

	psqlInfo := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, dbPort, user, password, dbname)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()

	err = db.Ping()
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) {
			log.Printf("PostgreSQL Error Detected:")
			log.Printf("  Code:    %s", pqErr.Code)
			log.Printf("  Message: %s", pqErr.Message)
			log.Printf("  Details: %s", pqErr.Detail)
			switch pqErr.Code {
			case "28P01":
				log.Fatalf("FATAL: Invalid password for user '%s'. Check your .env file.", user)
			case "3D000":
				log.Fatalf("FATAL: Database '%s' does not exist. Create it first.", dbname)
			default:
				log.Fatalf("FATAL: Unhandled PostgreSQL error: %v", pqErr)
			}
		} else {
			log.Fatalf("Error connecting to the database: %v", err)
		}
	}
	log.Println("Successfully connected to the PostgreSQL database.")

	store := &Store{
		repo:   &shortener.DBStore{DB: db},
		pinger: db,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/shorten", store.handleShorten)
	mux.HandleFunc("/health", store.handleHealth)
	mux.HandleFunc("/", store.handleRedirect)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	serverAddr := fmt.Sprintf(":%s", port)
	log.Printf("Starting server on %s", serverAddr)
	log.Fatal(http.ListenAndServe(serverAddr, timeoutMiddleware(mux, 10*time.Second)))
}
