import { defineConfig } from '@hey-api/openapi-ts'

export default defineConfig({
  client: '@hey-api/client-fetch',
  input: '../../contracts/openapi/openapi-admin.yml',
  output: {
    path: './src/api/generated',
    format: 'prettier',
  },
  plugins: [
    '@hey-api/typescript',
    '@hey-api/sdk',
    {
      name: '@tanstack/react-query',
      // Admin SSE operations (adminEvict, adminSubscribeEvents) use client.sse.*,
      // so no React Query hooks are generated for them; call the raw SDK
      // functions for streaming instead.
      '~hooks': {
        operations: {
          isMutation: (operation) => {
            if (operation.id === 'adminEvict') {
              return false
            }
            return undefined
          },
          isQuery: (operation) => {
            if (operation.id === 'adminSubscribeEvents') {
              return false
            }
            return undefined
          },
        },
      },
    },
  ],
})

// Made with Bob
