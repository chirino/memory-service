-- Create Keycloak database and user
CREATE USER keycloak WITH PASSWORD 'keycloak';
CREATE DATABASE keycloak OWNER keycloak;
GRANT ALL PRIVILEGES ON DATABASE keycloak TO keycloak;

-- Create Langfuse database in the shared local Postgres instance.
CREATE DATABASE langfuse OWNER postgres;
GRANT ALL PRIVILEGES ON DATABASE langfuse TO postgres;
