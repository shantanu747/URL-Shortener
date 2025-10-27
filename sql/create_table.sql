CREATE TABLE urls (
    id SERIAL PRIMARY KEY,
    short_key VARCHAR(7) UNIQUE NOT NULL,
    long_url TEXT NOT NULL,
    -- PostgreSQL (timezone-aware)
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
    click_count INTEGER DEFAULT 1
);

-- Index for fast lookups by short_key (your redirect endpoint)
CREATE INDEX idx_short_key ON urls(short_key);

-- Index for checking if long_url exists (deduplication)
CREATE INDEX idx_long_url ON urls(long_url);