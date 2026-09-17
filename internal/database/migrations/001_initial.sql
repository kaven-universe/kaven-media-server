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

CREATE TABLE admin_credentials (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    username TEXT NOT NULL,
    password_hash BLOB NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE admin_sessions (
    token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
    credential_key BLOB NOT NULL CHECK (length(credential_key) = 32),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE INDEX admin_sessions_expires_at_idx ON admin_sessions(expires_at);

CREATE TABLE admin_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    remember_duration_days INTEGER NOT NULL CHECK (remember_duration_days BETWEEN 1 AND 365),
    public_uploads INTEGER NOT NULL DEFAULT 1 CHECK (public_uploads IN (0, 1)),
    max_file_count INTEGER NOT NULL DEFAULT 100 CHECK (max_file_count BETWEEN 1 AND 1000),
    max_image_file_size INTEGER NOT NULL DEFAULT 104857600 CHECK (max_image_file_size BETWEEN 1048576 AND 1073741824),
    max_hfs_file_size INTEGER NOT NULL DEFAULT 1048576000 CHECK (max_hfs_file_size BETWEEN 1048576 AND 1099511627776),
    allowed_domain_names TEXT NOT NULL DEFAULT '[]',
    hfs_roots TEXT NOT NULL DEFAULT '[]',
    bing_sync_enabled INTEGER NOT NULL DEFAULT 1 CHECK (bing_sync_enabled IN (0, 1)),
    bing_sync_interval_hours INTEGER NOT NULL DEFAULT 24 CHECK (bing_sync_interval_hours BETWEEN 1 AND 8760),
    upload_directory TEXT NOT NULL DEFAULT 'upload',
    download_directory TEXT NOT NULL DEFAULT 'download'
);

INSERT INTO admin_settings(id, remember_duration_days) VALUES (1, 30);
