-- Migration: Update allowed_domains and allowed_ips columns to JSON type
-- This migration changes the column types to JSON to properly handle string arrays

-- First, create temporary columns with JSON type
ALTER TABLE range_objects ADD COLUMN allowed_domains_json JSONB;
ALTER TABLE range_objects ADD COLUMN allowed_ips_json JSONB;

-- Convert existing data from text[] to JSON format
-- For allowed_domains: convert from text[] to JSON array
UPDATE range_objects 
SET allowed_domains_json = CASE 
    WHEN allowed_domains IS NULL THEN '[]'::jsonb
    WHEN allowed_domains = '{}' THEN '[]'::jsonb
    ELSE ('[' || array_to_json(string_to_array(trim(both '{}' from allowed_domains), ',')) || ']')::jsonb
END;

-- For allowed_ips: convert from text[] to JSON array
UPDATE range_objects 
SET allowed_ips_json = CASE 
    WHEN allowed_ips IS NULL THEN '[]'::jsonb
    WHEN allowed_ips = '{}' THEN '[]'::jsonb
    ELSE ('[' || array_to_json(string_to_array(trim(both '{}' from allowed_ips), ',')) || ']')::jsonb
END;

-- Drop the old columns
ALTER TABLE range_objects DROP COLUMN allowed_domains;
ALTER TABLE range_objects DROP COLUMN allowed_ips;

-- Rename the new columns to the original names
ALTER TABLE range_objects RENAME COLUMN allowed_domains_json TO allowed_domains;
ALTER TABLE range_objects RENAME COLUMN allowed_ips_json TO allowed_ips;

-- Add comments to document the column types
COMMENT ON COLUMN range_objects.allowed_domains IS 'JSON array of allowed domains for testing';
COMMENT ON COLUMN range_objects.allowed_ips IS 'JSON array of allowed IPs for testing';

-- Note: The application will now use GORM's JSON serializer to handle these columns
-- as []string in Go code, automatically converting to/from JSON in the database 