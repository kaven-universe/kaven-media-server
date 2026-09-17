ALTER TABLE admin_settings
ADD COLUMN local_log_enabled INTEGER NOT NULL DEFAULT 1 CHECK (local_log_enabled IN (0, 1));

ALTER TABLE admin_settings
ADD COLUMN log_max_file_size INTEGER NOT NULL DEFAULT 10485760 CHECK (log_max_file_size BETWEEN 1048576 AND 1073741824);

ALTER TABLE admin_settings
ADD COLUMN log_max_backups INTEGER NOT NULL DEFAULT 5 CHECK (log_max_backups BETWEEN 1 AND 100);
