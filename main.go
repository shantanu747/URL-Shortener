package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
)

// Struct to hold our database connection
type Store struct {
	db *sql.DB
}

func (s *Store) handleShorten(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "This is the shorten endpoint. it will accept a long URL and return a short one.")
}

func (s *Store) handleRedirect(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "This is the redirect endpoint. it will accept a short URL and return a long one.")
}

func main() {
	// Load environment variables from .env file
	err := godotenv.Load()
	if err != nil {
		log.Println("Note: .env file not found, reading from system environment variables")
	}

	// Read database configuration from environment variables
	host := os.Getenv("DB_HOST")
	port := os.Getenv("DB_PORT")
	user := os.Getenv("DB_USER")
	password := os.Getenv("DB_PASSWORD")
	dbname := os.Getenv("DB_NAME")

	// Construct the connection string
	psqlInfo := fmt.Sprintf("host=%s port=%s user=%s "+
		"password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

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
}
