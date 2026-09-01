import { chmod, mkdtemp, mkdir, readFile, stat, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import {
  assertPathUnchanged,
  assertTreeUnchanged,
  capturePath,
  captureTreeHash,
  embyLibraryPath,
  mediaPath,
  removeSnapshotted,
  safeUnder,
  strmRelativePath,
  writeStrm,
} from '../src/media/paths.ts'

const roots: string[] = []

async function fixtureRoot(): Promise<string> {
  const root = await mkdtemp(join(tmpdir(), 'embymedia-paths-'))
  roots.push(root)
  return root
}

afterEach(async () => {
  const { rm } = await import('node:fs/promises')
  await Promise.all(roots.splice(0).map(root => rm(root, { recursive: true, force: true })))
})

describe('safe media paths', () => {
  it('rejects traversal, absolute, NUL, case, Unicode, and symlink escapes', async () => {
    const root = await fixtureRoot()
    const outside = await fixtureRoot()
    await mkdir(join(root, 'Movies'))
    await mkdir(join(root, 'Café'))
    await symlink(outside, join(root, 'escape'))
    for (const candidate of ['../outside', '/etc/passwd', 'folder\0name', 'Movies/../outside']) {
      await expect(safeUnder(root, candidate)).rejects.toMatchObject({ code: expect.stringMatching(/INVALID_INPUT|POLICY_DENIED/) })
    }
    await expect(safeUnder(root, 'movies/item')).rejects.toMatchObject({ code: 'CONFLICT' })
    await expect(safeUnder(root, 'Cafe\u0301/item')).rejects.toMatchObject({ code: 'CONFLICT' })
    await expect(safeUnder(root, 'escape/item')).rejects.toMatchObject({ code: 'POLICY_DENIED' })
  })

  it('detects target and realpath changes after preview', async () => {
    const root = await fixtureRoot()
    await mkdir(join(root, 'Movies'))
    const target = join(root, 'Movies', 'item.strm')
    await writeFile(target, 'first')
    const snapshot = await capturePath(root, 'Movies/item.strm')
    await expect(assertPathUnchanged(snapshot)).resolves.toBeUndefined()
    await writeFile(target, 'changed content')
    await expect(assertPathUnchanged(snapshot)).rejects.toMatchObject({ code: 'CONFLICT' })
  })

  it('detects recursive tree changes and rejects in-root symlink snapshots', async () => {
    const root = await fixtureRoot()
    await mkdir(join(root, 'Series', 'Season 01'), { recursive: true })
    const episode = join(root, 'Series', 'Season 01', 'E01.strm')
    await writeFile(episode, '/media/Shows/E01.mkv\n')
    const snapshot = await capturePath(root, 'Series')
    const treeHash = await captureTreeHash(snapshot)
    await expect(assertTreeUnchanged(snapshot, treeHash)).resolves.toBeUndefined()
    await writeFile(episode, '/media/Shows/E01.changed.mkv\n')
    await expect(assertTreeUnchanged(snapshot, treeHash)).rejects.toMatchObject({ code: 'CONFLICT' })
    await symlink(join(root, 'Series'), join(root, 'Series link'))
    await expect(capturePath(root, 'Series link')).rejects.toMatchObject({ code: 'POLICY_DENIED' })
  })

  it('removes only unchanged snapshots and preserves container path contracts', async () => {
    const root = await fixtureRoot()
    const strm = await fixtureRoot()
    await mkdir(join(root, 'Movies'))
    await writeFile(join(root, 'Movies', 'delete.mkv'), 'fixture')
    const snapshot = await capturePath(root, 'Movies/delete.mkv')
    await removeSnapshotted(snapshot)
    await expect(readFile(join(root, 'Movies', 'delete.mkv'))).rejects.toMatchObject({ code: 'ENOENT' })
    expect(mediaPath('Movies', 'Folder/Movie.mkv')).toBe('/media/Movies/Folder/Movie.mkv')
    expect(embyLibraryPath('Movies')).toBe('/strm/Movies')
    const written = await writeStrm(strm, 'Movies', 'Folder/Movie.mkv')
    expect(await readFile(written, 'utf8')).toBe('/media/Movies/Folder/Movie.mkv\n')
    expect(strmRelativePath('Folder/.2160p.BluRay.mkv')).toBe('Folder/_2160p.BluRay.strm')
    const hiddenWritten = await writeStrm(strm, 'Movies', 'Folder/.2160p.BluRay.mkv')
    expect(hiddenWritten.endsWith('/Movies/Folder/_2160p.BluRay.strm')).toBe(true)
    expect(await readFile(hiddenWritten, 'utf8')).toBe('/media/Movies/Folder/.2160p.BluRay.mkv\n')
    expect((await stat(join(strm, 'Movies', 'Folder'))).mode & 0o777).toBe(0o755)
    expect((await stat(written)).mode & 0o777).toBe(0o644)
    await chmod(join(strm, 'Movies', 'Folder'), 0o700)
    await chmod(written, 0o600)
    await expect(writeStrm(strm, 'Movies', 'Folder/Movie.mkv')).rejects.toMatchObject({ code: 'EEXIST' })
    expect((await stat(join(strm, 'Movies', 'Folder'))).mode & 0o777).toBe(0o755)
    expect((await stat(written)).mode & 0o777).toBe(0o644)
  })
})
