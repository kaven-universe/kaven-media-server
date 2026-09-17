CREATE TABLE admin_settings_replacement (
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
    download_directory TEXT NOT NULL DEFAULT 'download',
    local_log_enabled INTEGER NOT NULL DEFAULT 1 CHECK (local_log_enabled IN (0, 1)),
    log_max_file_size INTEGER NOT NULL DEFAULT 10485760 CHECK (log_max_file_size BETWEEN 1048576 AND 1073741824),
    log_max_backups INTEGER NOT NULL DEFAULT 5 CHECK (log_max_backups BETWEEN 0 AND 100)
);

INSERT INTO admin_settings_replacement (
    id, remember_duration_days, public_uploads, max_file_count, max_image_file_size,
    max_hfs_file_size, allowed_domain_names, hfs_roots, bing_sync_enabled,
    bing_sync_interval_hours, upload_directory, download_directory,
    local_log_enabled, log_max_file_size, log_max_backups
)
SELECT
    id, remember_duration_days, public_uploads, max_file_count, max_image_file_size,
    max_hfs_file_size, allowed_domain_names, hfs_roots, bing_sync_enabled,
    bing_sync_interval_hours, upload_directory, download_directory,
    local_log_enabled, log_max_file_size, log_max_backups
FROM admin_settings;

DROP TABLE admin_settings;
ALTER TABLE admin_settings_replacement RENAME TO admin_settings;
