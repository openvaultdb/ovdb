// A tiny history router over web/routes.json, the same route list the Go
// capability registry checks. The server answers every console path with
// index.html, so deep links and reloads land on the right screen.
import { computed, ref } from 'vue'

import routes from '../routes.json'

export type Screen = 'home' | 'server' | 'settings' | 'databases' | 'create' | 'not-found'

export const currentPath = ref(typeof window === 'undefined' ? '/' : window.location.pathname)

export const screen = computed<Screen>(
  () => (routes.find((route) => route.path === currentPath.value)?.screen as Screen | undefined) ?? 'not-found',
)

export function navigate(path: string) {
  if (path === currentPath.value) return
  window.history.pushState({}, '', path)
  currentPath.value = path
  window.scrollTo(0, 0)
}

if (typeof window !== 'undefined') {
  window.addEventListener('popstate', () => {
    currentPath.value = window.location.pathname
  })
}
