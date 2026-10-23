// The TODO app (spec/features/todo-demo#REQ:todo-app-behaviour and
// local-server-and-web-console#REQ:session-ended-copy): both lists from the
// demo database, add, tick and delete through the /v1 data API, changes by
// other clients within the poll, item text as text only, and the
// session-ended and stopped-server copy.
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { resetConnection } from '../src/api'
import App from '../apps/todo/App.vue'
import { newId, pollInterval } from '../apps/todo/todo'
import { installFetch, json, type Handler } from './fakeServer'

enableAutoUnmount(afterEach)

const demo = {
  schema: 1,
  app: 'todo',
  installed: true,
  database: 'todo',
  location: '/home/a/ovdb/demos/todo',
  state: 'mounted',
  app_path: '/apps/todo/',
  lists: ['/lists/to-buy', '/lists/to-watch'],
  next: [],
}

type Rec = { key: string; data: Record<string, unknown> }

let store: Record<string, Rec[]>
let nestedKeys: 'short' | 'full'

function items(list: string, ...titles: string[]): Rec[] {
  return titles.map((title, i) => ({
    key: `lists/${list}/items/${title.toLowerCase().replace(/[^a-z]/g, '')}`,
    data: { title, done: false, added_at: `2020-01-01T10:00:0${i}Z` },
  }))
}

