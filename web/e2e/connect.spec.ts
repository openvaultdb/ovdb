// Increment 5 browser journey: Connect an existing database in the web
// console against a real ovdb binary. It checks what the person sees and,
// through Git, the CLI and the file system, that the storage was not changed.
import { execFileSync } from 'node:child_process'
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'

import { expect, test } from './fixtures'
import { ovdb, primary, signedInContext } from './ovdb'

const out = (name: string) => join(import.meta.dirname, 'screenshots', name + '.png')

interface Listed {
  databases: { id: string; engine: string; location: string; state: string }[]
}

const listed = () => (JSON.parse(ovdb('databases', '--json')) as Listed).databases

async function axe(page: Page, name: string) {
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze()
  expect(
    results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`),
    `axe violations on ${name}`,
  ).toEqual([])
}

function git(dir: string, ...args: string[]): string {
  return execFileSync('git', ['-C', dir, ...args], { encoding: 'utf8' })
}

/** An inGitDB Git repository with a record, created and removed through OVDB (its data kept), then committed. */
function existingRepository(id: string): string {
  ovdb('databases', 'create', id)
  const folder = join(process.env.OVDB_DATA_HOME!, id)
  ovdb('add', '/items', '{"title":"Milk"}', '--db', id)
  ovdb('databases', 'remove', id, '--yes')
  git(folder, 'add', '-A')
  git(folder, '-c', 'user.name=Person', '-c', 'user.email=person@example.com', 'commit', '-q', '--allow-empty', '-m', 'Everything')
  return folder
}

function snapshot(folder: string) {
  return { status: git(folder, 'status', '--porcelain', '--ignored'), head: git(folder, 'rev-parse', 'HEAD'), config: readFileSync(join(folder, '.git', 'config'), 'utf8') }
}

test('Home offers the four options in order, and a Git folder connects without a file changing (AC:home-shows-options, AC:connect-leaves-folder-untouched, AC:result-next-actions)', async ({ browser }) => {
  const folder = existingRepository('web-source')
  const before = snapshot(folder)

  const context = await signedInContext(browser, { viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  await page.goto(primary() + '/')
  const primaryOptions = page.locator('ul[aria-label="What would you like to do?"] [data-option]')
  expect(await primaryOptions.evaluateAll((els) => els.map((el) => el.getAttribute('data-option')))).toEqual(['demo', 'create', 'connect', 'server'])
  await expect(page.locator('[data-option="connect"]')).toContainText('Connect an existing database')

  // Keyboard only: tab to Connect, choose inGitDB, type the folder.
  await page.locator('[data-option="connect"]').focus()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(primary() + '/databases/connect')
  await expect(page.getByRole('heading', { name: 'What would you like to connect?' })).toBeVisible()
  const choices = page.locator('[data-engine]')
  expect(await choices.evaluateAll((els) => els.map((el) => el.getAttribute('data-engine')))).toEqual(['ingitdb', 'sqlite', 'firestore', 'mysql', 'postgres', 'manifest'])
  await axe(page, 'connect picker')
  await page.locator('[data-engine="ingitdb"]').focus()
  await page.keyboard.press('Enter')
  await expect(page.getByLabel('Folder')).toBeFocused()
  await page.keyboard.type(folder)
  await expect(page.getByLabel('Name')).toHaveValue('web-source')
  await page.getByLabel('Name').fill('web-journal')
  await axe(page, 'connect form')
  await page.getByLabel('Name').press('Enter')

  const result = page.getByTestId('connect-result')
  await expect(result.getByText('Connected database web-journal')).toBeVisible()
  await expect(result.getByText(`Your data stays where it is: ${folder}`)).toBeVisible()
  for (const name of ['Browse data', 'See your databases', 'Done']) {
    await expect(result.getByRole('link', { name })).toBeVisible()
  }
  await expect(result.getByRole('button', { name: 'Use as default' })).toBeVisible()
  await axe(page, 'connect result')
  expect(snapshot(folder)).toEqual(before)
  expect(listed().find((db) => db.id === 'web-journal')).toMatchObject({ engine: 'ingitdb', location: folder, state: 'mounted' })

  await result.getByRole('link', { name: 'Browse data' }).click()
  await expect(page).toHaveURL(primary() + '/browse/web-journal')
  await expect(page.getByText('items', { exact: true }).first()).toBeVisible()
  expect(snapshot(folder)).toEqual(before)

  // At 360 px in dark, too.
  await page.setViewportSize({ width: 360, height: 740 })
  await page.emulateMedia({ colorScheme: 'dark' })
  await page.goto(primary() + '/databases/connect')
  await page.locator('[data-engine="sqlite"]').click()
  await axe(page, 'connect form 360 dark')
  ovdb('databases', 'remove', 'web-journal', '--yes')
  await context.close()
})

test('a text file named .sqlite and a manifest without its connection variable are refused, naming only the variable (AC:connect-postgres-manifest, AC:dsn-never-leaks)', async ({ browser }) => {
  const dir = mkdtempSync(join(tmpdir(), 'ovdb-connect-e2e-'))
  const text = join(dir, 'x.sqlite')
  writeFileSync(text, 'not a database\n')
  execFileSync(process.env.OVDB_E2E_BIN!, ['init', '--engine', 'postgres', '--id', 'crm-web', '--out', join(dir, 'crm.yaml')], { env: process.env })
  const manifestPath = join(dir, 'crm.yaml')
  writeFileSync(manifestPath, readFileSync(manifestPath, 'utf8').replace('OVDB_POSTGRES_DSN', 'OVDB_E2E_CRM_DSN'))

  const context = await signedInContext(browser)
  const page = await context.newPage()
  await page.goto(primary() + '/databases/connect')
  await page.locator('[data-engine="sqlite"]').click()
  await page.getByLabel('File').fill(text)
  await page.getByLabel('Name').fill('x-web')
  await page.getByRole('button', { name: 'Connect database' }).click()
  await expect(page.getByText(`${text} isn't a SQLite database file.`)).toBeVisible()
  await expect(page.getByLabel('File')).toHaveAttribute('aria-invalid', 'true')
  expect(readFileSync(text, 'utf8')).toBe('not a database\n')

  await page.getByRole('button', { name: 'Change storage' }).click()
  await page.locator('[data-engine="postgres"]').click()
  await expect(page.getByTestId('manifest-steps')).toContainText('ovdb init --engine postgres --id <name>')
  await expect(page.getByRole('textbox')).toHaveCount(1)
  await page.getByLabel('Manifest file').fill(manifestPath)
  await page.getByRole('button', { name: 'Connect database' }).click()
  const alert = page.getByRole('alert')
  await expect(alert).toContainText('Set OVDB_E2E_CRM_DSN and run ovdb server restart from that shell')
  await expect(page.getByText("OVDB_E2E_CRM_DSN isn't set in the OVDB server's environment")).toBeVisible()
  expect(listed().some((db) => db.id === 'crm-web' || db.id === 'x-web')).toBe(false)
  expect(existsSync(join(process.env.OVDB_HOME!, 'databases', 'crm-web.yaml'))).toBe(false)
  expect(readdirSync(join(process.env.OVDB_HOME!, 'databases')).filter((name) => name.startsWith('.'))).toEqual([])
  await context.close()
  rmSync(dir, { recursive: true, force: true })
})

