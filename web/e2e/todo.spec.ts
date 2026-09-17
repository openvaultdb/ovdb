// Increment 4 browser journeys: Try a demo in the web console with the
// keyboard only and the TODO app against a real ovdb binary
// (spec/features/todo-demo, local-server-and-web-console AC:routes,
// AC:session-ended-shown, AC:session-survives-restart,
// AC:cross-site-cookie-post-blocked, first-run-onboarding
// AC:web-keyboard-and-contrast, and the browser half of Journey D).
import { rmSync } from 'node:fs'
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { join } from 'node:path'

import AxeBuilder from '@axe-core/playwright'
import type { Locator, Page } from '@playwright/test'

import { expect, test } from './fixtures'
import { endAllSessions, fallback, ovdb, port, primary, signedInContext } from './ovdb'

const out = (name: string) => join(import.meta.dirname, 'screenshots', name + '.png')
const app = () => primary() + '/apps/todo/'

type Listed = { records: { path: string; data: { title: string; done: boolean } }[] }
const listed = (path: string) => (JSON.parse(ovdb('list', path, '--db', 'todo', '--json')) as Listed).records.map((r) => r.data)

/** Starts from no demo: unregistered and its folder gone (a retry reruns everything). */
function resetDemo() {
  if ((JSON.parse(ovdb('demo', 'status', '--json')) as { installed: boolean }).installed) {
    ovdb('databases', 'remove', 'todo', '--yes')
  }
  rmSync(join(process.env.OVDB_DATA_HOME!, 'demos'), { recursive: true, force: true })
}

function ensureInstalled() {
  ovdb('demo', 'install', '--yes')
}

/** Presses Tab until target has focus: the page is used with the keyboard only. */
async function tabTo(page: Page, target: Locator, max = 60) {
  for (let i = 0; i < max; i++) {
    if (await target.evaluate((element) => element === document.activeElement).catch(() => false)) return
    await page.keyboard.press('Tab')
  }
  throw new Error(`Tab never reached ${target}`)
}

async function expectAccessible(page: Page, name: string) {
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze()
  expect(
    results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`),
    `axe violations on ${name}`,
  ).toEqual([])
}

async function localSite(html: string): Promise<{ url: string; server: Server }> {
  const server = createServer((_, response) => {
    response.setHeader('Content-Type', 'text/html')
    response.end(html)
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  return { url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`, server }
}

