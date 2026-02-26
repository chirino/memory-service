/**
 * Site configuration loaded from environment variables.
 */

/**
 * The project version used in documentation for Maven dependencies and Docker image tags.
 * Set via PROJECT_VERSION environment variable, defaults to 999-SNAPSHOT.
 */
export const PROJECT_VERSION = import.meta.env.PROJECT_VERSION || "999-SNAPSHOT";