beforeEach(() => {
  resetConnection()
  nestedKeys = 'short'
  store = {
    lists: [
      { key: 'lists/to-watch', data: { title: 'To watch' } },
      { key: 'lists/to-buy', data: { title: 'To buy' } },
    ],
    'lists/to-buy': items('to-buy', 'Milk', 'Bananas', 'Coffee').reverse(),
    'lists/to-watch': items('to-watch', 'The Matrix', 'Interstellar'),
  }
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function routes(extra: Record<string, Handler> = {}): Record<string, Handler> {
  return {
    'GET /api/local/v1/demo': () => json(200, demo),
    'POST /v1/databases/todo/query': (init) => {
      const query = JSON.parse(String(init.body)) as { collection: string; parent?: string }
      const records = query.parent ? store[query.parent] : store.lists
      // Before openvaultdb-go PR #22 nested keys come back without their parent.
      return json(200, {
        records: records.map((r) => ({ ...r, key: query.parent && nestedKeys === 'short' ? r.key.slice(query.parent.length + 1) : r.key })),
      })
    },
    ...extra,
  }
}

async function open(extra: Record<string, Handler> = {}) {
  const calls = installFetch(routes(extra))
  const wrapper = mount(App, { attachTo: document.body })
  await flushPromises()
  return { wrapper, calls }
}

const titles = (wrapper: ReturnType<typeof mount>, list: string) =>
  wrapper.findAll(`[data-list="${list}"] li label`).map((label) => label.text())

describe('TODO app', () => {
  it('shows both lists in order, items by when they were added, and where the data is stored', async () => {
    for (const keys of ['short', 'full'] as const) {
      nestedKeys = keys
      const { wrapper } = await open()
      expect(wrapper.findAll('h2').map((h) => h.text())).toEqual(['To buy', 'To watch'])
      expect(titles(wrapper, 'to-buy')).toEqual(['Milk', 'Bananas', 'Coffee'])
      expect(titles(wrapper, 'to-watch')).toEqual(['The Matrix', 'Interstellar'])
      expect(wrapper.get('[data-list="to-buy"] [data-testid="left"]').text()).toBe('3 to go')
      expect(wrapper.get('[data-testid="stored-path"]').text()).toBe('Stored in /home/a/ovdb/demos/todo on this computer')
      expect(wrapper.get('[data-testid="stored"]').text()).toContain('ovdb list /lists/to-buy/items --db todo')
      expect(wrapper.get('[data-testid="stored"] a').attributes('href')).toBe('/')
      expect(wrapper.get('[data-list="to-buy"] input[type="text"]').attributes('id')).toBe(
        wrapper.get('[data-list="to-buy"] form label').attributes('for'),
      )
      expect(wrapper.get('[data-list="to-buy"] form label').text()).toBe('Add to To buy')
      expect(document.title).toBe('TODO demo · OpenVaultDB')
      wrapper.unmount()
    }
  })

  it('renders item text as text, never HTML', async () => {
    store['lists/to-buy'].push({ key: 'lists/to-buy/items/x', data: { title: '<img src=x onerror=alert(1)>', done: false, added_at: '2020-01-01T11:00:00Z' } })
    const { wrapper } = await open()
    expect(titles(wrapper, 'to-buy')).toContain('<img src=x onerror=alert(1)>')
    expect(wrapper.find('[data-list="to-buy"] img').exists()).toBe(false)
    expect(wrapper.get('[data-item="x"] button').attributes('aria-label')).toBe('Delete <img src=x onerror=alert(1)>')
  })

  it('shows changes by other clients on the next poll and when the window regains focus', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const { wrapper } = await open()
    expect(pollInterval).toBeLessThanOrEqual(3000)
    store['lists/to-watch'].push({ key: 'lists/to-watch/items/arrival', data: { title: 'Arrival', done: false, added_at: '2020-01-01T12:00:00Z' } })
    vi.advanceTimersByTime(pollInterval)
    await flushPromises()
    expect(titles(wrapper, 'to-watch')).toEqual(['The Matrix', 'Interstellar', 'Arrival'])

    store['lists/to-buy'] = store['lists/to-buy'].filter((r) => !r.key.endsWith('coffee'))
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(titles(wrapper, 'to-buy')).toEqual(['Milk', 'Bananas'])
  })

  it('does not poll while the tab is hidden', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const { calls } = await open()
    const before = calls.length
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    vi.advanceTimersByTime(pollInterval * 3)
    await flushPromises()
    expect(calls.length).toBe(before)
  })

  it('adds, ticks and deletes items through the data API', async () => {
    const { wrapper, calls } = await open({
      'PATCH /v1/databases/todo/records/lists/to-buy/items/milk': (init) => {
        const done = JSON.parse(String(init.body)).updates[0].value as boolean
        store['lists/to-buy'][2].data.done = done
        return json(204, null)
      },
      'DELETE /v1/databases/todo/records/lists/to-buy/items/coffee': () => {
        store['lists/to-buy'] = store['lists/to-buy'].filter((r) => !r.key.endsWith('coffee'))
        return json(204, null)
      },
    })
    // Add: any new id under the list.
    const post = vi.fn()
    const fetch = globalThis.fetch as ReturnType<typeof vi.fn>
    const original = fetch.getMockImplementation()!
    fetch.mockImplementation(async (path: string, init: RequestInit = {}) => {
      const match = /^\/v1\/databases\/todo\/records\/lists\/to-buy\/items\/([a-z0-9]{6})$/.exec(path)
      if (init.method === 'POST' && match) {
        post(path, JSON.parse(String(init.body)))
        store['lists/to-buy'].push({ key: `lists/to-buy/items/${match[1]}`, data: JSON.parse(String(init.body)).data })
        return new Response('{}', { status: 201 })
      }
      return original(path, init)
    })

    const field = wrapper.get('[data-list="to-buy"] input[type="text"]')
    await field.setValue('Tea')
    await wrapper.get('[data-list="to-buy"] form').trigger('submit')
    expect(titles(wrapper, 'to-buy')).toEqual(['Milk', 'Bananas', 'Coffee', 'Tea']) // at once
    await flushPromises()
    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][1]).toEqual({ data: { title: 'Tea', done: false, added_at: expect.stringMatching(/^\d{4}-\d\d-\d\dT/) } })
    expect((field.element as HTMLInputElement).value).toBe('')

    await wrapper.get('[data-item="milk"] input').trigger('change')
    await flushPromises()
    expect(calls.some((c) => c.method === 'PATCH' && c.path.endsWith('/items/milk'))).toBe(true)
    expect((wrapper.get('[data-item="milk"] input').element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.get('[data-item="milk"] label').classes()).toContain('line-through')
    expect(wrapper.get('[data-list="to-buy"] [data-testid="left"]').text()).toBe('3 to go')

    await wrapper.get('[data-item="coffee"] button').trigger('click')
    await flushPromises()
    expect(calls.some((c) => c.method === 'DELETE' && c.path.endsWith('/items/coffee'))).toBe(true)
    expect(titles(wrapper, 'to-buy')).toEqual(['Milk', 'Bananas', 'Tea'])
  })

  it('puts a change back and says so when saving fails', async () => {
    const { wrapper } = await open({
      'DELETE /v1/databases/todo/records/lists/to-buy/items/milk': () => json(503, { error: { code: 'internal', message: 'disk full' } }),
    })
    await wrapper.get('[data-item="milk"] button').trigger('click')
    await flushPromises()
    expect(titles(wrapper, 'to-buy')).toEqual(['Milk', 'Bananas', 'Coffee'])
    expect(wrapper.get('[role="alert"]').text()).toContain("Couldn't save that change")
    expect(wrapper.get('[role="alert"]').text()).toContain('disk full')
  })

  it('shows the session-ended copy on 401, then the stopped-server copy when the server goes away', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    let answer: 'ok' | 401 | 'down' = 'ok'
    const { wrapper } = await open({
      'POST /v1/databases/todo/query': (init) =>
        answer === 401
          ? json(401, { schema: 1, error: { code: 'unauthorized', message: 'Sign in', next: [] } })
          : answer === 'down'
            ? 'network-error'
            : routes()['POST /v1/databases/todo/query'](init),
    })
    answer = 401
    vi.advanceTimersByTime(pollInterval)
    await flushPromises()
    expect(wrapper.get('[data-testid="session-ended"]').text()).toContain('Your session ended')
    expect(wrapper.get('[data-testid="session-ended"]').text()).toContain('ovdb open')
    expect(wrapper.get('[data-testid="session-ended"]').text()).toContain('To come back to your lists, run ovdb demo open.')
    expect(wrapper.find('[data-list]').exists()).toBe(false)

    answer = 'down'
    vi.advanceTimersByTime(pollInterval)
    await flushPromises()
    expect(wrapper.get('[data-testid="server-stopped"]').text()).toContain("The OVDB server isn't running")
    expect(wrapper.get('[data-testid="server-stopped"]').text()).toContain('Ask your AI assistant to start OVDB again, or run ovdb open.')
    expect(wrapper.get('[data-testid="server-stopped"]').text()).toContain('ovdb demo open')
  })

  it('says the demo was removed instead of switching to another demo database (review F6)', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    let current: typeof demo = demo
    const { wrapper } = await open({
      'GET /api/local/v1/demo': () => json(200, current),
      'POST /v1/databases/todo/query': (init) =>
        current === demo ? routes()['POST /v1/databases/todo/query'](init) : json(404, { error: { code: 'not_found', message: 'database not found: todo' } }),
      'POST /v1/databases/other/query': () => json(200, { records: [{ key: 'lists/secret', data: { title: 'Someone else' } }] }),
    })
    expect(titles(wrapper, 'to-buy')).toEqual(['Milk', 'Bananas', 'Coffee'])

    // The demo is removed and another demo database is what the server finds now.
    current = { ...demo, database: 'other', location: '/home/a/ovdb/demos/other' }
    for (let i = 0; i < 3; i++) {
      vi.advanceTimersByTime(pollInterval)
      await flushPromises()
    }
    const notice = wrapper.get('[data-testid="not-installed"]')
    expect(notice.text()).toContain('The TODO demo database was removed')
    expect(notice.text()).toContain('ovdb demo install --yes')
    expect(wrapper.text()).not.toContain('Someone else')
    expect(wrapper.find('[data-list]').exists()).toBe(false)

    // Installed again under its own id: the lists come back.
    current = demo
    vi.advanceTimersByTime(pollInterval)
    await flushPromises()
    expect(wrapper.find('[data-testid="not-installed"]').exists()).toBe(false)
    expect(titles(wrapper, 'to-buy')).toEqual(['Milk', 'Bananas', 'Coffee'])
  })

  it('says how to install the demo when it is not installed', async () => {
    const { wrapper } = await open({
      'GET /api/local/v1/demo': () => json(200, { ...demo, installed: false, database: undefined, next: [] }),
    })
    expect(wrapper.get('[data-testid="not-installed"]').text()).toContain("The TODO demo isn't installed")
    expect(wrapper.get('[data-testid="not-installed"]').text()).toContain('ovdb demo install --yes')
    expect(wrapper.get('[data-testid="not-installed"] a').attributes('href')).toBe('/demo')
  })

  it('makes short ids like the CLI', () => {
    expect(newId()).toMatch(/^[a-z0-9]{6}$/)
  })
})
