package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockURLRepo is a test double for shortener.URLRepository used in handler tests.
type mockURLRepo struct {
	findByLongURLFunc  func(ctx context.Context, longURL string) (string, error)
	saveFunc           func(ctx context.Context, shortKey, longURL string) error
	findByShortKeyFunc func(ctx context.Context, shortKey string) (string, error)
}

func (m *mockURLRepo) FindByLongURL(ctx context.Context, longURL string) (string, error) {
	if m.findByLongURLFunc != nil {
		return m.findByLongURLFunc(ctx, longURL)
	}
	return "", nil
}

func (m *mockURLRepo) Save(ctx context.Context, shortKey, longURL string) error {
	if m.saveFunc != nil {
		return m.saveFunc(ctx, shortKey, longURL)
	}
	return nil
}

func (m *mockURLRepo) FindByShortKey(ctx context.Context, shortKey string) (string, error) {
	if m.findByShortKeyFunc != nil {
		return m.findByShortKeyFunc(ctx, shortKey)
	}
	return "", nil
}

// mockPinger is a test double for the Pinger interface.
type mockPinger struct{ err error }

func (m *mockPinger) PingContext(_ context.Context) error { return m.err }

// newTestStore builds a Store with a mock repo and pinger for use in handler tests.
func newTestStore(repo *mockURLRepo, pingerErr error) *Store {
	return &Store{
		repo:   repo,
		pinger: &mockPinger{err: pingerErr},
	}
}

// ---- handleShorten ----

func TestHandleShorten_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	store := newTestStore(&mockURLRepo{}, nil)
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/v1/shorten", nil)
		rr := httptest.NewRecorder()
		store.handleShorten(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", method, rr.Code)
		}
	}
}

