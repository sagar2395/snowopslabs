// SPDX-License-Identifier: Apache-2.0
//
// The app's single TanStack Query client and the query-key registry.
//
// Data now flows through the query cache instead of per-view useState + a
// manual refreshTick: views declare what they need with useQuery, get caching,
// retry and shared loading/error handling for free, and the app invalidates the
// cache when a run completes so every affected view refetches exactly once.

import { QueryClient } from '@tanstack/react-query'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // The labctl server is local and fast; a short stale window means tab
      // switches feel instant (served from cache) while a completed run still
      // triggers a refetch via explicit invalidation.
      staleTime: 15_000,
      // One retry smooths a transient blip without masking a real outage for
      // long — the designed error state should appear promptly when the server
      // is actually down.
      retry: 1,
      retryDelay: 800,
      // A local operator tool doesn't benefit from refetching every time the
      // window regains focus; run-completion invalidation is the real signal.
      refetchOnWindowFocus: false,
    },
  },
})

// qk is the single source of truth for query keys, so invalidation and reads
// can never drift apart on a stringly-typed key.
export const qk = {
  status: ['status'] as const,
  dashboards: ['dashboards'] as const,
  apps: ['apps'] as const,
  platform: ['platform'] as const,
  scenarios: ['scenarios'] as const,
  incidents: ['incidents'] as const,
  incidentHistory: ['incidents', 'history'] as const,
  learnPaths: ['learn', 'paths'] as const,
  challenges: ['challenges'] as const,
  challengeStatus: ['challenges', 'status'] as const,
  challengeHistory: ['challenges', 'history'] as const,
  results: ['results'] as const,
  leaderboard: ['leaderboard'] as const,
  traffic: ['traffic'] as const,
  runs: ['runs'] as const,
  run: (id: string) => ['runs', id] as const,
}
