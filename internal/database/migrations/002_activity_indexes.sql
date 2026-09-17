CREATE INDEX access_records_created_at_id_idx
ON access_records(created_at DESC, id DESC);

CREATE INDEX download_records_created_at_id_idx
ON download_records(created_at DESC, id DESC);
