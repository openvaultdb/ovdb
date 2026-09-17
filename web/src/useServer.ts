// Shared, polled server document: the header badge, Home's status line and
// the OVDB server panel all read the same one. Polling is what shows the
// stopped-server copy when the server goes away while the page is open.
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { api, type Server, type ServerDocument } from './api'

const server = ref<Server | null>(null)
const loading = ref(true)
let subscribers = 0
let timer: ReturnType<typeof setInterval> | undefined

export const pollInterval = 10_000

export async function refreshServer() {
  const result = await api<ServerDocument>('GET', '/api/local/v1/server')
  if (result.ok) server.value = result.data.server
  loading.value = false
}

function onFocus() {
  void refreshServer()
}

export function useServer() {
  onMounted(() => {
    if (subscribers++ === 0) {
      void refreshServer()
      timer = setInterval(refreshServer, pollInterval)
      window.addEventListener('focus', onFocus)
    }
  })
  onBeforeUnmount(() => {
    if (--subscribers === 0) {
      clearInterval(timer)
      window.removeEventListener('focus', onFocus)
    }
  })
  return { server, loading }
}