// Screenshots of Connect for visual review, in light and dark at three sizes.
for (const size of [{ width: 360, height: 740 }, { width: 1280, height: 800 }, { width: 1920, height: 1080 }]) {
  for (const colorScheme of ['light', 'dark'] as const) {
    const suffix = `${size.width}x${size.height}-${colorScheme}`
    test(`connect screens ${suffix}`, async ({ browser }) => {
      const id = `shot-${size.width}-${colorScheme}`
      const folder = existingRepository(id + '-src')
      const context = await signedInContext(browser, { viewport: size, colorScheme })
      const page = await context.newPage()
      const shot = async (name: string) => {
        await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
        await page.screenshot({ path: out(`connect-${name}-${suffix}`), fullPage: true })
      }
      await page.goto(primary() + '/')
      await page.getByText('What would you like to do?').waitFor()
      await shot('home')
      await page.goto(primary() + '/databases/connect')
      await page.getByText('What would you like to connect?').waitFor()
      await shot('picker')
      await page.locator('[data-engine="postgres"]').click()
      await page.getByTestId('manifest-steps').waitFor()
      await shot('postgres')
      await page.getByRole('button', { name: 'Change storage' }).click()
      await page.locator('[data-engine="ingitdb"]').click()
      await page.getByLabel('Folder').fill(folder + '-missing')
      await page.getByLabel('Name').fill(id)
      await page.getByRole('button', { name: 'Connect database' }).click()
      await page.getByRole('alert').waitFor()
      await shot('refused')
      await page.getByLabel('Folder').fill(folder)
      await page.getByLabel('Name').fill(id)
      await shot('form')
      await page.getByRole('button', { name: 'Connect database' }).click()
      await page.getByTestId('connect-result').waitFor()
      await shot('result')
      ovdb('databases', 'remove', id, '--yes')
      await context.close()
    })
  }
}
