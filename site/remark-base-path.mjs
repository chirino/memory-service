import { visit } from 'unist-util-visit';

/**
 * Remark plugin to prepend the base path to absolute links in markdown content.
 * This ensures links like [text](/docs/foo/) work correctly when deployed with a base path.
 */
export function remarkBasePath() {
  const base = process.env.ASTRO_BASE || '/';

  return (tree) => {
    visit(tree, 'link', (node) => {
      // Only transform absolute paths (starting with /) that aren't external
      if (node.url && node.url.startsWith('/') && !node.url.startsWith('//')) {
        // Prepend base path, avoiding double slashes
        const normalizedBase = base.endsWith('/') ? base.slice(0, -1) : base;
        node.url = normalizedBase + node.url;
      }
    });
  };
}
