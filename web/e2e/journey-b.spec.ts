// Journey B, whole (configuration-parity#REQ:journey-b-web,
// AC:journey-b-passes): from a login link given by `ovdb open --print-url`,
// a person creates a database, browses it, sets it as the default, turns
// usage statistics off in Settings and opens Explore data; then the bare
// address without a session shows the landing page. What happened is
// checked through the CLI.
import { execFileSync } from 'node:child_process'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { expect, test } from './fixtures'
import { fallback, loginLink, ovdb, primary } from './ovdb'

/** `ovdb pwd` outside any project: the global default is what applies. */
function pwdOutsideProjects(): string {
  const dir = mkdtempSync(join(tmpdir(), 'ovdb-e2e-cwd-'))
  try {
    return execFileSync(process.env.OVDB_E2E_BIN!, ['pwd'], { env: process.env, encoding: 'utf8', cwd: dir })
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
}

test('Journey B: create, browse, set default, turn usage statistics off and explore, from a login link', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  await page.goto(loginLink().url)
  await expect(page.getByRole('heading', { name: 'What would you like to do?' })).toBeVisible()

  // Create a database.
  await page.locator('[data-option="create"]').click()
  await page.locator('[data-engine="ingitdb"]').click()
  await page.getByLabel('Name').fill('journeyb')
  await page.getByRole('button', { name: 'Create database' }).click()
  await expect(page.getByTestId('create-result').getByText('Created database journeyb')).toBeVisible()
  ovdb('add', '/items', '{"title":"Hello"}', '--db', 'journeyb', '--id', 'hello')

  // Browse it.
  await page.goto(primary() + '/')
  await page.locator('[data-option="browse"]').click()
  await page.locator('[data-browse-database="journeyb"]').click()
  await page.locator('[data-collection="items"]').click()
  await page.locator('[data-record="hello"]').click()
  await expect(page.getByTestId('record')).toContainText('"title": "Hello"')

  // Set it as the default for all projects.
  await page.getByTestId('use-as-default').click()
  await expect(page.getByRole('status')).toContainText('Now using journeyb by default for all projects')
  expect(pwdOutsideProjects()).toBe('journeyb:/ (global default)\n')

  // Settings → Usage statistics, in the TUI's words, turned off.
  await page.goto(primary() + '/settings')
  const usage = page.getByTestId('usage-settings')
  await expect(usage.getByTestId('usage-state')).toHaveText("Status: Off (you haven't decided yet)")
  await expect(usage).toContainText('Help improve OpenVaultDB by sending usage statistics to PostHog (EU).')
  await expect(usage).toContainText('Anything you type')
  await usage.getByRole('button', { name: 'Keep off' }).click()
  await expect(usage.getByTestId('usage-state')).toHaveText('Status: Off')
  const status = JSON.parse(ovdb('telemetry', 'status', '--json')) as { telemetry: { state: string; channel: string; has_install_id: boolean } }
  expect(status.telemetry).toMatchObject({ state: 'disabled', channel: 'web', has_install_id: false })

  // Explore data.
  await page.goto(primary() + '/explore?db=journeyb')
  await expect(page.getByText("Where would you like to explore journeyb's data?")).toBeVisible()
  await context.close()

  // The bare address without a session: the landing page.
  const anonymous = await browser.newContext()
  const landing = await anonymous.newPage()
  for (const base of [primary(), fallback()]) {
    await landing.goto(base + '/')
    await expect(landing.getByRole('heading', { name: 'Open the console from OVDB' })).toBeVisible()
    await expect(landing.getByText('ask your AI assistant', { exact: false })).toBeVisible()
  }
  await anonymous.close()

  ovdb('databases', 'remove', 'journeyb', '--yes')
})
