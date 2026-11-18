-- GPUHub 0002: GPU fallback chain for jobs
ALTER TABLE jobs ADD COLUMN gpu_fallback TEXT NOT NULL DEFAULT '[]';
