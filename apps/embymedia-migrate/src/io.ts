import { readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { mkdir } from 'node:fs/promises'

const MAX_SECRET_FILE_BYTES = 1024 * 1024

export async function readSecretFile(path: string): Promise<string> {
  const content = await readFile(resolve(path), 'utf8')
  if (Buffer.byteLength(content) > MAX_SECRET_FILE_BYTES) throw new Error(`${path} exceeds 1 MiB`)
  const value = content.trim()
  if (value.length === 0) throw new Error(`${path} is empty`)
  return value
}

export async function writeJsonReport(path: string, report: unknown): Promise<void> {
  const absolute = resolve(path)
  await mkdir(dirname(absolute), { recursive: true })
  await writeFile(absolute, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 })
}
