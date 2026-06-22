import { createFileRoute, Navigate } from '@tanstack/react-router'

export const Route = createFileRoute('/')({
  component: IndexRoute,
})

function IndexRoute() {
  return <Navigate to="/conversations" />
}

// Made with Bob
