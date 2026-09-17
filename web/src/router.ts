// A tiny history router over web/routes.json, the same route list the Go
// capability registry checks. The server answers every console path with
// index.html, so deep links and reloads land on the right screen.
import { computed, ref } from 'vue'

import routes from '../routes.json'

export type Screen = 'home' | 'demo' | 'server' | 'settings' | 'databases' | 'create' | 'connect' | 'browse' | 'skills' | 'not-found'

export const currentPath = ref(typeof window === 'undefined' ? '/' : window.location.pathname)

export const screen = computed<Screen>(
  () =>
    (routes.find(
      (route) =>
        route.path === currentPath.value || ('prefix' in route && route.prefix && currentPath.value.startsWith(route.path + '/')),
    )?.screen as Screen | undefined) ?? 'not-found',
)

/** Goes to path, which may carry a query (`/databases/connect?engine=mysql`); screens match the path alone. */
export function navigate(path: string) {
  const pathname = path.split('?')[0]
  if (path === currentPath.value + window.location.search) return
  window.history.pushState({}, '', path)
  currentPath.value = pathname
  window.scrollTo(0, 0)
}

if (typeof window !== 'undefined') {
  window.addEventListener('popstate', () => {
    currentPath.value = window.location.pathname
  })
}
