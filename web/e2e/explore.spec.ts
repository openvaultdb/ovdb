// Increment 7 browser journey: Explore data hand-off to DataTug against a
// real ovdb binary (spec/features/explore-data-handoff). It asserts the
// intent-first menu, the four-key descriptor DataTug CLI writes with no
// token, the DataTug.app honesty copy and that no control there implies
// OVDB can open a database, and an opt-in real `datatug` run when it is on
// this machine's PATH.
import { execFileSync } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'

import { expect, test } from './fixtures'
import { ovdb, primary, signedInContext } from './ovdb'

const out = (name: string) => join(import.meta.dirname, 'screenshots', name + '.png')

async function axe(page: Page, name: string) {
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze()
  expect(
    results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`),
    `axe violations on ${name}`,
  ).toEqual([])
}

function datatugOnPath(): boolean {
  try {
    execFileSync(process.platform === 'win32' ? 'where' : 'which', ['datatug'], { stdio: 'pipe' })
    return true
  } catch {
    return false
  }
}

function ensureDemo() {
  if (!(JSON.parse(ovdb('demo', 'status', '--json')) as { installed: boolean }).installed) {
    ovdb('demo', 'install', '--yes')
  }
}

test.beforeAll(() => {
  ensureDemo()
})

test('Home offers Explore data once a database is registered', async ({ browser }) => {
  ensureDemo()
  const context = await signedInContext(browser, { viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  await page.goto(primary() + '/')
  await expect(page.locator('[data-option="explore"]')).toContainText('Explore data')
  await expect(page.locator('[data-option="explore"][aria-disabled]')).toHaveCount(0)
  await context.close()
})

test('Explore data names the demo database, shows demo-specific DataTug CLI copy, and writes a four-key descriptor with no token', async ({
  browser,
}) => {
  const context = await signedInContext(browser, { viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  await page.goto(primary() + '/demo')
  await page.getByTestId('demo-result').waitFor()
  await page.locator('[data-testid="demo-explore"]').click()
  await expect(page).toHaveURL(primary() + '/explore?db=todo')
  await expect(page.getByRole('heading', { name: 'Explore data' })).toBeFocused()
  await expect(page.getByText("Where would you like to explore todo's data?")).toBeVisible()
  await expect(page.locator('[data-option="datatug-cli"]')).toContainText(
    "DataTug shows your two lists, not their items yet. See items in the TODO app, in Browse data, or with `ovdb list /lists/to-buy/items --db todo`.",
  )
  await axe(page, 'explore menu')

  await page.locator('[data-option="datatug-cli"]').click()
  const result = page.getByTestId('explore-datatug-cli')
  await result.waitFor()
  const text = await result.textContent()
  expect(text).toContain('--no-policies')
  expect(text).toContain('ovdb token create --db todo --scope read-only')
  expect(text).not.toMatch(/ovdb_[A-Za-z0-9]/) // never a token value
  await axe(page, 'explore datatug-cli')

  const descriptorPath = join(process.env.OVDB_HOME!, 'explore', 'datatug', 'todo.json')
  expect(existsSync(descriptorPath)).toBe(true)
  const descriptor = JSON.parse(readFileSync(descriptorPath, 'utf8')) as Record<string, unknown>
  expect(Object.keys(descriptor).sort()).toEqual(['baseUrl', 'databaseId', 'principalId', 'tokenEnv'])
  expect(descriptor.databaseId).toBe('todo')
  expect(descriptor.tokenEnv).toBe('OVDB_DATATUG_TOKEN')
  expect(descriptor.principalId).toBe('local-owner')
  expect(descriptor).not.toHaveProperty('token')

  if (datatugOnPath()) {
    await expect(result).toContainText('datatug is on the PATH the OVDB server sees.')
  } else {
    await expect(result).toContainText("datatug isn't on the PATH the OVDB server sees")
    await expect(result).toContainText('brew tap datatug/tap && brew install datatug')
  }

  for (const viewport of [{ width: 360, height: 740 }, { width: 1280, height: 800 }, { width: 1920, height: 1080 }]) {
    for (const colorScheme of ['light', 'dark'] as const) {
      const suffix = `${viewport.width}x${viewport.height}-${colorScheme}`
      const sized = await signedInContext(browser, { viewport, colorScheme })
      const view = await sized.newPage()
      await view.goto(primary() + '/explore?db=todo')
      await view.getByText("Where would you like to explore todo's data?").waitFor()
      await view.screenshot({ path: out(`explore-menu-${suffix}`), fullPage: true })
      await view.locator('[data-option="datatug-cli"]').click()
      await view.getByTestId('explore-datatug-cli').waitFor()
      await view.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
      await view.screenshot({ path: out(`explore-datatug-cli-${suffix}`), fullPage: true })
      await sized.close()
    }
  }
  await context.close()
})

test('DataTug.app states the honest limitation and offers no control implying OVDB can open a database there', async ({ browser }) => {
  const context = await signedInContext(browser, { viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  await page.goto(primary() + '/explore?db=todo')
  await page.getByText("Where would you like to explore todo's data?").waitFor()
  await page.locator('[data-option="datatug-app"]').click()
  await expect(page.getByText("DataTug.app can't open an OpenVaultDB database directly yet.")).toBeVisible()
  await expect(page.getByTestId('datatug-cli-instead')).toContainText('Use DataTug CLI instead')
  const openLink = page.getByTestId('open-datatug-app')
  await expect(openLink).toHaveAttribute('href', 'https://datatug.app')
  await expect(openLink).toHaveText('Open DataTug.app')
  // AC:datatug-app-is-honest: no control labelled as opening the database there.
  await expect(page.getByText(/open (the |your )?database/i)).toHaveCount(0)
  await axe(page, 'explore datatug-app')

  for (const viewport of [{ width: 360, height: 740 }, { width: 1280, height: 800 }, { width: 1920, height: 1080 }]) {
    for (const colorScheme of ['light', 'dark'] as const) {
      const suffix = `${viewport.width}x${viewport.height}-${colorScheme}`
      const sized = await signedInContext(browser, { viewport, colorScheme })
      const view = await sized.newPage()
      await view.goto(primary() + '/explore?db=todo')
      await view.locator('[data-option="datatug-app"]').click()
      await view.getByTestId('open-datatug-app').waitFor()
      await view.screenshot({ path: out(`explore-datatug-app-${suffix}`), fullPage: true })
      await sized.close()
    }
  }
  await context.close()
})

// Opt-in: actually runs `datatug query run` against the real server, real
// PATH-installed datatug. Skipped unless OVDB_E2E_DATATUG=1, since most CI
// runners have no datatug binary (spike S4 proved this end to end by hand).
test('a real datatug query run reads the demo lists through the printed command', async ({ browser }) => {
  test.skip(process.env.OVDB_E2E_DATATUG !== '1', 'set OVDB_E2E_DATATUG=1 with datatug on PATH to run this')
  const created = JSON.parse(ovdb('token', 'create', '--db', 'todo', '--scope', 'read-only', '--label', 'e2e-explore', '--json')) as { token: string }
  const context = await signedInContext(browser)
  const page = await context.newPage()
  await page.goto(primary() + '/explore?db=todo')
  await page.locator('[data-option="datatug-cli"]').click()
  const result = page.getByTestId('explore-datatug-cli')
  await result.waitFor()
  const command = await page.locator('[data-testid="explore-datatug-cli"] pre').last().textContent()
  await context.close()
  expect(command).toContain('datatug query run')
  const rewritten = command!.replace(/\s*\\\s*\n\s*/g, ' ')
  const output = execFileSync('sh', ['-c', rewritten], {
    encoding: 'utf8',
    env: {
      ...process.env,
      OVDB_DATATUG_TOKEN: created.token,
      OVDB_DATATUG_TOKEN_BASE_URL: 'http://127.0.0.1:' + process.env.OVDB_PORT,
      OVDB_DATATUG_TOKEN_PRINCIPAL_ID: 'local-owner',
    },
  })
  const rows = JSON.parse(output.slice(output.indexOf('['))) as { $key: string }[]
  expect(rows.map((r) => r.$key).sort()).toEqual(['to-buy', 'to-watch'])
})
