import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { resetConnection } from '../src/api'
import App from '../src/App.vue'
import { currentPath } from '../src/router'
import HomeScreen from '../src/screens/HomeScreen.vue'
import ServerScreen from '../src/screens/ServerScreen.vue'
import SettingsScreen from '../src/screens/SettingsScreen.vue'
import { defaultRoutes, home, installFetch, json, server } from './fakeServer'

enableAutoUnmount(afterEach)

beforeEach(() => {
  resetConnection()
  window.history.replaceState({}, '', '/')
  currentPath.value = '/'
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('Home', () => {
  it('renders the status line, the question and the options the server sends, in its order', async () => {
    installFetch(defaultRoutes)
    const wrapper = mount(HomeScreen)
    await flushPromises()

    expect(wrapper.get('[data-testid="status-line"]').text()).toBe('OVDB server running at http://ovdb.localhost:6832 · Databases: none')
    expect(wrapper.get('h1').text()).toBe('What would you like to do?')
    const options = wrapper.findAll('[data-option]').map((option) => option.attributes('data-option'))
    expect(options).toEqual(['create', 'server', 'settings'])
    expect(wrapper.get('[data-option="create"]').attributes('href')).toBe('/databases/new')
    const serverOption = wrapper.get('[data-option="server"]')
    expect(serverOption.text()).toContain('OVDB server')
    expect(serverOption.text()).toContain('Running')
    expect(serverOption.attributes('href')).toBe('/server')
    // Parity exception E1: the web wording replaces "Start the OVDB server".
    expect(wrapper.text()).not.toContain('Start the OVDB server')
  })

  it('shows the badge and status line for the state the server reports', async () => {
    installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/home': () =>
        json(200, {
          ...home,
          status_line: [{ key: 'home.status.server_not_running' }],
          options: [{ ...home.options[1], badge: { tone: 'neutral', label_key: 'server.badge.not_running' } }],
        }),
    })
    const wrapper = mount(HomeScreen)
    await flushPromises()
    expect(wrapper.get('[data-testid="status-line"]').text()).toBe('OVDB server not running')
    expect(wrapper.get('[data-option="server"]').text()).toContain('Not running')
    expect(wrapper.findAll('[data-option]')).toHaveLength(1)
  })

  it('navigates without reloading and moves between options with the arrow keys', async () => {
    installFetch(defaultRoutes)
    const wrapper = mount(HomeScreen, { attachTo: document.body })
    await flushPromises()
    const link = wrapper.get('[data-option="server"]')
    const create = wrapper.get('[data-option="create"]')
    await link.trigger('click', { button: 0 })
    expect(currentPath.value).toBe('/server')
    expect(window.location.pathname).toBe('/server')

    ;(link.element as HTMLElement).focus()
    await link.trigger('keydown', { key: 'ArrowDown' })
    expect(document.activeElement).toBe(create.element) // the last primary option wraps to the first
    wrapper.unmount()
  })
})

describe('OVDB server panel', () => {
  it('shows state, both addresses, version, start and log, and the server’s stop and restart commands', async () => {
    installFetch(defaultRoutes)
    const wrapper = mount(ServerScreen)
    await flushPromises()
    const text = wrapper.text()
    for (const want of ['OVDB server', 'Running', server.address, 'Also at http://127.0.0.1:6832', '1.2.3', server.log]) {
      expect(text).toContain(want)
    }
    const help = wrapper.get('[data-testid="stop-help"]')
    expect(help.text()).toContain('ovdb server stop')
    expect(help.text()).toContain('ovdb server restart')
    // Parity exception E2: no stop or restart button.
    expect(wrapper.findAll('button')).toHaveLength(0)
  })

  it('shows the state the server reports', async () => {
    installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/server': () => json(200, { schema: 1, server: { ...server, state: 'stopping' }, next: [] }),
    })
    const wrapper = mount(ServerScreen)
    await flushPromises()
    expect(wrapper.text()).toContain('Stopping')
    expect(wrapper.find('[data-testid="stop-help"]').exists()).toBe(false)
  })
})

