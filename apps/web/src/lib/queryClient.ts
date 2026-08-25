import { QueryClient } from '@tanstack/react-query'

// Shared QueryClient. Living in its own module lets non-React code (e.g. the
// auth store's logout) clear the cache without circular imports.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, retry: 1 },
  },
})
