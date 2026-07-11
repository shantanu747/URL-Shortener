package shortener

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lib/pq"
)

// mockRepo is a test double for URLRepository.
// Set the function fields to control what each method returns per test.
type mockRepo struct {
	findByLongURLFunc  func(ctx context.Context, longURL string) (string, error)
	saveFunc           func(ctx context.Context, shortKey, longURL string) error
	findByShortKeyFunc func(ctx context.Context, shortKey string) (string, error)
}

func (m *mockRepo) FindByLongURL(ctx context.Context, longURL string) (string, error) {
	if m.findByLongURLFunc != nil {
		return m.findByLongURLFunc(ctx, longURL)
	}
	return "", nil
}

func (m *mockRepo) Save(ctx context.Context, shortKey, longURL string) error {
	if m.saveFunc != nil {
		return m.saveFunc(ctx, shortKey, longURL)
	}
	return nil
}

func (m *mockRepo) FindByShortKey(ctx context.Context, shortKey string) (string, error) {
	if m.findByShortKeyFunc != nil {
		return m.findByShortKeyFunc(ctx, shortKey)
	}
	return "", nil
}

// collisionError returns a pq.Error that looks like a unique constraint violation.
func collisionError() error {
	return &pq.Error{Code: "23505"}
}

// ---- ValidateLongURL ----

func TestValidateLongURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		wantErr bool
		errSnip string // substring expected in the error message
	}{
		{
			name:    "valid https URL",
			url:     "https://example.com/path?q=1",
			wantErr: false,
		},
		{
			name:    "valid http URL",
			url:     "http://example.com",
			wantErr: false,
		},
		{
			name:    "empty string",
			url:     "",
			wantErr: true,
			errSnip: "scheme",
		},
		{
			name:    "URL exceeds max length",
			url:     "https://example.com/" + strings.Repeat("a", MaxURLLength),
			wantErr: true,
			errSnip: "maximum length",
		},
		{
			name:    "ftp scheme rejected",
			url:     "ftp://example.com/file",
			wantErr: true,
			errSnip: "scheme",
		},
		{
			name:    "javascript scheme rejected",
			url:     "javascript:alert(1)",
			wantErr: true,
			errSnip: "scheme",
		},
		{
			name:    "no scheme",
			url:     "example.com/path",
			wantErr: true,
			errSnip: "scheme",
		},
		{
			name:    "localhost blocked",
			url:     "http://localhost/admin",
			wantErr: true,
			errSnip: "private",
		},
		{
			name:    "127.0.0.1 blocked",
			url:     "http://127.0.0.1:8080",
			wantErr: true,
			errSnip: "private",
		},
		{
			name:    "0.0.0.0 blocked",
			url:     "http://0.0.0.0",
			wantErr: true,
			errSnip: "private",
		},
		{
			name:    "IPv6 localhost blocked",
			url:     "http://[::1]/",
			wantErr: true,
			errSnip: "private",
		},
		{
			name:    "127.x.x.x range blocked",
			url:     "http://127.0.0.2/",
			wantErr: true,
			errSnip: "private",
		},
		{
			name:    "10.x private range blocked",
			url:     "http://10.0.0.1/internal",
			wantErr: true,
			errSnip: "private",
		},
		{
			name:    "192.168.x private range blocked",
			url:     "http://192.168.1.100/",
			wantErr: true,
			errSnip: "private",
		},
		{
			name:    "172.16.x private range blocked",
			url:     "http://172.16.0.1/",
			wantErr: true,
			errSnip: "private",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateLongURL(tc.url)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errSnip != "" && !strings.Contains(err.Error(), tc.errSnip) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errSnip)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
			}
		})
	}
}

// ---- generateShortURLKey ----

func TestGenerateShortURLKey_Length(t *testing.T) {
	t.Parallel()
	key := generateShortURLKey("https://example.com", 0)
	if len(key) != 7 {
		t.Errorf("expected key length 7, got %d (key: %q)", len(key), key)
	}
}

func TestGenerateShortURLKey_Deterministic(t *testing.T) {
	t.Parallel()
	url := "https://example.com/path"
	key1 := generateShortURLKey(url, 0)
	key2 := generateShortURLKey(url, 0)
	if key1 != key2 {
		t.Errorf("expected deterministic key, got %q and %q", key1, key2)
	}
}

