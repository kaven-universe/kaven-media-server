CREATE TABLE IF NOT EXISTS images (
    id TEXT PRIMARY KEY,
    uuid TEXT NOT NULL UNIQUE,
    sha1 TEXT UNIQUE,
    folder TEXT NOT NULL,
    name TEXT NOT NULL,
    original_name TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    upload_date TEXT NOT NULL,
    upload_ip TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS image_cache (
    id TEXT PRIMARY KEY,
    image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
    original_url TEXT NOT NULL UNIQUE,
    folder TEXT NOT NULL,
    name TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS access_records (
    id TEXT PRIMARY KEY,
    image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
    ip TEXT NOT NULL,
    original_url TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS access_records_image_id_idx ON access_records(image_id);

CREATE TABLE IF NOT EXISTS bing_images (
    id TEXT PRIMARY KEY,
    start_date TEXT,
    full_start_date TEXT,
    end_date TEXT,
    url TEXT NOT NULL UNIQUE,
    url_base TEXT,
    copyright TEXT,
    copyright_link TEXT,
    quiz TEXT,
    wp INTEGER NOT NULL DEFAULT 0,
    hsh TEXT UNIQUE,
    drk INTEGER,
    top INTEGER,
    bot INTEGER,
    hs_json TEXT NOT NULL DEFAULT '[]',
    file TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS download_records (
    id TEXT PRIMARY KEY,
    file TEXT NOT NULL,
    ip TEXT NOT NULL,
    original_url TEXT NOT NULL,
    user_agent TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
