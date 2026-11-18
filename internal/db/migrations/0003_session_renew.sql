-- GPUHub 0003: session_renew flag on jobs
ALTER TABLE jobs ADD COLUMN session_renew INTEGER NOT NULL DEFAULT 0;
