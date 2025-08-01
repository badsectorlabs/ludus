-- Migration: Add is_router column to vm_objects table
-- This migration adds a boolean column to identify router VMs

-- Add the is_router column with a default value of false
ALTER TABLE vm_objects ADD COLUMN is_router BOOLEAN DEFAULT FALSE;

-- Create an index on the is_router column for better query performance
CREATE INDEX idx_vm_objects_is_router ON vm_objects(is_router);

-- Note: The actual router identification will be done by the application logic
-- when VMs are updated, so no data migration is needed here 