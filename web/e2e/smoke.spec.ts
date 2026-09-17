// Increment 1b browser smoke tests: landing, login links on both hosts,
// code reuse, framing, cross-site posts and the console's Home, OVDB server
// and Settings routes, against a real ovdb binary.
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'

import { expect, test } from '@playwright/test'

import { fallback, loginLink, ovdb, port, primary, signedInContext } from './ovdb'

test('the bare address without a session shows the landing page', async ({ page }) => {
  for (const base of [primary(), fallback()]) {
    await page.goto(base + '/')
    await expect(page.getByRole('heading', { name: 'Open the console from OVDB' })).toBeVisible()
    await expect(page.getByText('ovdb open', { exact: true })).toBeVisible()
    await expect(page.getByText('ask your AI assistant', { exact: false })).toBeVisible()
    await expect(page.getByText('Running at')).toHaveCount(0)
  }
  await expect(page.getByText("If ovdb.localhost didn't open, use the 127.0.0.1 link.")).toBeVisible()
})

test('a login link signs in on ovdb.localhost, and the session survives a reload', async ({ browser }) => {
  const context = await browser.newContext()
  const page = await context.newPage()
  const link = loginLink()
  await page.goto(link.url)
  await expect(page).toHaveURL(primary() + '/')
  await expect(page.getByRole('heading', { name: 'What would you like to do?' })).toBeVisible()
  await expect(page.getByTestId('status-line')).toContainText(`OVDB server running at ${primary()}`)

  const [cookie] = (await context.cookies()).filter((c) => c.name === `ovdb_session_${port()}`)
  expect(cookie).toMatchObject({ domain: 'ovdb.localhost', httpOnly: true, sameSite: 'Lax', path: '/' })

  await page.reload()
  await expect(page.getByRole('heading', { name: 'What would you like to do?' })).toBeVisible()
  await context.close()
})

test('the fallback link signs in on 127.0.0.1, and a reused code shows the landing page', async ({ browser }) => {
  const link = loginLink()
  const first = await browser.newContext()
  const page = await first.newPage()
  await page.goto(link.fallback_url)
  await expect(page).toHaveURL(fallback() + '/')
  await expect(page.getByRole('heading', { name: 'What would you like to do?' })).toBeVisible()
  await first.close()

  const second = await browser.newContext()
  const reuse = await second.newPage()
  await reuse.goto(link.url)
  await expect(reuse.getByRole('heading', { name: 'Open the console from OVDB' })).toBeVisible()
  expect((await second.cookies()).length).toBe(0)
  await second.close()
})

test('the console refuses to be framed by another page', async ({ page }) => {
  // The framing page runs on a real loopback origin: Chromium's Local Network
  // Access checks would otherwise block the frame before OVDB's headers
  // could, and the test would pass for the wrong reason.
  const framer = createServer((request, response) => {
    response.setHeader('Content-Type', 'text/html')
    response.end(
      request.url === '/inner'
        ? '<p>control frame</p>'
        : `<iframe id="control" src="/inner"></iframe><iframe id="ovdb" src="${primary()}/"></iframe>`,
    )
  })
  await new Promise<void>((resolve) => framer.listen(0, '127.0.0.1', resolve))
  const { port: framerPort } = framer.address() as AddressInfo
  try {
    const response = page.waitForResponse(`${primary()}/`)
    await page.goto(`http://localhost:${framerPort}/`)
    const headers = (await response).headers()
    expect(headers['x-frame-options']).toBe('DENY')
    expect(headers['content-security-policy']).toContain("frame-ancestors 'none'")
    await expect(page.frameLocator('#control').getByText('control frame')).toBeVisible()
    await expect
      .poll(() => page.frames().map((frame) => frame.url()))
      .toContain('chrome-error://chromewebdata/')
    await expect(page.frameLocator('#ovdb').getByText('Open the console from OVDB')).toHaveCount(0)
  } finally {
    framer.close()
  }
})

test('a cross-site form POST is rejected and a cross-site link stays signed in', async ({ browser }) => {
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
        <form id="config" method="post" enctype="text/plain" action="${primary()}/api/local/v1/config">
          <input type="hidden" name='{"key":"server.port","value":"' value='7999"}'><button>Change port</button>
        </form>
        <a id="link" href="${primary()}/server">Open OVDB</a>`,
    }),
  )

  await page.goto('http://evil.example/')
  const login = page.waitForResponse(`${primary()}/login`)
  await page.locator('#login button').click()
  expect((await login).status()).toBe(403)
  await expect(page.getByText('This request came from another website')).toBeVisible()

  await page.goto('http://evil.example/')
  const config = page.waitForResponse(`${primary()}/api/local/v1/config`)
  await page.locator('#config button').click()
  expect([401, 403]).toContain((await config).status())
  expect(ovdb('config', 'get', 'server.port', '--json')).not.toContain('7999')

  // The rejected POST did not spend the code.
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

test('Home, OVDB server and Settings work with the keyboard', async ({ browser }) => {
  const context = await signedInContext(browser)
  const page = await context.newPage()
  await page.goto(primary() + '/')

  await page.locator('[data-option="server"]').focus()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(primary() + '/server')
  await expect(page.getByRole('heading', { name: 'OVDB server' })).toBeFocused()
  await expect(page.getByTestId('stop-help')).toContainText('ovdb server stop')
  await page.goBack()
  await expect(page.getByRole('heading', { name: 'What would you like to do?' })).toBeVisible()

  await page.goto(primary() + '/settings')
  const field = page.getByLabel('Port')
  await expect(field).toHaveValue(String(port()))
  await field.fill('70000')
  await page.getByRole('button', { name: 'Save port' }).click()
  await expect(page.getByText('Enter a port number from 1 to 65535.')).toBeVisible()

  const next = String(port() + 1)
  await field.fill(next)
  await field.press('Enter')
  await expect(page.getByRole('status')).toContainText(`will use port ${next} the next time it starts`)
  await expect(page.getByRole('status')).toContainText('ovdb server restart')
  expect(JSON.parse(ovdb('config', 'get', 'server.port', '--json')).config.server.port).toBe(Number(next))
  ovdb('config', 'set', 'server.port', String(port()))
  await context.close()
})
