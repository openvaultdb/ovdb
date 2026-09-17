// Builds ovdb (unless OVDB_E2E_BIN is set), starts it detached in a fresh
// temporary OVDB home on a free port, and stops it and removes the home
// afterwards. Workers inherit the environment set here.
import { execFileSync } from 'node:child_process'
import { existsSync, mkdtempSync, rmSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..')

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer()
    server.once('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const address = server.address()
      server.close(() => resolve(typeof address === 'object' && address ? address.port : 0))
    })
  })
}

export default async function globalSetup() {
  if (!existsSync(join(webRoot, 'dist', 'index.html'))) {
    throw new Error('web/dist is not built: run `pnpm -C web build` before the browser tests')
  }
  const base = mkdtempSync(join(tmpdir(), 'ovdb-e2e-'))
  let binary = process.env.OVDB_E2E_BIN
  if (!binary) {
    binary = join(base, process.platform === 'win32' ? 'ovdb.exe' : 'ovdb')
    execFileSync('go', ['build', '-o', binary, '.'], { cwd: join(webRoot, '..'), stdio: 'inherit' })
  }
  const port = await freePort()
  Object.assign(process.env, {
    OVDB_E2E_BIN: binary,
    OVDB_HOME: join(base, 'home'),
    OVDB_RUNTIME_DIR: join(base, 'run'),
    OVDB_DATA_HOME: join(base, 'data'),
    OVDB_PORT: String(port),
    OVDB_NON_INTERACTIVE: '1',
  })
  execFileSync(binary, ['server', 'start', '--json'], { env: process.env, stdio: 'pipe' })

  return () => {
    try {
      execFileSync(binary, ['server', 'stop', '--json'], { env: process.env, stdio: 'pipe' })
    } finally {
      rmSync(base, { recursive: true, force: true })
    }
  }
}
