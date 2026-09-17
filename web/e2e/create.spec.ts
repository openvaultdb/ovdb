// Increment 2 browser journey: Create a database and Databases in the web
// console against a real ovdb binary (the web half of Journey B so far).
// It checks what the person sees and, through the CLI and the file system,
// what really happened.
import { existsSync, mkdirSync, readdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from './fixtures'
import { ovdb, primary, signedInContext } from './ovdb'

const dataHome = () => process.env.OVDB_DATA_HOME!

interface Listed {
  databases: { id: string; engine: string; location: string; state: string }[]
}

const listed = () => (JSON.parse(ovdb('databases', '--json')) as Listed).databases

test('create an inGitDB database from Home, see it listed, then remove it and keep its data', async ({ browser }) => {
  const context = await signedInContext(browser)
  const page = await context.newPage()
  await page.goto(primary() + '/')
  await page.locator('[data-option="create"]').click()
  await expect(page).toHaveURL(primary() + '/databases/new')
  await expect(page.getByRole('heading', { name: 'Where should OVDB keep your data?' })).toBeVisible()

  // Pinned inGitDB and SQLite, then the rest by name; the filter keeps that order.
  const choices = page.locator('[data-engine]')
  await expect(choices).toHaveCount(5)
  expect(await choices.evaluateAll((els) => els.map((el) => el.getAttribute('data-engine')))).toEqual([
    'ingitdb', 'sqlite', 'firestore', 'mysql', 'postgres',
  ])
  await page.getByLabel('Filter').fill('sql')
  await expect(choices).toHaveCount(3)
  expect(await choices.evaluateAll((els) => els.map((el) => el.getAttribute('data-engine')))).toEqual(['sqlite', 'mysql', 'postgres'])

  // PostgreSQL: manifest steps, a docs link, and nothing to type.
  await page.locator('[data-engine="postgres"]').click()
  const steps = page.getByTestId('manifest-steps')
  await expect(steps.getByRole('heading', { name: 'Set this up with a manifest file' })).toBeVisible()
  await expect(steps.getByText('ovdb init --engine postgres --id <name>')).toBeVisible()
  await expect(steps.getByText('ovdb databases reload <name>')).toBeVisible()
  await expect(steps.getByText('Guided connect is coming.')).toBeVisible()
  await expect(steps.getByRole('link', { name: /github\.com\/openvaultdb/ })).toBeVisible()
  await expect(page.getByRole('textbox')).toHaveCount(0)
  await page.getByRole('button', { name: 'Change storage' }).click()
  await page.getByLabel('Filter').fill('')

  // inGitDB with the suggested location.
  await page.locator('[data-engine="ingitdb"]').click()
  await expect(page.getByLabel('Name')).toBeFocused()
  await page.getByLabel('Name').fill('web1')
  const folder = join(dataHome(), 'web1')
  await expect(page.getByLabel('Location')).toHaveValue(folder)
  await page.getByRole('button', { name: 'Create database' }).click()

  const result = page.getByTestId('create-result')
  await expect(result.getByText('Created database web1')).toBeVisible()
  await expect(result.getByText(`Stored in ${folder}/ as readable files with Git history.`)).toBeVisible()
  expect(existsSync(folder)).toBe(true)
  expect(listed().find((db) => db.id === 'web1')).toMatchObject({ engine: 'ingitdb', location: folder, state: 'mounted' })

  // Home now counts it; Databases lists it and removes it after confirming.
  await result.getByRole('link', { name: 'Done' }).click()
  await expect(page.getByTestId('status-line')).toContainText('database')
  await page.locator('[data-option="databases"]').click()
  await expect(page).toHaveURL(primary() + '/databases')
  const row = page.locator('[data-database="web1"]')
  await expect(row.getByText('Ready')).toBeVisible()
  await expect(row.getByText(folder)).toBeVisible()
  await row.getByRole('button', { name: 'Reload' }).click()
  await expect(page.getByRole('status')).toContainText('Reloaded database web1 · Ready')
  await row.getByRole('button', { name: 'Remove' }).click()
  await expect(row.getByText(`Its data stays where it is: ${folder}`)).toBeVisible()
  await expect(row.getByRole('button', { name: 'Keep it' })).toBeFocused()
  await row.getByRole('button', { name: 'Remove' }).click()
  await expect(page.getByRole('status')).toContainText('Removed database web1 from OVDB')
  await expect(page.getByText(`Your data is still in ${folder}.`)).toBeVisible()
  await expect(page.locator('[data-database="web1"]')).toHaveCount(0)
  expect(existsSync(folder)).toBe(true)
  expect(listed().some((db) => db.id === 'web1')).toBe(false)
  await context.close()
})

test('creating over a folder with files is refused with next steps, and nothing is written (AC:create-refuses-overwrite)', async ({ browser }) => {
  const folder = join(dataHome(), 'notes-web')
  mkdirSync(folder, { recursive: true })
  writeFileSync(join(folder, 'mine.txt'), 'keep me')

  const context = await signedInContext(browser)
  const page = await context.newPage()
  await page.goto(primary() + '/databases/new')
  await page.locator('[data-engine="ingitdb"]').click()
  await page.getByLabel('Name').fill('notes-web')
  await page.getByRole('button', { name: 'Create database' }).click()

  const alert = page.getByRole('alert')
  await expect(alert.getByText("Couldn't create the database")).toBeVisible()
  // The reason sits under the field it is about.
  await expect(page.getByText(`${folder} already has files in it. OVDB never writes over existing data.`)).toBeVisible()
  await expect(alert.getByText('ovdb databases create notes-web --path <another absolute path>', { exact: true })).toBeVisible()
  await expect(alert.getByText('notes-web-2')).toHaveCount(0)
  await expect(page.getByLabel('Location')).toHaveAttribute('aria-invalid', 'true')
  await alert.getByRole('button', { name: 'Choose another location' }).click()
  await expect(page.getByLabel('Location')).toBeFocused()

  expect(readdirSync(folder)).toEqual(['mine.txt'])
  expect(listed().some((db) => db.id === 'notes-web')).toBe(false)
  await context.close()
})

test('a SQLite result puts describing the schema first', async ({ browser }) => {
  const context = await signedInContext(browser)
  const page = await context.newPage()
  await page.goto(primary() + '/databases/new')
  await page.locator('[data-engine="sqlite"]').click()
  await page.getByLabel('Name').fill('shop-web')
  await expect(page.getByLabel('Location')).toHaveValue(join(dataHome(), 'shop-web.sqlite'))
  await page.getByRole('button', { name: 'Create database' }).click()
  const result = page.getByTestId('create-result')
  await expect(result.getByText('Created database shop-web')).toBeVisible()
  const first = result.locator('li').first()
  await expect(first).toContainText('Describe your data: edit')
  await expect(first).toContainText('ovdb databases reload shop-web')
  await expect(result).not.toContainText('ovdb add')
  ovdb('databases', 'remove', 'shop-web', '--yes')
  await context.close()
})
