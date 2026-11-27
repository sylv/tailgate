-- a setup key only works on the setup page, which swaps it for a real key
ALTER TABLE keys ADD COLUMN is_setup INTEGER NOT NULL DEFAULT 0 CHECK (is_setup IN (0, 1));
