# chat-vecelai-ts

Full Express + Vercel AI SDK tutorial app for Memory Service.

## Local development before npm publish

The app consumes `@chirino/memory-service-vercelai` from a local `file:` dependency.

```bash
cd typescript/vercelai
npm install
npm run build

cd ../examples/vecelai/chat-vecelai-ts
npm install
npm run dev
```

After the package is published, replace the dependency source with a semver version and keep imports unchanged.