func TestGenerateShortURLKey_DifferentSalts(t *testing.T) {
	t.Parallel()
	url := "https://example.com"
	key0 := generateShortURLKey(url, 0)
	key1 := generateShortURLKey(url, 1)
	key2 := generateShortURLKey(url, 2)
	if key0 == key1 || key1 == key2 || key0 == key2 {
		t.Errorf("different salts should produce different keys: %q %q %q", key0, key1, key2)
	}
}

func TestGenerateShortURLKey_URLSafeChars(t *testing.T) {
	t.Parallel()
	for salt := 0; salt < 10; salt++ {
		key := generateShortURLKey("https://example.com", salt)
		for _, ch := range key {
			isURLSafe := (ch >= 'A' && ch <= 'Z') ||
				(ch >= 'a' && ch <= 'z') ||
				(ch >= '0' && ch <= '9') ||
				ch == '-' || ch == '_' || ch == '='
			if !isURLSafe {
				t.Errorf("key %q contains non-URL-safe character %q (salt %d)", key, ch, salt)
			}
		}
	}
}

// ---- generateFullShortURL ----

func TestGenerateFullShortURL_DefaultBaseURL(t *testing.T) {
	t.Setenv("BASE_URL", "") // ensure env is unset
	result, err := generateFullShortURL("abc1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "abc1234") {
		t.Errorf("result %q does not contain short key", result)
	}
	if !strings.HasPrefix(result, "http://localhost:8080") {
		t.Errorf("expected fallback base URL, got %q", result)
	}
}

func TestGenerateFullShortURL_EnvVarBaseURL(t *testing.T) {
	t.Setenv("BASE_URL", "https://myshortener.io")
	result, err := generateFullShortURL("abc1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://myshortener.io/abc1234"
	if result != want {
		t.Errorf("expected %q, got %q", want, result)
	}
}

func TestGenerateFullShortURL_TrailingSlashHandled(t *testing.T) {
	t.Setenv("BASE_URL", "https://myshortener.io/")
	result, err := generateFullShortURL("abc1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// url.JoinPath normalises trailing slashes; key must appear exactly once
	if strings.Count(result, "abc1234") != 1 {
		t.Errorf("expected key to appear exactly once in %q", result)
	}
}

// ---- isCollisionError ----

func TestIsCollisionError_Nil(t *testing.T) {
	t.Parallel()
	if isCollisionError(nil) {
		t.Error("expected false for nil error")
	}
}

func TestIsCollisionError_NonPQError(t *testing.T) {
	t.Parallel()
	if isCollisionError(errors.New("generic error")) {
		t.Error("expected false for non-pq error")
	}
}

func TestIsCollisionError_PQNonCollision(t *testing.T) {
	t.Parallel()
	err := &pq.Error{Code: "28P01"} // wrong password
	if isCollisionError(err) {
		t.Error("expected false for non-collision pq error")
	}
}

func TestIsCollisionError_PQCollision(t *testing.T) {
	t.Parallel()
	if !isCollisionError(collisionError()) {
		t.Error("expected true for unique constraint violation")
	}
}

// ---- HandleShortURLRequest ----

func TestHandleShortURLRequest_ValidationFails(t *testing.T) {
	t.Parallel()
	repo := &mockRepo{}
	_, err := HandleShortURLRequest(context.Background(), repo, "ftp://bad.scheme.com")
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("error %q should mention 'validation failed'", err.Error())
	}
}

func TestHandleShortURLRequest_ExistingURL(t *testing.T) {
	repo := &mockRepo{
		findByLongURLFunc: func(_ context.Context, _ string) (string, error) {
			return "abc1234", nil // dedup hit
		},
	}
	t.Setenv("BASE_URL", "https://s.io")
	shortURL, err := HandleShortURLRequest(context.Background(), repo, "https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(shortURL, "abc1234") {
		t.Errorf("expected existing key in %q", shortURL)
	}
}

func TestHandleShortURLRequest_NewURL(t *testing.T) {
	saveCalled := false
	repo := &mockRepo{
		findByLongURLFunc: func(_ context.Context, _ string) (string, error) {
			return "", nil
		},
		saveFunc: func(_ context.Context, _, _ string) error {
			saveCalled = true
			return nil
		},
	}
	t.Setenv("BASE_URL", "https://s.io")
	shortURL, err := HandleShortURLRequest(context.Background(), repo, "https://example.com/new")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !saveCalled {
		t.Error("expected Save to be called for new URL")
	}
	if shortURL == "" {
		t.Error("expected non-empty short URL")
	}
}

func TestHandleShortURLRequest_CollisionRetrySuccess(t *testing.T) {
	callCount := 0
	repo := &mockRepo{
		findByLongURLFunc: func(_ context.Context, _ string) (string, error) {
			return "", nil
		},
		saveFunc: func(_ context.Context, _, _ string) error {
			callCount++
			if callCount < 3 {
				return collisionError()
			}
			return nil
		},
	}
	t.Setenv("BASE_URL", "https://s.io")
	_, err := HandleShortURLRequest(context.Background(), repo, "https://example.com")
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 Save calls, got %d", callCount)
	}
}

