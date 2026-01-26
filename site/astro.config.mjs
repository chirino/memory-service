import { defineConfig } from 'astro/config';
import mdx from '@astrojs/mdx';
import tailwindcss from '@tailwindcss/vite';
import { remarkBasePath } from './remark-base-path.mjs';

// Get base path from environment variable
// For main branch: ASTRO_BASE=/memory-service/
// For tags: ASTRO_BASE=/memory-service/docs/v1.0.0/
const base = process.env.ASTRO_BASE || '/';

// Get site URL (for canonical URLs, sitemaps, etc.)
const site = process.env.ASTRO_SITE || 'https://chirino.github.io';

// Get project version from environment variable (default: 999-SNAPSHOT)
const projectVersion = process.env.PROJECT_VERSION || '999-SNAPSHOT';

export default defineConfig({
  site,
  base,
  integrations: [mdx()],
  vite: {
    plugins: [tailwindcss()],
    define: {
      'import.meta.env.PROJECT_VERSION': JSON.stringify(projectVersion),
    },
  },
  markdown: {
    remarkPlugins: [remarkBasePath],
    shikiConfig: {
      theme: 'github-dark',
      wrap: true,
    },
  },
  build: {
    format: 'directory',
  },
});
