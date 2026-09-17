// The storage catalogue as the console shows it. The server sends the
// engines already in order (inGitDB and SQLite pinned, then by name); the
// console only filters, with the rule setup.FilterEngines uses in Go
// (database-setup-and-providers#REQ:catalogue-order-and-filter).
import type { Engine } from './api'

export function filterEngines(engines: Engine[], filter: string): Engine[] {
  const needle = filter.trim().toLowerCase()
  return engines.filter((engine) => `${engine.id}\n${engine.name}\n${engine.description}`.toLowerCase().includes(needle))
}

/** The default location for a new database under the data home, as setup.DefaultPath builds it. */
export function defaultLocation(dataHome: string, engine: string, name: string): string {
  const separator = dataHome.includes('\\') && !dataHome.includes('/') ? '\\' : '/'
  const base = dataHome.endsWith(separator) ? dataHome : dataHome + separator
  return base + name + (engine === 'sqlite' ? '.sqlite' : '')
}
