# Memory Service Documentation Site

This directory contains the Astro-based static site for Memory Service documentation and marketing pages.

## Technology Stack

- **[Astro](https://astro.build/)** - Static site generator
- **[Tailwind CSS v4](https://tailwindcss.com/)** - Utility-first CSS framework
- **[MDX](https://mdxjs.com/)** - Markdown with JSX support for docs

## Local Development

### Prerequisites

- Node.js 20+ 
- npm (or pnpm/yarn)

### Getting Started

```bash
# Install dependencies
npm install

# Start development server (http://localhost:4321)
npm run dev

# Build the site
npm run build

# Preview the built site
npm run preview
```

### Available Scripts

| Command | Description |
|---------|-------------|
| `npm run dev` | Start the development server with hot reload |
| `npm run build` | Build the production site to `./dist` |
| `npm run preview` | Preview the production build locally |

## Project Structure

```
site/
├── public/              # Static assets (favicon, images)
├── src/
│   ├── components/      # Astro components (Header, Footer, Sidebar, etc.)
│   ├── data/            # Data files (versions.json)
│   ├── layouts/         # Page layouts (BaseLayout, DocsLayout)
│   ├── pages/           # File-based routing
│   │   ├── index.astro  # Home page
│   │   └── docs/        # Documentation pages
│   └── styles/          # Global CSS (Tailwind)
├── astro.config.mjs     # Astro configuration
├── package.json
└── tsconfig.json
```

## Adding Documentation Pages

### Markdown (.md)

Create a new `.md` file in `src/pages/docs/`:

```markdown
---
layout: ../../layouts/DocsLayout.astro
title: My New Page
description: Page description for SEO
---

# My New Page

Content goes here...
```

### MDX (.mdx)

For pages that need interactive components or JSX:

```mdx
---
layout: ../../layouts/DocsLayout.astro
title: My Interactive Page
description: Page with components
---

import { Code } from 'astro:components';

# My Interactive Page

<div class="card p-4">
  Custom JSX content here
</div>
```

### Updating the Sidebar

Edit `src/components/DocsSidebar.astro` to add new pages to the navigation:

```typescript
const navigation: NavSection[] = [
  {
    title: 'Section Name',
    items: [
      { label: 'Page Label', href: '/docs/your-page/' },
    ],
  },
];
```

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `ASTRO_BASE` | Base path for the site | `/` |
| `ASTRO_SITE` | Full site URL | `https://chirino.github.io` |

The base path is used for all internal links and assets. It's configured automatically by the GitHub Actions workflows:

- **Main branch**: `ASTRO_BASE=/memory-service/`
- **Tagged releases**: `ASTRO_BASE=/memory-service/docs/v1.0.0/`

### Building for Different Environments

```bash
# Build for local preview (no base path)
npm run build

# Build for GitHub Pages main site
ASTRO_BASE=/memory-service/ npm run build

# Build for a specific version
ASTRO_BASE=/memory-service/docs/v1.0.0/ npm run build
```

## Versioned Documentation

The site supports versioned documentation for each release tag.

### How It Works

1. **Main branch** (`main`) deploys to the root: `https://chirino.github.io/memory-service/`
2. **Git tags** (`v*.*.*`) deploy to versioned paths: `https://chirino.github.io/memory-service/docs/v1.0.0/`

### GitHub Actions Workflows

- `.github/workflows/pages-main.yml` - Deploys on push to `main`
- `.github/workflows/pages-tag.yml` - Deploys on push of version tags

### Creating a New Version

1. Tag a release:
   ```bash
   git tag v1.0.0
   git push origin v1.0.0
   ```

2. The GitHub Action automatically:
   - Builds the site at that tag with the versioned base path
   - Deploys to `/docs/v1.0.0/` on GitHub Pages
   - Updates `versions.json` with the new version

### Version Selector

The `VersionSelector` component reads from `src/data/versions.json` to display available versions. The GitHub Actions workflow updates this file automatically when deploying tags.

## Styling

All styling uses Tailwind CSS v4. Custom styles are in `src/styles/global.css`:

- **Theme colors**: Custom brand colors (primary, accent, surface)
- **Component classes**: `.btn`, `.card`, `.sidebar-link`, etc.
- **Typography**: Prose styles for markdown content

### Adding Custom Styles

```css
/* In src/styles/global.css */
@layer components {
  .my-custom-class {
    @apply px-4 py-2 bg-primary-600 text-white rounded-lg;
  }
}
```

## Deployment

### GitHub Pages Setup

1. Go to repository Settings → Pages
2. Set Source to "Deploy from a branch"
3. Select `gh-pages` branch and `/ (root)` folder
4. Save

The workflows will create the `gh-pages` branch automatically on first deploy.

### Manual Deployment

For manual deployment (not recommended for production):

```bash
# Build the site
ASTRO_BASE=/memory-service/ npm run build

# The built files are in ./dist
# Deploy this folder to your hosting
```

## Troubleshooting

### Build Errors

- **Missing dependencies**: Run `npm ci` to ensure clean install
- **Node version**: Ensure Node.js 20+ is installed
- **TypeScript errors**: Check `tsconfig.json` extends Astro's config

### Broken Links/Assets

- Ensure all internal links use the `basePath` variable
- Use `import.meta.env.BASE_URL` for asset URLs
- Check that `ASTRO_BASE` ends with a trailing slash

### Version Selector Not Updating

- Check that `versions.json` exists in `src/data/`
- Verify the GitHub Actions workflow completed successfully
- Check the `gh-pages` branch for `versions.json`
