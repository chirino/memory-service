CREATE DATABASE IF NOT EXISTS memory_service_analytics;
CREATE USER IF NOT EXISTS memory_service_analytics IDENTIFIED WITH plaintext_password BY 'memory-service-analytics';
GRANT CREATE DATABASE, CREATE TABLE, DROP TABLE, CREATE VIEW, DROP VIEW, SELECT, INSERT, ALTER ON memory_service_analytics.* TO memory_service_analytics;
GRANT SELECT ON system.merge_tree_settings TO memory_service_analytics;