test('Try a demo with the keyboard only, open the TODO app, and pass axe at 360 and 1280 px in light and dark', async ({ browser }) => {
  resetDemo()
  const context = await signedInContext(browser, { viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  await page.goto(primary() + '/')
  const option = page.locator('[data-option="demo"]')
  await expect(option).toContainText('Try a demo')
  await tabTo(page, option)
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(primary() + '/demo')
  await expect(page.getByRole('heading', { name: 'Try a demo' })).toBeFocused()
  const location = join(process.env.OVDB_DATA_HOME!, 'demos', 'todo')
  await expect(page.getByTestId('demo-location')).toHaveText(`The lists will be stored as readable files in ${location}`)
  await expectAccessible(page, 'Try a demo')

  await tabTo(page, page.getByTestId('install-demo'))
  await page.keyboard.press('Enter')
  const result = page.getByTestId('demo-result')
  await expect(result).toContainText('The TODO demo is ready')
  await expect(result).toBeFocused()
  await expect(result.getByRole('link')).toHaveText(['Open TODO app', 'Install TODO AI skill (ask the person first)', 'Done'])
  await expectAccessible(page, 'Try a demo result')

  await tabTo(page, result.getByRole('link', { name: 'Open TODO app' }))
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(app())
  await expect(page.getByRole('heading', { name: 'To buy' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'To watch' })).toBeVisible()
  await expect(page.getByTestId('stored-path')).toHaveText(`Stored in ${location} on this computer`)
  await expectAccessible(page, 'TODO app')
  await context.close()

  // Both sizes and both schemes, with screenshots for review (1920 too).
  for (const viewport of [{ width: 360, height: 740 }, { width: 1280, height: 800 }, { width: 1920, height: 1080 }]) {
    for (const colorScheme of ['light', 'dark'] as const) {
      const suffix = `${viewport.width}x${viewport.height}-${colorScheme}`
      const sized = await signedInContext(browser, { viewport, colorScheme })
      const view = await sized.newPage()
      await view.goto(primary() + '/')
      await view.locator('[data-option="demo"]').waitFor()
      await view.screenshot({ path: out(`home-demo-${suffix}`), fullPage: true })
      await view.goto(primary() + '/demo')
      await view.getByTestId('demo-result').waitFor()
      await view.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
      await view.screenshot({ path: out(`demo-${suffix}`), fullPage: true })
      if (viewport.width !== 1920) await expectAccessible(view, `Try a demo ${suffix}`)
      await view.goto(app())
      await expect(view.locator('[data-list] li')).toHaveCount(5)
      await view.screenshot({ path: out(`todo-${suffix}`), fullPage: true })
      expect(await view.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(viewport.width)
      if (viewport.width !== 1920) await expectAccessible(view, `TODO app ${suffix}`)
      await sized.close()
    }
  }
})

test('ovdb demo open starts the server and lands signed in on the TODO app', async ({ browser }) => {
  ensureInstalled()
  ovdb('server', 'stop')
  try {
    const link = JSON.parse(ovdb('demo', 'open', '--print-url', '--json')) as { url: string }
    expect(new URL(link.url).searchParams.get('next')).toBe('/apps/todo/')
    const context = await browser.newContext()
    const page = await context.newPage()
    await page.goto(link.url)
    await expect(page).toHaveURL(app())
    await expect(page.getByRole('heading', { name: 'To buy' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'To watch' })).toBeVisible()
    await context.close()
  } finally {
    ovdb('server', 'start')
  }
})

test('keyboard-only edits in the app are what the CLI reads', async ({ browser }) => {
  resetDemo()
  ensureInstalled()
  const context = await signedInContext(browser)
  const page = await context.newPage()
  await page.goto(app())
  const buy = page.locator('[data-list="to-buy"]')
  await expect(buy.locator('li')).toHaveCount(3)

  await tabTo(page, buy.getByLabel('Add to To buy'))
  await page.keyboard.type('Tea')
  await page.keyboard.press('Enter')
  await expect(buy.locator('li label')).toHaveText(['Milk', 'Bananas', 'Coffee', 'Tea'])
  await expect(buy.getByLabel('Add to To buy')).toBeFocused()

  await tabTo(page, buy.getByRole('checkbox', { name: 'Milk' }))
  await page.keyboard.press('Space')
  await expect(buy.getByRole('checkbox', { name: 'Milk' })).toBeChecked()

  await tabTo(page, buy.getByRole('button', { name: 'Delete Coffee' }))
  await page.keyboard.press('Enter')
  await expect(buy.locator('li label')).toHaveText(['Milk', 'Bananas', 'Tea'])
  await expect(buy.getByRole('checkbox', { name: 'Tea' })).toBeFocused()

  await expect
    .poll(() => listed('/lists/to-buy/items').map((item) => `${item.title}:${item.done}`).sort())
    .toEqual(['Bananas:false', 'Milk:true', 'Tea:false'])
  await context.close()
})

test('a record added with ovdb appears in the open app within 3 seconds, as text', async ({ browser }) => {
  ensureInstalled()
  const context = await signedInContext(browser)
  const page = await context.newPage()
  let dialogs = 0
  page.on('dialog', (dialog) => {
    dialogs++
    void dialog.dismiss()
  })
  await page.goto(app())
  const watch = page.locator('[data-list="to-watch"]')
  await expect(watch.getByText('Interstellar')).toBeVisible()

  ovdb('add', '/lists/to-watch/items', '{"title":"Arrival","done":false}', '--db', 'todo')
  await expect(watch.getByText('Arrival', { exact: true })).toBeVisible({ timeout: 3000 })

  const hostile = '<img src=x onerror=alert(1)>'
  ovdb('add', '/lists/to-buy/items', JSON.stringify({ title: hostile, done: false }), '--db', 'todo')
  await expect(page.locator('[data-list="to-buy"]').getByText(hostile, { exact: true })).toBeVisible({ timeout: 3000 })
  await expect(page.locator('[data-list] img')).toHaveCount(0)
  expect(dialogs).toBe(0)
  await context.close()
})

test('the app shows the session-ended copy when its session is removed, then the stopped-server copy', async ({ browser }) => {
  ensureInstalled()
  const context = await signedInContext(browser)
  const page = await context.newPage()
  await page.goto(app())
  await expect(page.getByRole('heading', { name: 'To buy' })).toBeVisible()
  try {
    endAllSessions()
    // The next poll, without focus events or a reload.
    await expect(page.getByRole('heading', { name: 'Your session ended' })).toBeVisible({ timeout: 4000 })
    await expect(page.getByText('Use ovdb open or ask your AI assistant for a new link.')).toBeVisible()
    ovdb('server', 'stop')
    await expect(page.getByRole('heading', { name: "The OVDB server isn't running" })).toBeVisible({ timeout: 4000 })
    await expect(page.getByText('Ask your AI assistant to start OVDB again, or run ovdb open.')).toBeVisible()
  } finally {
    ovdb('server', 'start')
    await context.close()
  }
})

test('the session survives a restart and a link from another site opens the app signed in', async ({ browser }) => {
  ensureInstalled()
  const context = await signedInContext(browser)
  const page = await context.newPage()
  ovdb('server', 'restart')
  await page.route('http://evil.example/**', (route) =>
    route.fulfill({ contentType: 'text/html', body: `<a id="link" href="${app()}">My lists</a>` }),
  )
  await page.goto('http://evil.example/')
  await page.locator('#link').click()
  await expect(page).toHaveURL(app())
  await expect(page.getByRole('heading', { name: 'To buy' })).toBeVisible()
  const cookie = (await context.cookies(primary())).find((c) => c.name === `ovdb_session_${port()}`)
  expect(cookie).toMatchObject({ httpOnly: true, sameSite: 'Lax' })
  await context.close()
})

test('a page on another port cannot install a demo with the session cookie', async ({ browser }) => {
  // Same site as 127.0.0.1, so the browser sends the cookie; cross-origin
  // protection must refuse the request before the endpoint runs.
  const context = await signedInContext(browser, {}, 'fallback')
  const site = await localSite(`
    <form id="install" method="post" enctype="text/plain" action="${fallback()}/api/local/v1/demo/install">
      <input type="hidden" name='{"id":"evil-demo","x":"' value='"}'><button>Install</button>
    </form>`)
  try {
    const page = await context.newPage()
    await page.goto(site.url)
    const response = page.waitForResponse(`${fallback()}/api/local/v1/demo/install`)
    await page.locator('#install button').click()
    const answer = await response
    expect(answer.status()).toBe(403)
    expect((await answer.request().allHeaders())['cookie']).toContain(`ovdb_session_${port()}`)
    await expect(page.getByText('This request came from another website')).toBeVisible()
    expect(ovdb('databases', '--json')).not.toContain('evil-demo')
  } finally {
    site.server.close()
    await context.close()
  }
})