func TestHandleShortURLRequest_ExhaustsRetries(t *testing.T) {
	t.Parallel()
	repo := &mockRepo{
		findByLongURLFunc: func(_ context.Context, _ string) (string, error) {
			return "", nil
		},
		saveFunc: func(_ context.Context, _, _ string) error {
			return collisionError() // always collide
		},
	}
	_, err := HandleShortURLRequest(context.Background(), repo, "https://example.com")
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if !strings.Contains(err.Error(), "attempts") {
		t.Errorf("error %q should mention retry attempts", err.Error())
	}
}

func TestHandleShortURLRequest_NonCollisionDBError(t *testing.T) {
	t.Parallel()
	repo := &mockRepo{
		findByLongURLFunc: func(_ context.Context, _ string) (string, error) {
			return "", nil
		},
		saveFunc: func(_ context.Context, _, _ string) error {
			return errors.New("connection lost")
		},
	}
	_, err := HandleShortURLRequest(context.Background(), repo, "https://example.com")
	if err == nil {
		t.Fatal("expected error for non-collision DB error, got nil")
	}
}

func TestHandleShortURLRequest_LookupDBError(t *testing.T) {
	t.Parallel()
	repo := &mockRepo{
		findByLongURLFunc: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("db down")
		},
	}
	_, err := HandleShortURLRequest(context.Background(), repo, "https://example.com")
	if err == nil {
		t.Fatal("expected error for lookup failure, got nil")
	}
	if !strings.Contains(err.Error(), "database lookup failed") {
		t.Errorf("error %q should mention 'database lookup failed'", err.Error())
	}
}

// ---- HandleRedirectRequest ----

func TestHandleRedirectRequest_TooShort(t *testing.T) {
	t.Parallel()
	repo := &mockRepo{}
	_, err := HandleRedirectRequest(context.Background(), repo, "abc")
	if err == nil || !strings.Contains(err.Error(), "invalid short key length") {
		t.Errorf("expected invalid length error, got: %v", err)
	}
}

func TestHandleRedirectRequest_TooLong(t *testing.T) {
	t.Parallel()
	repo := &mockRepo{}
	_, err := HandleRedirectRequest(context.Background(), repo, "abcdefgh")
	if err == nil || !strings.Contains(err.Error(), "invalid short key length") {
		t.Errorf("expected invalid length error, got: %v", err)
	}
}

func TestHandleRedirectRequest_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mockRepo{
		findByShortKeyFunc: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("short URL not found")
		},
	}
	_, err := HandleRedirectRequest(context.Background(), repo, "abc1234")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got: %v", err)
	}
}

func TestHandleRedirectRequest_DBError(t *testing.T) {
	t.Parallel()
	repo := &mockRepo{
		findByShortKeyFunc: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("connection refused")
		},
	}
	_, err := HandleRedirectRequest(context.Background(), repo, "abc1234")
	if err == nil {
		t.Fatal("expected error for DB failure, got nil")
	}
}

func TestHandleRedirectRequest_Success(t *testing.T) {
	t.Parallel()
	want := "https://example.com/original"
	repo := &mockRepo{
		findByShortKeyFunc: func(_ context.Context, _ string) (string, error) {
			return want, nil
		},
	}
	got, err := HandleRedirectRequest(context.Background(), repo, "abc1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}