func TestHandleShorten_InvalidJSON(t *testing.T) {
	t.Parallel()
	store := newTestStore(&mockURLRepo{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shorten", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	store.handleShorten(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleShorten_EmptyLongURL(t *testing.T) {
	t.Parallel()
	store := newTestStore(&mockURLRepo{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shorten", strings.NewReader(`{"long_url":""}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	store.handleShorten(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
	var resp ShortenResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Error == "" {
		t.Error("expected error message in response body")
	}
}

func TestHandleShorten_ValidationFails_PrivateIP(t *testing.T) {
	t.Parallel()
	store := newTestStore(&mockURLRepo{}, nil)
	body := `{"long_url":"http://192.168.1.1/secret"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shorten", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	store.handleShorten(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for private IP, got %d", rr.Code)
	}
}

func TestHandleShorten_ValidationFails_BadScheme(t *testing.T) {
	t.Parallel()
	store := newTestStore(&mockURLRepo{}, nil)
	body := `{"long_url":"ftp://files.example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shorten", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	store.handleShorten(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad scheme, got %d", rr.Code)
	}
}

func TestHandleShorten_NewURL_Success(t *testing.T) {
	t.Setenv("BASE_URL", "https://s.io")
	repo := &mockURLRepo{
		findByLongURLFunc: func(_ context.Context, _ string) (string, error) { return "", nil },
		saveFunc:          func(_ context.Context, _, _ string) error { return nil },
	}
	store := newTestStore(repo, nil)
	body := `{"long_url":"https://example.com/some/path"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shorten", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	store.handleShorten(rr, req)
	if rr.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d; body: %s", rr.Code, rr.Body.String())
	}
	var resp ShortenResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.ShortURL == "" {
		t.Error("expected non-empty short_url in response")
	}
	if resp.Error != "" {
		t.Errorf("expected no error, got: %s", resp.Error)
	}
}

func TestHandleShorten_ExistingURL_ReturnsDedup(t *testing.T) {
	t.Setenv("BASE_URL", "https://s.io")
	repo := &mockURLRepo{
		findByLongURLFunc: func(_ context.Context, _ string) (string, error) {
			return "abc1234", nil // already shortened
		},
	}
	store := newTestStore(repo, nil)
	body := `{"long_url":"https://example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shorten", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	store.handleShorten(rr, req)
	if rr.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rr.Code)
	}
	var resp ShortenResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if !strings.Contains(resp.ShortURL, "abc1234") {
		t.Errorf("expected deduped key in %q", resp.ShortURL)
	}
}

func TestHandleShorten_DBError(t *testing.T) {
	t.Parallel()
	repo := &mockURLRepo{
		findByLongURLFunc: func(_ context.Context, _ string) (string, error) { return "", nil },
		saveFunc: func(_ context.Context, _, _ string) error {
			return errors.New("connection refused")
		},
	}
	store := newTestStore(repo, nil)
	body := `{"long_url":"https://example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shorten", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	store.handleShorten(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on DB error, got %d", rr.Code)
	}
}

// ---- handleRedirect ----

func TestHandleRedirect_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	store := newTestStore(&mockURLRepo{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/abc1234", nil)
	rr := httptest.NewRecorder()
	store.handleRedirect(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleRedirect_EmptyKey(t *testing.T) {
	t.Parallel()
	store := newTestStore(&mockURLRepo{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	store.handleRedirect(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for empty key, got %d", rr.Code)
	}
}

func TestHandleRedirect_InvalidChars(t *testing.T) {
	t.Parallel()
	store := newTestStore(&mockURLRepo{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/abc!@#$", nil)
	rr := httptest.NewRecorder()
	store.handleRedirect(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid chars, got %d", rr.Code)
	}
}

func TestHandleRedirect_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mockURLRepo{
		findByShortKeyFunc: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("short URL not found")
		},
	}
	store := newTestStore(repo, nil)
	req := httptest.NewRequest(http.MethodGet, "/abc1234", nil)
	rr := httptest.NewRecorder()
	store.handleRedirect(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestHandleRedirect_Success(t *testing.T) {
	t.Parallel()
	want := "https://example.com/original/path"
	repo := &mockURLRepo{
		findByShortKeyFunc: func(_ context.Context, _ string) (string, error) {
			return want, nil
		},
	}
	store := newTestStore(repo, nil)
	req := httptest.NewRequest(http.MethodGet, "/abc1234", nil)
	rr := httptest.NewRecorder()
	store.handleRedirect(rr, req)
	if rr.Code != http.StatusFound {
		t.Errorf("expected 302, got %d", rr.Code)
	}
	location := rr.Header().Get("Location")
	if location != want {
		t.Errorf("expected Location %q, got %q", want, location)
	}
}

func TestHandleRedirect_APIPrefix(t *testing.T) {
	t.Parallel()
	store := newTestStore(&mockURLRepo{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil)
	rr := httptest.NewRecorder()
	store.handleRedirect(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for /api/ prefix, got %d", rr.Code)
	}
}

// ---- handleHealth ----

func TestHandleHealth_Healthy(t *testing.T) {
	t.Parallel()
	store := newTestStore(&mockURLRepo{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	store.handleHealth(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	var body map[string]string
	json.NewDecoder(rr.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", body)
	}
}

func TestHandleHealth_Unhealthy(t *testing.T) {
	t.Parallel()
	store := newTestStore(&mockURLRepo{}, errors.New("db unreachable"))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	store.handleHealth(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}
	var body map[string]string
	json.NewDecoder(rr.Body).Decode(&body)
	if body["status"] != "unhealthy" {
		t.Errorf("expected status=unhealthy, got %v", body)
	}
}

func TestHandleHealth_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	store := newTestStore(&mockURLRepo{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rr := httptest.NewRecorder()
	store.handleHealth(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

// ---- Full end-to-end flow ----

func TestFullFlow_ShortenThenRedirect(t *testing.T) {
	t.Setenv("BASE_URL", "https://s.io")

	// Store the saved short key so redirect can look it up
	var savedShortKey string
	var savedLongURL = "https://example.com/full-flow-test"

	repo := &mockURLRepo{
		findByLongURLFunc: func(_ context.Context, _ string) (string, error) {
			return "", nil // not seen before
		},
		saveFunc: func(_ context.Context, shortKey, longURL string) error {
			savedShortKey = shortKey
			return nil
		},
		findByShortKeyFunc: func(_ context.Context, shortKey string) (string, error) {
			if shortKey == savedShortKey {
				return savedLongURL, nil
			}
			return "", errors.New("short URL not found")
		},
	}
	store := newTestStore(repo, nil)

	// Step 1: Shorten
	body := `{"long_url":"` + savedLongURL + `"}`
	shortenReq := httptest.NewRequest(http.MethodPost, "/api/v1/shorten", strings.NewReader(body))
	shortenReq.Header.Set("Content-Type", "application/json")
	shortenRR := httptest.NewRecorder()
	store.handleShorten(shortenRR, shortenReq)

	if shortenRR.Code != http.StatusCreated {
		t.Fatalf("shorten: expected 201, got %d; body: %s", shortenRR.Code, shortenRR.Body.String())
	}

	var shortenResp ShortenResponse
	json.NewDecoder(shortenRR.Body).Decode(&shortenResp)
	if shortenResp.ShortURL == "" {
		t.Fatal("shorten: expected non-empty short_url")
	}

	// Step 2: Redirect using the key extracted from the short URL
	if savedShortKey == "" {
		t.Fatal("expected savedShortKey to be set after shorten")
	}

	redirectReq := httptest.NewRequest(http.MethodGet, "/"+savedShortKey, nil)
	redirectRR := httptest.NewRecorder()
	store.handleRedirect(redirectRR, redirectReq)

	if redirectRR.Code != http.StatusFound {
		t.Errorf("redirect: expected 302, got %d", redirectRR.Code)
	}
	if redirectRR.Header().Get("Location") != savedLongURL {
		t.Errorf("redirect: expected Location %q, got %q", savedLongURL, redirectRR.Header().Get("Location"))
	}
}
