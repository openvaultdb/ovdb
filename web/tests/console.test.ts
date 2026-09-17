import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { connection } from '../src/api'
import App from '../src/App.vue'
import { currentPath } from '../src/router'
import HomeScreen from '../src/screens/HomeScreen.vue'
import ServerScreen from '../src/screens/ServerScreen.vue'
import SettingsScreen from '../src/screens/SettingsScreen.vue'
import { defaultRoutes, installFetch, json, server } from './fakeServer'

enableAutoUnmount(afterEach)

beforeEach(() => {
  connection.value = 'ok'
  window.history.replaceState({}, '', '/')
  currentPath.value = '/'
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('Home', () => {
  it('shows the status line, the question and only the implemented options in order', async () => {
    installFetch(defaultRoutes)
    const wrapper = mount(HomeScreen)
    await flushPromises()

    expect(wrapper.get('[data-testid="status-line"]').text()).toBe('OVDB server running at http://ovdb.localhost:6832')
    expect(wrapper.get('h1').text()).toBe('What would you like to do?')
    const options = wrapper.findAll('[data-option]').map((option) => option.attributes('data-option'))
    expect(options).toEqual(['server', 'settings'])
    const serverOption = wrapper.get('[data-option="server"]')
    expect(serverOption.text()).toContain('OVDB server')
    expect(serverOption.text()).toContain('Running')
    expect(serverOption.attributes('href')).toBe('/server')
    // Parity exception E1: the web console never offers to start the server.
    expect(wrapper.text()).not.toContain('Start the OVDB server')
  })

  it('navigates without reloading and moves between options with the arrow keys', async () => {
    installFetch(defaultRoutes)
    const wrapper = mount(HomeScreen, { attachTo: document.body })
    await flushPromises()
    const link = wrapper.get('[data-option="server"]')
    await link.trigger('click', { button: 0 })
    expect(currentPath.value).toBe('/server')
    expect(window.location.pathname).toBe('/server')

    ;(link.element as HTMLElement).focus()
    await link.trigger('keydown', { key: 'ArrowDown' })
    expect(document.activeElement).toBe(link.element) // the only primary option wraps to itself
    wrapper.unmount()
  })
})

describe('OVDB server panel', () => {
  it('shows state, both addresses, version, start and log, and how to stop it from a terminal', async () => {
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
})

describe('Settings', () => {
  it('shows the running port and saves a new one for the next start', async () => {
    const calls = installFetch({
      ...defaultRoutes,
      'PUT /api/local/v1/config': () =>
        json(200, {
          schema: 1,
          config: { server: { port: 7000 } },
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
  })

  it('rejects a port that is not a number from 1 to 65535 before calling the server', async () => {
    const calls = installFetch(defaultRoutes)
    const wrapper = mount(SettingsScreen)
    await flushPromises()
    for (const bad of ['0', '65536', 'abc', '']) {
      await wrapper.get('input').setValue(bad)
      await wrapper.get('form').trigger('submit')
      await flushPromises()
      expect(wrapper.text()).toContain('Enter a port number from 1 to 65535.')
      expect(wrapper.get('input').attributes('aria-invalid')).toBe('true')
    }
    expect(calls.filter((call) => call.method === 'PUT')).toHaveLength(0)
  })

  it('shows the server’s reason when it refuses the value', async () => {
    installFetch({
      ...defaultRoutes,
      'PUT /api/local/v1/config': () =>
        json(400, { schema: 1, error: { code: 'invalid_argument', message: "Couldn't use that port", reason: 'Port 80 is reserved.', next: [] } }),
    })
    const wrapper = mount(SettingsScreen)
    await flushPromises()
    await wrapper.get('input').setValue('80')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('Port 80 is reserved.')
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
    expect(wrapper.get('[data-testid="session-ended"] [role="status"]').text()).toBe(
      'Your session ended — use ovdb open or ask your AI assistant for a new link',
    )
    expect(wrapper.get('[data-testid="session-ended"] code').text()).toBe('ovdb open')
    wrapper.unmount()
  })

  it('shows the stopped-server copy when the server cannot be reached', async () => {
    installFetch({ 'GET /api/local/v1/server': () => 'network-error', 'GET /api/local/v1/status': () => 'network-error' })
    const wrapper = mount(App)
    await flushPromises()
    const stopped = wrapper.get('[data-testid="server-stopped"]').text()
    expect(stopped).toContain("The OVDB server isn't running")
    expect(stopped).toContain('Ask your AI assistant to start OVDB again, or run ovdb open.')
    wrapper.unmount()
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
