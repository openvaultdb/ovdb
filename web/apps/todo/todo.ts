// The TODO app's data (spec/features/todo-demo#REQ:todo-app-behaviour): the
// demo database from GET /api/local/v1/demo, its lists and their items
// through the same /v1 data API the CLI and AI agents use, on the same origin
// with the console session. Paths are built from the collection asked for and
// each key's last segment, so they are right whether the server returns full
// nested keys or keys without their parent.
import { ref } from 'vue'

import { api, connection, type ApiError, type DataRecord, type DemoDocument } from '../../src/api'
import { display, keyId, keyOf, recordURL } from '../../src/datapath'

/** Changes by other clients show within 3 s while the page is visible. */
export const pollInterval = 2500

export interface Item {
  id: string
  /** Absolute path, as `ovdb get` takes it: /lists/to-buy/items/milk */
  path: string
  title: string
  done: boolean
  addedAt: string
}

export interface List {
  id: string
  path: string
  title: string
  items: Item[]
}

/** A short id like the CLI's: six lowercase letters and digits. */
export function newId(): string {
  const alphabet = '0123456789abcdefghijklmnopqrstuvwxyz'
  const bytes = crypto.getRandomValues(new Uint8Array(6))
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join('')
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : value === undefined || value === null ? '' : JSON.stringify(value)
}

// Items without a readable added_at go last.
function addedTime(item: Item) {
  const time = Date.parse(item.addedAt)
  return Number.isNaN(time) ? Number.POSITIVE_INFINITY : time
}

function byAdded(a: Item, b: Item) {
  return addedTime(a) - addedTime(b) || a.id.localeCompare(b.id)
}

export function useTodo() {
  const demo = ref<DemoDocument | null>(null)
  const lists = ref<List[]>([])
  const loaded = ref(false)
  const loadProblem = ref<ApiError | null>(null)
  const saveProblem = ref<ApiError | null>(null)

  // Every local change bumps the generation, so a poll that started before it
  // cannot put back what the person just changed.
  let generation = 0
  let inFlight = false

  const base = () => `/v1/databases/${encodeURIComponent(demo.value?.database ?? '')}`

  async function refresh() {
    if (inFlight) return
    inFlight = true
    try {
      await load()
    } finally {
      inFlight = false
    }
  }

  async function load() {
    const started = generation
    if (!demo.value?.installed) {
      const found = await api<DemoDocument>('GET', '/api/local/v1/demo')
      if (!found.ok) {
        if (connection.value === 'ok') loadProblem.value = found.error
        return
      }
      demo.value = found.data
      if (!found.data.installed) {
        loaded.value = true
        return
      }
    }
    const listRecords = await api<{ records: DataRecord[] }>('POST', base() + '/query', { collection: 'lists' })
    if (!listRecords.ok) {
      // The demo was removed meanwhile: look it up again next time.
      if (listRecords.error.message.startsWith('database not found')) demo.value = null
      if (connection.value === 'ok') loadProblem.value = listRecords.error
      return
    }
    const order = demo.value.lists
    const found = await Promise.all(
      listRecords.data.records.map(async (record): Promise<List | ApiError> => {
        const id = keyId(record.key)
        const items = await api<{ records: DataRecord[] }>('POST', base() + '/query', { collection: 'items', parent: keyOf(['lists', id]) })
        if (!items.ok) return items.error
        return {
          id,
          path: display(['lists', id]),
          title: text(record.data?.title) || id,
          items: items.data.records
            .map((item) => {
              const itemId = keyId(item.key)
              return {
                id: itemId,
                path: display(['lists', id, 'items', itemId]),
                title: text(item.data?.title),
                done: item.data?.done === true,
                addedAt: text(item.data?.added_at),
              }
            })
            .sort(byAdded),
        }
      }),
    )
    const failed = found.find((list): list is ApiError => 'code' in list)
    if (failed) {
      if (connection.value === 'ok') loadProblem.value = failed
      return
    }
    if (started !== generation) return
    const rank = (list: List) => {
      const index = order.indexOf(list.path)
      return index < 0 ? order.length : index
    }
    lists.value = (found as List[]).sort((a, b) => rank(a) - rank(b) || a.id.localeCompare(b.id))
    loadProblem.value = null
    loaded.value = true
  }

  async function write(change: () => void, request: () => ReturnType<typeof api>) {
    generation++
    saveProblem.value = null
    const before = JSON.stringify(lists.value)
    change()
    const result = await request()
    if (!result.ok) {
      generation++
      lists.value = JSON.parse(before) as List[]
      if (connection.value === 'ok') saveProblem.value = result.error
    }
    await refresh()
    return result.ok
  }

  function add(list: List, title: string) {
    const id = newId()
    const item: Item = { id, path: display(['lists', list.id, 'items', id]), title, done: false, addedAt: new Date().toISOString() }
    const segments = ['lists', list.id, 'items', id]
    return write(
      () => list.items.push(item),
      () => api('POST', recordURL(demo.value!.database!, segments), { data: { title, done: false, added_at: item.addedAt } }),
    )
  }

  function toggle(list: List, item: Item) {
    const done = !item.done
    return write(
      () => (item.done = done),
      () => api('PATCH', recordURL(demo.value!.database!, ['lists', list.id, 'items', item.id]), { updates: [{ fieldName: 'done', value: done }] }),
    )
  }

  function remove(list: List, item: Item) {
    return write(
      () => (list.items = list.items.filter((other) => other.id !== item.id)),
      () => api('DELETE', recordURL(demo.value!.database!, ['lists', list.id, 'items', item.id])),
    )
  }

  return { demo, lists, loaded, loadProblem, saveProblem, refresh, add, toggle, remove }
}