describe('Settings', () => {
  it('shows the running port and saves a new one for the next start', async () => {
    const calls = installFetch({
      ...defaultRoutes,
      'PUT /api/local/v1/config': () =>
        json(200, {
          schema: 1,
          config: { server: { port: 7000 } },
          changed: true,
          next: [{ label: 'Restart the OVDB server to use it', command: 'ovdb server restart' }],
        }),
    })
    const wrapper = mount(SettingsScreen)
    await flushPromises()
    const input = wrapper.get('input')
    expect((input.element as HTMLInputElement).value).toBe('6832')
    expect(wrapper.text()).toContain('The OVDB server is using port 6832 now.')
    expect(wrapper.get('label').attributes('for')).toBe(input.attributes('id'))

    await input.setValue('7000')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(calls.at(-1)).toEqual({ method: 'PUT', path: '/api/local/v1/config', body: { key: 'server.port', value: '7000' } })
    const notice = wrapper.get('[role="status"]')
    expect(notice.text()).toContain('Saved. The OVDB server will use port 7000 the next time it starts.')
    expect(notice.text()).toContain('ovdb server restart')
    expect(notice.text()).toContain('After the restart this page stops working.')
  })

  it('says "No change" without a restart when the port is already set', async () => {
    installFetch({
      ...defaultRoutes,
      'PUT /api/local/v1/config': () => json(200, { schema: 1, config: { server: { port: 6832 } }, changed: false, next: [] }),
    })
    const wrapper = mount(SettingsScreen)
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    const notice = wrapper.get('[role="status"]')
    expect(notice.text()).toBe('No change. The OVDB server is already set to use port 6832.')
    expect(wrapper.text()).not.toContain('restart')
  })

  it('sends the value as typed and renders the server’s message, reason and next', async () => {
    const calls = installFetch({
      ...defaultRoutes,
      'PUT /api/local/v1/config': () =>
        json(400, {
          schema: 1,
          error: {
            code: 'invalid_argument',
            message: "Couldn't use that port",
            reason: 'server.port must be a port number from 1 to 65535, not "abc".',
            next: [{ label: 'Show the current setting', command: 'ovdb config get server.port' }],
          },
        }),
    })
    const wrapper = mount(SettingsScreen)
    await flushPromises()
    await wrapper.get('input').setValue('abc')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(calls.at(-1)?.body).toEqual({ key: 'server.port', value: 'abc' })
    expect(wrapper.get('input').attributes('aria-invalid')).toBe('true')
    expect(wrapper.text()).toContain('server.port must be a port number from 1 to 65535, not "abc".')
    expect(wrapper.get('[role="alert"]').text()).toContain("Couldn't use that port")
    expect(wrapper.get('[role="alert"]').text()).toContain('ovdb config get server.port')
  })
})

describe('console shell', () => {
  it('routes by path and titles the page', async () => {
    installFetch(defaultRoutes)
    window.history.replaceState({}, '', '/settings')
    currentPath.value = '/settings'
    const wrapper = mount(App)
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('Settings')
    expect(document.title).toBe('Settings · OpenVaultDB')
    wrapper.unmount()
  })

  it('shows the session-ended copy when the server answers 401', async () => {
    installFetch({
      'GET /api/local/v1/server': () => json(401, { schema: 1, error: { code: 'unauthorized', message: 'Sign in first.', next: [] } }),
      'GET /api/local/v1/status': () => json(401, { schema: 1, error: { code: 'unauthorized', message: 'Sign in first.', next: [] } }),
    })
    const wrapper = mount(App)
    await flushPromises()
    const ended = wrapper.get('[data-testid="session-ended"]')
    expect(ended.get('h1').text()).toBe('Your session ended')
    expect(ended.text()).toContain('Use ovdb open or ask your AI assistant for a new link.')
    expect(ended.get('code').text()).toBe('ovdb open')
    expect(wrapper.find('form[action="/logout"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows the stopped-server copy when the server cannot be reached', async () => {
    installFetch({ 'GET /api/local/v1/server': () => 'network-error', 'GET /api/local/v1/status': () => 'network-error' })
    const wrapper = mount(App)
    await flushPromises()
    const stopped = wrapper.get('[data-testid="server-stopped"]').text()
    expect(wrapper.get('[data-testid="server-stopped"] h1').text()).toBe("The OVDB server isn't running")
    expect(stopped).toContain('Ask your AI assistant to start OVDB again, or run ovdb open.')
    wrapper.unmount()
  })

  it('asks for a new link, not "stopped", when the server moved to a saved port', async () => {
    let up = true
    const calls = installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/server': () => (up ? json(200, { schema: 1, server, next: [] }) : 'network-error'),
      'PUT /api/local/v1/config': () => json(200, { schema: 1, config: { server: { port: 7000 } }, changed: true, next: [] }),
    })
    window.history.replaceState({}, '', '/settings')
    currentPath.value = '/settings'
    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('input').setValue('7000')
    await wrapper.get('main form').trigger('submit')
    await flushPromises()
    expect(calls.some((call) => call.method === 'PUT')).toBe(true)
    up = false
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(wrapper.get('[data-testid="session-ended"] h1').text()).toBe('Your session ended')
  })

  it('offers Sign out as a same-origin form POST', async () => {
    installFetch(defaultRoutes)
    const wrapper = mount(App)
    await flushPromises()
    const form = wrapper.get('form[action="/logout"]')
    expect(form.attributes('method')).toBe('post')
    expect(form.get('button').text()).toBe('Sign out')
  })

  it('shows a not-found screen for unknown paths', async () => {
    installFetch(defaultRoutes)
    window.history.replaceState({}, '', '/nope')
    currentPath.value = '/nope'
    const wrapper = mount(App)
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe("There's nothing at this address.")
    wrapper.unmount()
  })
})
