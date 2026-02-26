/**
 * Convert a CLI flag name to its environment variable equivalent.
 * e.g. "--db-url" → "MEMORY_SERVICE_DB_URL"
 *      "--port"   → "MEMORY_SERVICE_PORT"
 */
export function toEnvKey(flag: string): string {
  const base = flag
    .replace(/^--/, '')
    .replace(/-/g, '_')
    .toUpperCase();
  return 'MEMORY_SERVICE_' + base;
}

/**
 * Convert a block of CLI-flag-format config to environment variable format.
 * Converts --flag=value lines; passes through comments and blank lines unchanged.
 */
export function toEnvBlock(text: string): string {
  return text.split('\n').map(line => {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) return line;
    const eqIdx = line.indexOf('=');
    if (eqIdx <= 0) return line;
    const key = line.substring(0, eqIdx);
    const value = line.substring(eqIdx + 1);
    return toEnvKey(key) + '=' + value;
  }).join('\n');
}
