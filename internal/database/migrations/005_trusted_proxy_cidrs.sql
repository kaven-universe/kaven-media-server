ALTER TABLE admin_settings
ADD COLUMN trusted_proxy_cidrs TEXT NOT NULL DEFAULT '[]';
