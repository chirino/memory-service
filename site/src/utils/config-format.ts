/**
 * Convert a Quarkus property name to its environment variable equivalent.
 * Handles quoted segments (e.g., "io.github.chirino") with double underscore boundaries.
 */
export function toEnvKey(prop: string): string {
  return prop
    .replace(/\."/g, '__')
    .replace(/"\./g, '__')
    .replace(/"/g, '')
    .replace(/[.\-]/g, '_')
    .toUpperCase();
}

/**
 * Convert a block of property-format config to environment variable format.
 * Converts key=value lines; passes through comments and blank lines unchanged.
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
