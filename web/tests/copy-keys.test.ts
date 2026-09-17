// The web half of spec/features/configuration-parity#REQ:copy-catalogue and
// its AC:copy-key-missing-fails: scans every .vue/.ts source file for a
// t('literal key') call and fails, naming the file and key, when that
// literal is absent from copy/en.json. This is the Vite-side mirror of
// copy/copy_ast_test.go's Go AST scan.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import en from '../../copy/en.json'

const here = dirname(fileURLToPath(import.meta.url))
const webRoot = join(here, '..')

function collectSourceFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules' || entry === 'dist') continue
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      collectSourceFiles(full, out)
    } else if (/\.(vue|ts)$/.test(entry) && !entry.endsWith('.test.ts')) {
      out.push(full)
    }
  }
  return out
}

describe('t() literal keys', () => {
  it('every literal t(\'key\') referenced in src/ and apps/ exists in copy/en.json', () => {
    const catalogue = en as Record<string, string>
    const keyPattern = /\bt\(\s*['"]([^'"]+)['"]/g
    const missing: string[] = []

    for (const dir of ['src', 'apps']) {
      for (const file of collectSourceFiles(join(webRoot, dir))) {
        const content = readFileSync(file, 'utf-8')
        for (const match of content.matchAll(keyPattern)) {
          const key = match[1]
          if (!(key in catalogue)) {
            missing.push(`${file}: t('${key}')`)
          }
        }
      }
    }

    expect(missing, missing.join('\n')).toEqual([])
  })
})
