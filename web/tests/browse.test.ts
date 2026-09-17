// Browse data in the web console (database-context-navigation
// #REQ:browse-data-read-only, REQ:select-database-in-tui-and-web): record
// values render as text, pages hold 50 records, each view names its CLI
// command, and Use as default changes only the global default (parity E3).
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { resetConnection } from '../src/api'
import { browseRoute, display, escapeId, parseBrowseRoute, quoteArg, recordSegments, recordURL, unescapeSegment } from '../src/datapath'
import { currentPath } from '../src/router'
import BrowseScreen from '../src/screens/BrowseScreen.vue'
import { defaultRoutes, installFetch, json, type Handler } from './fakeServer'

enableAutoUnmount(afterEach)

beforeEach(() => {
  resetConnection()
  vi.stubGlobal('scrollTo', () => {})
})

afterEach(() => {
  vi.unstubAllGlobals()
})

function at(path: string) {
  window.history.replaceState({}, '', path)
  currentPath.value = path
}

const databases = {
  schema: 1,
  databases: [
    { id: 'notes', engine: 'ingitdb', state: 'mounted', manifest: '/h/databases/notes.yaml' },
    { id: 'todo', engine: 'ingitdb', state: 'mounted', manifest: '/h/databases/todo.yaml' },
  ],
  next: [],
}
const context = (global: string | null) => ({
  schema: 1,
  context: global ? { database: global, path: '/', scope: 'global' } : null,
  global: global ? { database: global, path: '/', scope: 'global' } : null,
  databases: ['notes', 'todo'],
  next: [],
})

const items = Array.from({ length: 120 }, (_, i) => ({ key: `items/r${String(i).padStart(3, '0')}`, data: { title: `record ${i}` } }))

function routes(extra: Record<string, Handler> = {}): Record<string, Handler> {
  return {
    ...defaultRoutes,
    'GET /api/local/v1/databases': () => json(200, databases),
    'GET /api/local/v1/context': () => json(200, context('todo')),
    'GET /v1/databases/notes': () => json(200, { id: 'notes', engine: 'ingitdb', schemaMode: 'schemaless', collections: ['items'] }),
    'POST /v1/databases/notes/dtql': (init) => {
      const doc = String(init.body)
      const limit = Number(/limit: (\d+)/.exec(doc)![1])
      const offset = Number(/offset: (\d+)/.exec(doc)![1])
      return json(200, { records: items.slice(offset, offset + limit) })
    },
    'POST /v1/databases/notes/query': (init) => {
      const limit = JSON.parse(String(init.body)).limit as number
      return json(200, { records: items.slice(0, limit).map((r) => ({ ...r, key: r.key.replace('items/', 'sub/') })) })
    },
    'GET /v1/databases/notes/records/items/x': () =>
      json(200, { key: 'items/x', data: { title: '<script>alert(1)</script>', note: '<img src=x onerror=alert(2)>' } }),
    'GET /v1/databases/notes/records/items/gone': () => json(404, { error: { code: 'not_found', message: 'record not found: items/gone' } }),
    ...extra,
  }
}

async function open(path: string, extra: Record<string, Handler> = {}) {
  at(path)
  const calls = installFetch(routes(extra))
  const wrapper = mount(BrowseScreen, { attachTo: document.body })
  await flushPromises()
  return { wrapper, calls }
}

describe('paths', () => {
  it('escapes ids like record.EscapeID and round-trips browse routes', () => {
    expect(escapeId('a/b.txt')).toBe('a%2Fb%2Etxt')
    expect(unescapeSegment('a%2fb%2Etxt')).toBe('a/b.txt')
    expect(unescapeSegment('50%off')).toBeNull()
    expect(display(['files', 'a/b.txt'])).toBe('/files/a%2Fb%2Etxt')
    const route = browseRoute('notes', ['files', 'a/b.txt'])
    expect(route).toBe('/browse/notes/files/a%252Fb%252Etxt')
    expect(parseBrowseRoute(route)).toEqual({ db: 'notes', segments: ['files', 'a/b.txt'] })
    expect(parseBrowseRoute('/browse/notes/files/50%25off')).toEqual({ db: 'notes', segments: null })
    expect(parseBrowseRoute('/browse')).toBeNull()
    expect(recordURL('notes', ['x', 'hello world?'])).toBe('/v1/databases/notes/records/x/hello%20world%3F')
  })

  it('refuses dot parts and control characters in decoded ids (review F1, F2)', () => {
    const esc = String.fromCharCode(0x1b)
    const bel = String.fromCharCode(0x07)
    for (const segment of [
      '%2E%2E%2F%2E%2E',
      '%2E%2E%2F%2E%2E%2Fp2',
      '%2E%2E%2F%2E%2E%2F.git%2Fhooks%2Fp4',
      '%2E%2E%2F%2E%2E%2Fsecrets%2F%24records%2Fs1',
      `c${esc}[31mol`,
      `a${esc}]0;PWNED${bel}b`,
      '%2E', '%2E%2E', 'a%2F', '%2Fa', 'a%2F%2Fb', 'nul' + String.fromCharCode(0), 'del' + String.fromCharCode(0x7f),
    ]) {
      expect(unescapeSegment(segment), segment).toBeNull()
    }
    expect(unescapeSegment('a%2Fb%2Etxt')).toBe('a/b.txt')
    expect(unescapeSegment('a..b')).toBe('a..b')
    expect(parseBrowseRoute('/browse/notes/items/%252E%252E%252F%252E%252E')).toEqual({ db: 'notes', segments: null })
  })

  it('prefers the server full nested key and composes an older short one', () => {
    const items = ['lists', 'to-buy', 'items']
    expect(recordSegments(items, 'lists/to-buy/items/milk')).toEqual(['lists', 'to-buy', 'items', 'milk'])
    expect(recordSegments(items, 'items/milk')).toEqual(['lists', 'to-buy', 'items', 'milk'])
    expect(recordSegments(items, 'lists/to-buy/items/a%2Fb')).toEqual(['lists', 'to-buy', 'items', 'a/b'])
    expect(recordSegments(items, 'items/a%2Fb')).toEqual(['lists', 'to-buy', 'items', 'a/b'])
    expect(recordSegments(['notes'], 'notes/x')).toEqual(['notes', 'x'])
  })

  it('quotes shown commands for the shell (review F7)', () => {
    expect(quoteArg('/items/a%2Fb', false)).toBe('/items/a%2Fb')
    expect(quoteArg('/items/x;rm -rf ~', false)).toBe("'/items/x;rm -rf ~'")
    expect(quoteArg("/items/it's", false)).toBe("'/items/it'\\''s'")
    expect(quoteArg("/items/it's", true)).toBe("'/items/it''s'")
  })
})

