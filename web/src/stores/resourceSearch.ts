import { nextTick } from 'vue'
import type { LocationQueryRaw } from 'vue-router'

export interface ResourceSearchSnapshot {
  key: string
  results: any[]
  total: number
  hasMore: boolean
  offset: number
  scrollTop: number
  receivedAt: string
}

let snapshot: ResourceSearchSnapshot | null = null

export function resourceQueryKey(query: LocationQueryRaw): string {
  const params = new URLSearchParams()
  for (const key of ['q', 'channel', 'health', 'provider']) {
    const value = query[key]
    if (typeof value === 'string' && value) params.set(key, value)
  }
  if (!params.has('provider')) params.set('provider', 'all')
  return params.toString()
}

export function resourceReturnTo(fullPath: string): string {
  try {
    const url = new URL(fullPath, window.location.origin)
    if (url.origin !== window.location.origin) return '/resources'
    if (url.pathname === '/resources' || url.pathname === '/favorites') return `${url.pathname}${url.search}`
  } catch {
    // Malformed navigation state falls back to the resource index.
  }
  return '/resources'
}

export function getResourceSnapshot(key: string): ResourceSearchSnapshot | null {
  return snapshot?.key === key ? snapshot : null
}

export function setResourceSnapshot(value: ResourceSearchSnapshot | null): void {
  snapshot = value
}

export function resourceCanvas(): HTMLElement | null {
  return document.querySelector<HTMLElement>('.app-canvas')
}

export async function restoreResourceScroll(scrollTop: number): Promise<void> {
  await nextTick()
  resourceCanvas()?.scrollTo({ top: scrollTop })
}
