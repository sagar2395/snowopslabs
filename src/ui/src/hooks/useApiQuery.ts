// SPDX-License-Identifier: Apache-2.0
import { useQuery, type QueryKey } from '@tanstack/react-query'
import { errMessage } from '../lib/errors'

/** The view-facing shape of a cached read: the data plus the loading/error
 *  flags the designed states render, and a zero-arg `reload` for retry/refresh
 *  buttons. It mirrors the fields views used to hand-roll with useState so a
 *  view migrates by swapping its state block for one call. */
export interface ApiQuery<T> {
  data: T | undefined
  /** True only on the very first load (no cached data yet). */
  loading: boolean
  /** True once any successful response has populated the cache. */
  loaded: boolean
  /** A human message when the last fetch failed, else null. */
  loadError: string | null
  /** True while a fetch is in flight, including background refetches. */
  refreshing: boolean
  /** Trigger a refetch (retry / manual refresh). */
  reload: () => void
}

/** useApiQuery wraps TanStack Query in the view-facing ApiQuery shape, so every
 *  view gets identical caching, retry and loading/error semantics. Run
 *  completion invalidates the cache app-wide (see App.tsx), which reruns these. */
export interface ApiQueryOptions<T> {
  /** Transform the cached data before it reaches the view (e.g. sort). */
  select?: (data: T) => T
  /** Poll every N ms. TanStack pauses this while the tab is hidden, matching
   *  the old manual "skip when document.hidden" interval. */
  refetchInterval?: number
}

export function useApiQuery<T>(key: QueryKey, queryFn: () => Promise<T>, options?: ApiQueryOptions<T>): ApiQuery<T> {
  const q = useQuery({ queryKey: key, queryFn, select: options?.select, refetchInterval: options?.refetchInterval })
  return {
    data: q.data,
    loading: q.isPending,
    loaded: q.data !== undefined,
    loadError: q.isError ? errMessage(q.error) : null,
    refreshing: q.isFetching,
    reload: () => { void q.refetch() },
  }
}
