// Increment 3 browser journey: Browse data and Use as default in the web
// console against a real ovdb binary (database-context-navigation
// AC:browse-own-data and AC:tui-and-web-select-database; the browse and
// default steps of Journey B). Records are written the way an agent or app
// writes them, through the data API, and checked back through the CLI.
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { expect, test } from './fixtures'
import { fallback, ovdb, primary, signedInContext } from './ovdb'

const out = (name: string) => join(import.meta.dirname, 'screenshots', name + '.png')

async function put(db: string, key: string, data: unknown) {
  const secret = readFileSync(join(process.env.OVDB_RUNTIME_DIR!, 'secret'), 'utf8').trim()
  const response = await fetch(`${fallback()}/v1/databases/${db}/records/${key}`, {
    method: 'PUT',
    headers: { Authorization: `Bearer ${secret}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ data }),
  })
  expect(response.status, `PUT ${key}`).toBe(204)
}

/** `ovdb pwd` outside any project: the global default is what applies. */
function pwdOutsideProjects(): string {
  const dir = mkdtempSync(join(tmpdir(), 'ovdb-e2e-cwd-'))
  try {
    return execFileSync(process.env.OVDB_E2E_BIN!, ['pwd'], { env: process.env, encoding: 'utf8', cwd: dir })
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
}

test('browse a database page by page, see a record as text, and use it as the default', async ({ browser }) => {
  ovdb('databases', 'create', 'browse1')
  ovdb('add', '/items', '{"title":"from the CLI"}', '--db', 'browse1', '--id', 'cli')
  await put('browse1', 'items/x', { title: '<script>window.pwned = 1</script>', note: '<img src=x onerror="window.pwned = 2">' })
  for (let i = 0; i < 55; i++) await put('browse1', `items/r${String(i).padStart(2, '0')}`, { title: `Record ${i}`, done: i % 2 === 0 })

  const context = await signedInContext(browser, { viewport: { width: 1920, height: 1080 } })
  const page = await context.newPage()
  await page.goto(primary() + '/')
  await page.locator('[data-option="browse"]').click()
  await expect(page).toHaveURL(primary() + '/browse')
  await expect(page.getByRole('heading', { name: 'Browse data' })).toBeVisible()
  await page.locator('[data-browse-database="browse1"]').click()

  await expect(page.getByText('ovdb list / --db browse1', { exact: true })).toBeVisible()
  await page.locator('[data-collection="items"]').click()
  await expect(page).toHaveURL(primary() + '/browse/browse1/items')
  await expect(page.getByText('ovdb list /items --db browse1', { exact: true })).toBeVisible()
  await expect(page.locator('[data-record]')).toHaveCount(50)
  await expect(page.getByTestId('page')).toHaveText('Records 1–50')
  await page.screenshot({ path: out('browse-records-1920x1080-light'), fullPage: true })
  await page.getByRole('button', { name: 'Next page' }).click()
  await expect(page.getByTestId('page')).toHaveText('Records 51–57')
  await expect(page.locator('[data-record]')).toHaveCount(7)
  await expect(page.getByRole('button', { name: 'Next page' })).toHaveCount(0)

  await page.locator('[data-record="x"]').click()
  const record = page.getByTestId('record')
  await expect(record).toContainText('"title": "<script>window.pwned = 1</script>"')
  await expect(page.getByText('ovdb get /items/x --db browse1', { exact: true })).toBeVisible()
  expect(await page.evaluate(() => (window as { pwned?: number }).pwned)).toBeUndefined()
  await expect(record.locator('script, img')).toHaveCount(0)
  // Read-only: the only buttons are Use as default and opening a nested collection.
  await expect(page.locator('main button')).toHaveText(['Use as default', 'Open'])
  await page.screenshot({ path: out('browse-record-1920x1080-light'), fullPage: true })

  // The record the CLI wrote is there too, and a reload keeps the place.
  await page.goto(primary() + '/browse/browse1/items/cli')
  await expect(page.getByTestId('record')).toContainText('"title": "from the CLI"')

  await page.getByTestId('use-as-default').click()
  await expect(page.getByRole('status')).toContainText('Now using browse1 by default for all projects')
  expect(pwdOutsideProjects()).toBe('browse1:/ (global default)\n')

  // Home now summarises it.
  await page.goto(primary() + '/')
  await expect(page.getByTestId('status-line')).toContainText('using browse1 (default)')
  await context.close()

  // Phone width, dark.
  const phone = await signedInContext(browser, { viewport: { width: 360, height: 740 }, colorScheme: 'dark' })
  const small = await phone.newPage()
  await small.goto(primary() + '/browse/browse1/items')
  await expect(small.locator('[data-record]')).toHaveCount(50)
  expect(await small.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(360)
  await small.screenshot({ path: out('browse-records-360x740-dark'), fullPage: true })
  await small.goto(primary() + '/browse/browse1/items/x')
  await expect(small.getByTestId('record')).toBeVisible()
  expect(await small.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(360)
  await small.screenshot({ path: out('browse-record-360x740-dark'), fullPage: true })
  await phone.close()

  ovdb('databases', 'remove', 'browse1', '--yes')
})

test('a console session cannot set a project context (E3)', async ({ browser }) => {
  const context = await signedInContext(browser)
  const page = await context.newPage()
  await page.goto(primary() + '/')
  const status = await page.evaluate(async () => {
    const response = await fetch('/api/local/v1/context', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ scope: 'project', dir: '/tmp/p', database: 'x' }),
    })
    return { status: response.status, body: await response.json() }
  })
  expect(status.status).toBe(403)
  expect(status.body.error.code).toBe('forbidden')
  await context.close()
})
