ALTER TABLE backend_projects ADD COLUMN start_view_json TEXT
    CHECK (start_view_json IS NULL OR json_valid(start_view_json));