describe('Browse data', () => {
  it('lists databases with the default marked', async () => {
    const { wrapper } = await open('/browse')
    expect(wrapper.findAll('[data-browse-database]').map((a) => a.attributes('data-browse-database'))).toEqual(['notes', 'todo'])
    expect(wrapper.get('[data-browse-database="todo"]').text()).toContain('Default')
    expect(wrapper.get('[data-browse-database="notes"]').attributes('href')).toBe('/browse/notes')
  })

  it('shows collections with the list command', async () => {
    const { wrapper } = await open('/browse/notes')
    expect(wrapper.get('[data-collection="items"]').attributes('href')).toBe('/browse/notes/items')
    expect(wrapper.text()).toContain('ovdb list / --db notes')
  })

  it('pages records 50 at a time', async () => {
    const { wrapper, calls } = await open('/browse/notes/items')
    expect(wrapper.findAll('[data-record]')).toHaveLength(50)
    expect(wrapper.get('[data-testid="page"]').text()).toBe('Records 1–50')
    // Pages on the server: 51 records from the page's offset (review F8).
    expect(wrapper.text()).toContain('ovdb list /items --db notes')
    await wrapper.findAll('button').find((b) => b.text() === 'Next page')!.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find((b) => b.text() === 'Next page')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="page"]').text()).toBe('Records 101–120')
    expect(wrapper.findAll('[data-record]')).toHaveLength(20)
    expect(wrapper.findAll('button').some((b) => b.text() === 'Next page')).toBe(false)
    expect(wrapper.findAll('button').some((b) => b.text() === 'Previous page')).toBe(true)
    // Each page asks the server for 51 records from its offset (review F8).
    expect(calls.filter((c) => c.path.endsWith('/dtql')).map((c) => c.body)).toEqual([0, 50, 100].map(
      (offset) => `from: {name: "items"}\nlimit: 51\noffset: ${offset}\n`,
    ))
  })

  it('pages a nested collection through the query endpoint', async () => {
    const { wrapper, calls } = await open('/browse/notes/items/x/sub')
    expect(wrapper.findAll('[data-record]')).toHaveLength(50)
    expect(calls.find((c) => c.path.endsWith('/query'))?.body).toEqual({ collection: 'sub', limit: 51, parent: 'items/x' })
    expect(wrapper.get('[data-record="r000"]').attributes('href')).toBe('/browse/notes/items/x/sub/r000')
  })

  it('renders record values as text, never HTML (AC:browse-own-data)', async () => {
    const { wrapper } = await open('/browse/notes/items/x')
    const record = wrapper.get('[data-testid="record"]')
    expect(record.text()).toContain('"title": "<script>alert(1)</script>"')
    expect(record.element.querySelector('script, img')).toBeNull()
    expect(document.querySelector('script, img')).toBeNull()
    expect(wrapper.text()).toContain('ovdb get /items/x --db notes')
    // Read-only: nothing on the page edits the record.
    expect(wrapper.findAll('button').map((b) => b.text())).toEqual(['Use as default', 'Open'])
  })

  it('says Nothing here yet for a missing record and opens a nested collection', async () => {
    const { wrapper } = await open('/browse/notes/items/gone')
    expect(wrapper.get('[data-testid="nothing-here"]').text()).toBe('Nothing here yet')
    await wrapper.get('input').setValue('a/b')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.text()).toContain('Type one collection name, like items.')
    await wrapper.get('input').setValue('notes')
    await wrapper.get('form').trigger('submit')
    expect(currentPath.value).toBe('/browse/notes/items/gone/notes')
  })

  it('uses a database as the default for all projects only (E3)', async () => {
    const { wrapper, calls } = await open('/browse/notes', {
      'PUT /api/local/v1/context': () => json(200, { ...context('notes'), message: 'Now using notes by default for all projects' }),
    })
    await wrapper.get('[data-testid="use-as-default"]').trigger('click')
    await flushPromises()
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ scope: 'global', database: 'notes' })
    expect(wrapper.get('[role="status"]').text()).toContain('Now using notes by default for all projects')
    expect(wrapper.find('[data-testid="use-as-default"]').exists()).toBe(false)
  })

  it('shows a /v1 failure with its message', async () => {
    const { wrapper } = await open('/browse/shop', {
      'GET /v1/databases/shop': () => json(404, { error: { code: 'not_found', message: 'database not found: shop' } }),
    })
    expect(wrapper.get('[role="alert"]').text()).toContain('database not found: shop')
  })
})
