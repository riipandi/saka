import { createStore, useSelector } from '@tanstack/react-store'

export interface AppState {
  /** Mobile drawer visibility. */
  sidebarOpen: boolean
  /** Desktop sidebar collapse (icon-only) state. */
  sidebarCollapsed: boolean
  /** Labels of the sidebar groups the user folded. Closed, not open, is kept: a new group ships open. */
  sidebarClosedGroups: string[]
}

// ── Internal localStorage helpers ──────────────────────────────────────────

const SIDEBAR_STORAGE_KEY = 'app.sidebarCollapsed'

function getStoredSidebarCollapsed(): boolean {
  if (typeof window === 'undefined') return false
  const stored = window.localStorage.getItem(SIDEBAR_STORAGE_KEY)
  return stored === 'true'
}

const SIDEBAR_GROUPS_STORAGE_KEY = 'app.sidebarClosedGroups'

/** A stored value that is not a list of strings is a preference lost, not an error: every group opens. */
export function readStoredClosedGroups(): string[] {
  if (typeof window === 'undefined') return []
  try {
    const parsed: unknown = JSON.parse(
      window.localStorage.getItem(SIDEBAR_GROUPS_STORAGE_KEY) ?? '[]'
    )
    return Array.isArray(parsed) && parsed.every((label) => typeof label === 'string') ? parsed : []
  } catch {
    return []
  }
}

function persistSidebarCollapsed(collapsed: boolean) {
  if (typeof window === 'undefined') return
  window.localStorage.setItem(SIDEBAR_STORAGE_KEY, String(collapsed))
}

// ── Store ──────────────────────────────────────────────────────────────────

/** Hydrate from localStorage so the collapsed state survives page reloads. */
export const appStore = createStore<AppState>({
  sidebarOpen: false,
  sidebarCollapsed: getStoredSidebarCollapsed(),
  sidebarClosedGroups: readStoredClosedGroups()
})

export const toggleSidebar = () => {
  appStore.setState((prev) => ({ ...prev, sidebarOpen: !prev.sidebarOpen }))
}

export const closeSidebar = () => {
  appStore.setState((prev) => ({ ...prev, sidebarOpen: false }))
}

export const toggleSidebarCollapsed = () => {
  appStore.setState((prev) => {
    const next = !prev.sidebarCollapsed
    persistSidebarCollapsed(next)
    return { ...prev, sidebarCollapsed: next }
  })
}

export const setSidebarGroupOpen = (label: string, open: boolean) => {
  appStore.setState((prev) => {
    const closed = prev.sidebarClosedGroups.filter((name) => name !== label)
    const next = open ? closed : [...closed, label]
    window.localStorage.setItem(SIDEBAR_GROUPS_STORAGE_KEY, JSON.stringify(next))
    return { ...prev, sidebarClosedGroups: next }
  })
}

export function useSidebarClosedGroups() {
  return useSelector(appStore, (state) => state.sidebarClosedGroups)
}

export function useSidebarOpen() {
  return useSelector(appStore, (state) => state.sidebarOpen)
}

export function useSidebarCollapsed() {
  return useSelector(appStore, (state) => state.sidebarCollapsed)
}
