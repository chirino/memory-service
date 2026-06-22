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
