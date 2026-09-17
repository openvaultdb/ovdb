// Increment 1b browser smoke tests: landing, login links on both hosts,
// code reuse, framing, cross-site and cross-port posts, sign-out, session
// end, server stop, and the console's Home, OVDB server and Settings
// routes, against a real ovdb binary.
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'

import { expect, test } from './fixtures'
import { asOwner, endAllSessions, fallback, loginLink, ovdb, port, primary, signedInContext } from './ovdb'

/** Serves html on a free loopback port: another local site. */
async function localSite(html: (path: string) => string): Promise<{ url: string; server: Server }> {
  const server = createServer((request, response) => {
    response.setHeader('Content-Type', 'text/html')
    response.end(html(request.url ?? '/'))
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  return { url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`, server }
}

test('the bare address without a session shows the landing page', async ({ page }) => {
  for (const base of [primary(), fallback()]) {
    await page.goto(base + '/')
    await expect(page.getByRole('heading', { name: 'Open the console from OVDB' })).toBeVisible()
    await expect(page.getByText('ovdb open', { exact: true })).toBeVisible()
    await expect(page.getByText('ask your AI assistant', { exact: false })).toBeVisible()
    await expect(page.getByText('Running')).toHaveCount(0)
  }
  await expect(page.getByText("If ovdb.localhost didn't open, use the 127.0.0.1 link.")).toBeVisible()
})

test('a login link signs in on ovdb.localhost with a lasting session that survives a reload', async ({ browser }) => {
  const context = await browser.newContext()
  const page = await context.newPage()
  await page.goto(loginLink().url)
  await expect(page).toHaveURL(primary() + '/')
  await expect(page.getByRole('heading', { name: 'What would you like to do?' })).toBeVisible()
  await expect(page.getByTestId('status-line')).toContainText(`OVDB server running at ${primary()}`)
  await expect(page.locator('[data-option="server"]')).toContainText('Running')

  const [cookie] = (await context.cookies()).filter((c) => c.name === `ovdb_session_${port()}`)
  expect(cookie).toMatchObject({ domain: 'ovdb.localhost', httpOnly: true, sameSite: 'Lax', path: '/' })
  expect(cookie.expires).toBeGreaterThan(Date.now() / 1000 + 29 * 24 * 3600)

  await page.reload()
  await expect(page.getByRole('heading', { name: 'What would you like to do?' })).toBeVisible()
  await context.close()
})

test('the fallback link signs in on 127.0.0.1 for the browser session, and a reused code shows the landing page', async ({ browser }) => {
  const link = loginLink()
  const first = await browser.newContext()
  const page = await first.newPage()
  await page.goto(link.fallback_url)
  await expect(page).toHaveURL(fallback() + '/')
  await expect(page.getByRole('heading', { name: 'What would you like to do?' })).toBeVisible()
  const [cookie] = (await first.cookies()).filter((c) => c.name === `ovdb_session_${port()}`)
  expect(cookie).toMatchObject({ domain: '127.0.0.1', httpOnly: true, sameSite: 'Lax', expires: -1 })
  await first.close()

  const second = await browser.newContext()
  const reuse = await second.newPage()
  await reuse.goto(link.url)
  await expect(reuse.getByRole('heading', { name: 'Open the console from OVDB' })).toBeVisible()
  expect((await second.cookies()).length).toBe(0)
  await second.close()
})

test('the console refuses to be framed by another page', async ({ page, cspViolations }) => {
  // The framing page runs on a real loopback origin: Chromium's Local Network
  // Access checks would otherwise block the frame before OVDB's headers
  // could, and the test would pass for the wrong reason.
  const framer = await localSite((path) =>
    path === '/inner'
      ? '<p>control frame</p>'
      : `<iframe id="control" src="/inner"></iframe><iframe id="ovdb" src="${primary()}/"></iframe>`,
  )
  try {
    const response = page.waitForResponse(`${primary()}/`)
    await page.goto(framer.url)
    const headers = (await response).headers()
    expect(headers['x-frame-options']).toBe('DENY')
    expect(headers['content-security-policy']).toContain("frame-ancestors 'none'")
    await expect(page.frameLocator('#control').getByText('control frame')).toBeVisible()
    await expect.poll(() => page.frames().map((frame) => frame.url())).toContain('chrome-error://chromewebdata/')
    await expect(page.frameLocator('#ovdb').getByText('Open the console from OVDB')).toHaveCount(0)
    // The one expected violation: the refused frame, which also proves the
    // CSP watcher in fixtures.ts sees violations.
    await expect.poll(() => cspViolations.join('\n')).toContain('frame-ancestors')
    cspViolations.splice(0)
  } finally {
    framer.server.close()
  }
})

test('a cross-site login POST is refused by cross-origin protection and a cross-site link stays signed in', async ({ browser }) => {
  const context = await signedInContext(browser)
  const page = await context.newPage()
  const link = loginLink()
  const code = new URL(link.url).searchParams.get('code')!
  await page.route('http://evil.example/**', (route) =>
    route.fulfill({
      contentType: 'text/html',
      body: `
        <form id="login" method="post" action="${primary()}/login">
          <input type="hidden" name="code" value="${code}"><button>Sign in</button>
        </form>
        <a id="link" href="${primary()}/server">Open OVDB</a>`,
    }),
  )

  await page.goto('http://evil.example/')
  const login = page.waitForResponse(`${primary()}/login`)
  await page.locator('#login button').click()
  expect((await login).status()).toBe(403)
  await expect(page.getByText('This request came from another website')).toBeVisible()

  // The refused POST did not spend the code.
  const fresh = await browser.newContext()
  const freshPage = await fresh.newPage()
  await freshPage.goto(link.url)
  await expect(freshPage.getByRole('heading', { name: 'What would you like to do?' })).toBeVisible()
  await fresh.close()

  // A link from another site arrives signed in (SameSite=Lax).
  await page.goto('http://evil.example/')
  await page.locator('#link').click()
  await expect(page.getByRole('heading', { name: 'OVDB server' })).toBeVisible()
  await expect(page.getByTestId('address')).toHaveText(primary())
  await context.close()
})

test('a page on another 127.0.0.1 port cannot use the session cookie to mint tokens or change settings', async ({ browser }) => {
  // Same site, so the browser sends the cookie; cross-origin protection is
  // the only barrier, and it must answer before anything else does.
  const context = await signedInContext(browser, {}, 'fallback')
  const tokensBefore = JSON.stringify(await asOwner('/v1/tokens'))
  const site = await localSite(
    () => `
      <form id="tokens" method="post" enctype="text/plain" action="${fallback()}/v1/tokens">
        <input type="hidden" name='{"capabilities":["databases:create"],"x":"' value='"}'><button>Tokens</button>
      </form>
      <form id="config" method="post" enctype="text/plain" action="${fallback()}/api/local/v1/config">
        <input type="hidden" name='{"key":"server.port","value":"7999","x":"' value='"}'><button>Config</button>
      </form>`,
  )
  try {
    const page = await context.newPage()
    for (const [form, path] of [['#tokens', '/v1/tokens'], ['#config', '/api/local/v1/config']]) {
      await page.goto(site.url)
      const response = page.waitForResponse(`${fallback()}${path}`)
      await page.locator(`${form} button`).click()
      const answer = await response
      expect(answer.status()).toBe(403)
      expect(answer.request().headers()['cookie'] ?? (await answer.request().allHeaders())['cookie']).toContain(`ovdb_session_${port()}`)
      await expect(page.getByText('This request came from another website')).toBeVisible()
    }
    expect(JSON.stringify(await asOwner('/v1/tokens'))).toBe(tokensBefore)
    expect(ovdb('config', 'get', 'server.port', '--json')).not.toContain('7999')
  } finally {
    site.server.close()
    await context.close()
  }
})

test.describe('the console', () => {
  test.afterEach(() => {
    ovdb('config', 'set', 'server.port', String(port()))
  })

  test('Home, OVDB server and Settings work with the keyboard and show the server’s words', async ({ browser }) => {
    const context = await signedInContext(browser)
    const page = await context.newPage()
    await page.goto(primary() + '/')

    await page.locator('[data-option="server"]').focus()
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(primary() + '/server')
    await expect(page.getByRole('heading', { name: 'OVDB server' })).toBeFocused()
    await expect(page.getByTestId('stop-help')).toContainText('ovdb server stop')
    await expect(page.getByTestId('stop-help')).toContainText('ovdb server restart')
    await page.goBack()
    await expect(page.getByRole('heading', { name: 'What would you like to do?' })).toBeVisible()

    await page.goto(primary() + '/settings')
    const field = page.getByLabel('Port')
    await expect(field).toHaveValue(String(port()))
    await field.fill('70000')
    await page.getByRole('button', { name: 'Save port' }).click()
    await expect(page.getByText('server.port must be a port number from 1 to 65535, not "70000".')).toBeVisible()
    await expect(page.getByRole('alert')).toContainText("Couldn't use that port")
    await expect(field).toHaveAttribute('aria-invalid', 'true')

    await field.fill(String(port()))
    await field.press('Enter')
    await expect(page.getByRole('status')).toHaveText(`No change. The OVDB server is already set to use port ${port()}.`)

    const next = String(port() + 1)
    await field.fill(next)
    await field.press('Enter')
    await expect(page.getByRole('status')).toContainText(`will use port ${next} the next time it starts`)
    await expect(page.getByRole('status')).toContainText('ovdb server restart')
    expect(JSON.parse(ovdb('config', 'get', 'server.port', '--json')).config.server.port).toBe(Number(next))
    await context.close()
  })

  test('Sign out ends the session', async ({ browser }) => {
    const context = await signedInContext(browser)
    const page = await context.newPage()
    await page.goto(primary() + '/server')
    await page.getByRole('button', { name: 'Sign out' }).click()
    await expect(page).toHaveURL(primary() + '/signed-out')
    await expect(page.getByText('You signed out of the OVDB console.')).toBeVisible()
    await page.goto(primary() + '/')
    await expect(page.getByRole('heading', { name: 'Open the console from OVDB' })).toBeVisible()
    await context.close()
  })

  test('an open page shows the session-ended copy when its session is removed', async ({ browser }) => {
    const context = await signedInContext(browser)
    const page = await context.newPage()
    await page.goto(primary() + '/server')
    await expect(page.getByText('Log file')).toBeVisible()
    endAllSessions()
    await page.evaluate(() => window.dispatchEvent(new Event('focus')))
    await expect(page.getByRole('heading', { name: 'Your session ended' })).toBeVisible()
    await expect(page.getByText('Use ovdb open or ask your AI assistant for a new link.')).toBeVisible()
    await context.close()
  })

  test('an open page shows the stopped-server copy when the server stops', async ({ browser }) => {
    const context = await signedInContext(browser)
    const page = await context.newPage()
    await page.goto(primary() + '/')
    await expect(page.getByRole('heading', { name: 'What would you like to do?' })).toBeVisible()
    try {
      ovdb('server', 'stop')
      await page.evaluate(() => window.dispatchEvent(new Event('focus')))
      await expect(page.getByRole('heading', { name: "The OVDB server isn't running" })).toBeVisible()
      await expect(page.getByText('Ask your AI assistant to start OVDB again, or run ovdb open.')).toBeVisible()
    } finally {
      ovdb('server', 'start')
    }
    // The ovdb.localhost session outlives the restart.
    await page.reload()
    await expect(page.getByRole('heading', { name: 'What would you like to do?' })).toBeVisible()
    await context.close()
  })
})
