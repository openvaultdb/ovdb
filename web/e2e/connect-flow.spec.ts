// Increment 6 browser journeys: the connect flow against a real ovdb binary
// (spec/features/local-server-and-web-console AC:connect-flow-needs-session
// and AC:cross-site-cookie-post-blocked). A third-party browser app on
// another origin, listed in server.cors, sends the person to /authorize,
// exchanges the code at /token and reads with the scoped token.
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { join } from 'node:path'

import { expect, test } from './fixtures'
import { asOwner, fallback, ovdb, port, primary, signedInContext } from './ovdb'

const database = 'connect'
const out = (name: string) => join(import.meta.dirname, 'screenshots', name + '.png')

interface App {
  origin: string
  server: Server
}

/** The app's one page: a Connect link, and on the way back the code exchange and a read. */
function appPage(origin: string): string {
  const connect = new URLSearchParams({
    client_id: 'e2e-app',
    redirect_uri: origin + '/callback',
    db: database,
    capabilities: 'records:read,collections:read',
    state: 'e2e-state',
  })
  return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Connect test app</title></head>
<body>
<a id="connect" href="${primary()}/authorize?${connect}">Connect to OVDB</a>
<p id="result">waiting</p>
<script>
  const params = new URLSearchParams(location.search)
  const result = document.getElementById('result')
  if (params.get('error')) result.textContent = 'denied: ' + params.get('error')
  if (params.get('code')) {
    fetch(${JSON.stringify(primary() + '/token')}, {
      method: 'POST',
      body: new URLSearchParams({ grant_type: 'authorization_code', code: params.get('code'), client_id: 'e2e-app' }),
    })
      .then((r) => r.json())
      .then((t) => fetch(${JSON.stringify(primary() + '/v1/databases/' + database)}, { headers: { Authorization: 'Bearer ' + t.access_token } }))
      .then(async (r) => { result.textContent = 'read ' + r.status + ' ' + (await r.text()) })
      .catch((e) => { result.textContent = 'failed: ' + e })
  }
</script>
</body></html>`
}

async function startApp(): Promise<App> {
  let origin = ''
  const server = createServer((_, response) => {
    response.setHeader('Content-Type', 'text/html')
    response.end(appPage(origin))
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`
  return { origin, server }
}

let app: App

test.beforeAll(async () => {
  app = await startApp()
  const listed = JSON.parse(ovdb('databases', '--json')) as { databases: { id: string }[] }
  if (!listed.databases.some((d) => d.id === database)) ovdb('databases', 'create', database, '--json')
  // server.cors applies at the next start, like the port.
  ovdb('config', 'set', 'server.cors', app.origin, '--json')
  ovdb('server', 'restart', '--json')
})

test.afterAll(() => {
  app.server.close()
  ovdb('config', 'set', 'server.cors', '', '--json')
  ovdb('server', 'restart', '--json')
})

test('without a session /authorize shows the landing page and approves nothing', async ({ browser }) => {
  const context = await browser.newContext()
  const page = await context.newPage()
  await page.goto(app.origin + '/')
  await page.locator('#connect').click()
  await expect(page.getByRole('heading', { name: 'Open the console from OVDB' })).toBeVisible()
  await expect(page.getByRole('status')).toContainText('An app asked to connect to OVDB')
  await expect(page.getByRole('button', { name: 'Allow' })).toHaveCount(0)
  await context.close()
})

test('a signed-in person approves, and the app exchanges the code and reads with its token', async ({ browser }) => {
  const context = await signedInContext(browser, { viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  await page.goto(app.origin + '/')
  await page.locator('#connect').click()

  await expect(page.getByRole('heading', { name: `Allow e2e-app to use ${database}?` })).toBeVisible()
  await expect(page.getByText('records:read', { exact: true })).toBeVisible()
  await expect(page.getByText(app.origin + '/callback', { exact: true })).toBeVisible()
  await page.screenshot({ path: out('connect-consent') })

  await page.getByRole('button', { name: 'Allow' }).click()
  await page.waitForURL((url) => url.origin === app.origin && url.pathname === '/callback')
  const back = new URL(page.url())
  expect(back.searchParams.get('state')).toBe('e2e-state')
  expect(back.searchParams.get('code')).toBeTruthy()
  await expect(page.locator('#result')).toContainText(`read 200`)
  await expect(page.locator('#result')).toContainText(`"id":"${database}"`)

  // The grant is an application token in auth.json, listed without its secret.
  const tokens = (await asOwner('/v1/tokens')) as { tokens: { principalId?: string; databaseId: string; token?: string }[] }
  expect(tokens.tokens.some((t) => t.databaseId === database && !t.token)).toBe(true)

  // Deny goes back with the error and no code.
  await page.goto(app.origin + '/')
  await page.locator('#connect').click()
  await page.getByRole('button', { name: 'Deny' }).click()
  await page.waitForURL((url) => url.origin === app.origin && url.pathname === '/callback')
  expect(new URL(page.url()).searchParams.get('code')).toBeNull()
  await expect(page.locator('#result')).toHaveText('denied: access_denied')
  await context.close()
})

test('a page on another site cannot approve with the session cookie', async ({ browser }) => {
  const context = await signedInContext(browser, {}, 'fallback')
  const evil = createServer((_, response) => {
    response.setHeader('Content-Type', 'text/html')
    response.end(`<!doctype html><form method="post" action="${fallback()}/authorize">
      <input type="hidden" name="client_id" value="evil"><input type="hidden" name="db" value="${database}">
      <input type="hidden" name="redirect_uri" value="https://evil.example/cb">
      <input type="hidden" name="capabilities" value="records:read"><input type="hidden" name="decision" value="approve">
      <button>Approve</button></form>`)
  })
  await new Promise<void>((resolve) => evil.listen(0, '127.0.0.1', resolve))
  try {
    const page = await context.newPage()
    await page.goto(`http://127.0.0.1:${(evil.address() as AddressInfo).port}/`)
    const response = page.waitForResponse(`${fallback()}/authorize`)
    await page.locator('button').click()
    const answer = await response
    expect(answer.status()).toBe(403)
    expect((await answer.request().allHeaders())['cookie']).toContain(`ovdb_session_${port()}`)
    await expect(page.getByText('This request came from another website')).toBeVisible()
    expect(page.url()).not.toContain('evil.example')
  } finally {
    evil.close()
    await context.close()
  }
})
