import { createHash } from 'node:crypto'
import { createReadStream } from 'node:fs'
import { chmod, lstat, mkdir, readdir, realpath, rename, rm, writeFile } from 'node:fs/promises'
import { isAbsolute, posix, relative, resolve, sep, win32 } from 'node:path'
import { EmbymediaError } from '../errors.ts'

const MAX_TREE_ENTRIES = 10_000
const MAX_TREE_FILE_BYTES = 256 * 1024 * 1024

export interface PathSnapshot {
  readonly root: string
  readonly rootRealpath: string
  readonly relativePath: string
  readonly absolutePath: string
  readonly realpath: string
  readonly inode: string
  readonly mtimeMs: number
  readonly size: number
  readonly type: 'file' | 'directory'
}

function pathInside(root: string, candidate: string): boolean {
  const fromRoot = relative(root, candidate)
  return fromRoot === '' || (!fromRoot.startsWith(`..${sep}`) && fromRoot !== '..' && !isAbsolute(fromRoot))
}

function relativeSegments(input: string): string[] {
  if (input.includes('\0')) throw new EmbymediaError('INVALID_INPUT', 'path contains NUL')
  if (isAbsolute(input) || win32.isAbsolute(input)) throw new EmbymediaError('POLICY_DENIED', 'absolute user paths are forbidden')
  const segments = input.split(/[\\/]+/)
  if (segments.length === 0 || segments.every(segment => segment.length === 0)) throw new EmbymediaError('INVALID_INPUT', 'relative path is required')
  for (const segment of segments) {
    if (segment.length === 0 || segment === '.' || segment === '..') throw new EmbymediaError('POLICY_DENIED', 'path traversal segments are forbidden')
    if (segment.normalize('NFC') !== segment) throw new EmbymediaError('CONFLICT', 'path must use NFC Unicode normalization')
  }
  return segments
}

async function assertNoNormalizationCollision(parent: string, segment: string): Promise<void> {
  let entries: string[]
  try {
    entries = await readdir(parent)
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') return
    throw error
  }
  const folded = segment.normalize('NFC').toLocaleLowerCase('en-US')
  for (const entry of entries) {
    if (entry === segment) continue
    if (entry.normalize('NFC').toLocaleLowerCase('en-US') === folded) {
      throw new EmbymediaError('CONFLICT', 'path collides by case or Unicode normalization', { segment })
    }
  }
}

/** Resolve a user-relative path under one canonical root without following an escaping symlink. */
export async function safeUnder(root: string, input: string): Promise<string> {
  const rootAbsolute = resolve(root)
  const rootCanonical = await realpath(rootAbsolute)
  const segments = relativeSegments(input)
  let current = rootCanonical
  for (const segment of segments) {
    await assertNoNormalizationCollision(current, segment)
    const candidate = resolve(current, segment)
    if (!pathInside(rootCanonical, candidate)) throw new EmbymediaError('POLICY_DENIED', 'path escapes configured root')
    try {
      const metadata = await lstat(candidate)
      if (metadata.isSymbolicLink()) {
        const linked = await realpath(candidate)
        if (!pathInside(rootCanonical, linked)) throw new EmbymediaError('POLICY_DENIED', 'symlink escapes configured root')
        current = linked
      } else {
        current = candidate
      }
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
      current = candidate
    }
  }
  if (!pathInside(rootCanonical, current) || current === rootCanonical) {
    throw new EmbymediaError('POLICY_DENIED', 'path must identify a child of configured root')
  }
  return current
}

export async function capturePath(root: string, input: string): Promise<PathSnapshot> {
  const rootRealpath = await realpath(resolve(root))
  const absolutePath = await safeUnder(root, input)
  const logicalPath = resolve(rootRealpath, ...relativeSegments(input))
  if (absolutePath !== logicalPath) throw new EmbymediaError('POLICY_DENIED', 'snapshotted paths must not contain symlinks')
  let targetRealpath: string
  let metadata
  try {
    targetRealpath = await realpath(absolutePath)
    metadata = await lstat(absolutePath)
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') throw new EmbymediaError('NOT_FOUND', 'path does not exist')
    throw error
  }
  if (!pathInside(rootRealpath, targetRealpath)) throw new EmbymediaError('POLICY_DENIED', 'resolved path escapes configured root')
  if (!metadata.isFile() && !metadata.isDirectory()) throw new EmbymediaError('POLICY_DENIED', 'path type is not supported')
  return {
    root: resolve(root),
    rootRealpath,
    relativePath: relativeSegments(input).join('/'),
    absolutePath,
    realpath: targetRealpath,
    inode: String(metadata.ino),
    mtimeMs: metadata.mtimeMs,
    size: metadata.size,
    type: metadata.isDirectory() ? 'directory' : 'file',
  }
}

export async function assertPathUnchanged(snapshot: PathSnapshot): Promise<void> {
  const rootRealpath = await realpath(snapshot.root)
  if (rootRealpath !== snapshot.rootRealpath) throw new EmbymediaError('CONFLICT', 'media root realpath changed after preview')
  const absolutePath = await safeUnder(snapshot.root, snapshot.relativePath)
  let metadata
  let targetRealpath: string
  try {
    metadata = await lstat(absolutePath)
    targetRealpath = await realpath(absolutePath)
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') throw new EmbymediaError('CONFLICT', 'target disappeared after preview')
    throw error
  }
  if (absolutePath !== snapshot.absolutePath || targetRealpath !== snapshot.realpath
    || String(metadata.ino) !== snapshot.inode || metadata.mtimeMs !== snapshot.mtimeMs || metadata.size !== snapshot.size) {
    throw new EmbymediaError('CONFLICT', 'target changed after preview')
  }
}

/** Hash every descendant path, metadata record, and file byte under one unchanged directory snapshot. */
export async function captureTreeHash(snapshot: PathSnapshot): Promise<string> {
  await assertPathUnchanged(snapshot)
  if (snapshot.type !== 'directory') throw new EmbymediaError('INVALID_INPUT', 'tree hashes require a directory snapshot')
  const records: Array<readonly [string, 'file' | 'directory', string, number, number, string?]> = []
  const stack = [snapshot.absolutePath]
  let entries = 0
  let fileBytes = 0
  while (stack.length > 0) {
    const directory = stack.pop()
    if (directory === undefined) continue
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      if (++entries > MAX_TREE_ENTRIES) throw new EmbymediaError('POLICY_DENIED', 'snapshotted tree exceeds the 10000-entry cleanup limit')
      const path = resolve(directory, entry.name)
      const metadata = await lstat(path)
      if (metadata.isSymbolicLink()) throw new EmbymediaError('POLICY_DENIED', 'snapshotted trees must not contain symlinks')
      const relativePath = relative(snapshot.absolutePath, path).split(sep).join('/')
      if (metadata.isDirectory()) {
        records.push([relativePath, 'directory', String(metadata.ino), metadata.mtimeMs, metadata.size])
        stack.push(path)
      } else if (metadata.isFile()) {
        fileBytes += metadata.size
        if (fileBytes > MAX_TREE_FILE_BYTES) throw new EmbymediaError('POLICY_DENIED', 'snapshotted tree exceeds the 256 MiB cleanup manifest limit')
        const hash = createHash('sha256')
        for await (const chunk of createReadStream(path)) hash.update(chunk)
        records.push([relativePath, 'file', String(metadata.ino), metadata.mtimeMs, metadata.size, hash.digest('hex')])
      } else {
        throw new EmbymediaError('POLICY_DENIED', 'snapshotted tree contains an unsupported path type')
      }
    }
  }
  records.sort((left, right) => left[0].localeCompare(right[0]))
  return createHash('sha256').update(JSON.stringify(records)).digest('hex')
}

/** Reject a directory whose recursive manifest changed after preview. */
export async function assertTreeUnchanged(snapshot: PathSnapshot, expectedHash: string): Promise<void> {
  if (await captureTreeHash(snapshot) !== expectedHash) throw new EmbymediaError('CONFLICT', 'snapshotted directory tree changed after preview')
}

export async function removeSnapshotted(snapshot: PathSnapshot): Promise<void> {
  await assertPathUnchanged(snapshot)
  await rm(snapshot.absolutePath, { recursive: snapshot.type === 'directory', force: false })
}

export async function moveSnapshotted(snapshot: PathSnapshot, destinationRoot: string, destinationRelative: string): Promise<string> {
  await assertPathUnchanged(snapshot)
  const destination = await safeUnder(destinationRoot, destinationRelative)
  try {
    await lstat(destination)
    throw new EmbymediaError('CONFLICT', 'move destination already exists')
  } catch (error) {
    if (error instanceof EmbymediaError) throw error
    if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
  }
  await mkdir(resolve(destination, '..'), { recursive: true })
  const recheckedDestination = await safeUnder(destinationRoot, destinationRelative)
  if (recheckedDestination !== destination) throw new EmbymediaError('CONFLICT', 'move destination changed during execution')
  await rename(snapshot.absolutePath, destination)
  return destination
}

export function mediaPath(library: string, relativePath: string): string {
  const librarySegment = relativeSegments(library).join('/')
  const mediaSegments = relativeSegments(relativePath)
  return posix.join('/media', librarySegment, ...mediaSegments)
}

export function embyLibraryPath(library: string): string {
  return posix.join('/strm', relativeSegments(library).join('/'))
}

/** Derive one Emby-visible STRM path while preserving the original media target. */
export function strmRelativePath(relativeMediaPath: string): string {
  const normalizedMedia = relativeSegments(relativeMediaPath).join('/')
  const mediaExtension = posix.extname(normalizedMedia)
  const stem = mediaExtension.length === 0 ? normalizedMedia : normalizedMedia.slice(0, -mediaExtension.length)
  const directory = posix.dirname(stem)
  const basename = posix.basename(stem)
  const visibleBasename = basename.startsWith('.') ? `_${basename.slice(1) || 'media'}` : basename
  return directory === '.' ? `${visibleBasename}.strm` : posix.join(directory, `${visibleBasename}.strm`)
}

/** Create a scan-local STRM writer that validates each output directory once. */
export async function createStrmBatchWriter(strmRoot: string, library: string): Promise<(relativeMediaPath: string, mediaLibrary?: string, relativeOutputPath?: string) => Promise<string>> {
  const canonicalRoot = await realpath(resolve(strmRoot))
  const librarySegment = relativeSegments(library).join('/')
  const parents = new Map<string, { readonly path: string; readonly names: Map<string, string> }>()
  return async (relativeMediaPath, mediaLibrary = library, relativeOutputPath) => {
    const relativeStrm = posix.join(librarySegment, relativeOutputPath ?? strmRelativePath(relativeMediaPath))
    const relativeParent = posix.dirname(relativeStrm)
    let cached = parents.get(relativeParent)
    if (cached === undefined) {
      const parent = await safeUnder(strmRoot, relativeParent)
      await mkdir(parent, { recursive: true, mode: 0o755 })
      const parentRealpath = await realpath(parent)
      if (!pathInside(canonicalRoot, parentRealpath)) throw new EmbymediaError('POLICY_DENIED', 'STRM output directory escapes configured root')
      let current = canonicalRoot
      for (const segment of relative(canonicalRoot, parentRealpath).split(sep).filter(Boolean)) {
        current = resolve(current, segment)
        await chmod(current, 0o755)
      }
      const names = new Map((await readdir(parentRealpath)).map(name => [name.normalize('NFC').toLocaleLowerCase('en-US'), name]))
      cached = { path: parentRealpath, names }
      parents.set(relativeParent, cached)
    }
    const filename = posix.basename(relativeStrm)
    const folded = filename.normalize('NFC').toLocaleLowerCase('en-US')
    const collision = cached.names.get(folded)
    if (collision !== undefined && collision !== filename) throw new EmbymediaError('CONFLICT', 'path collides by case or Unicode normalization', { segment: filename })
    const target = resolve(cached.path, filename)
    if (!pathInside(canonicalRoot, target)) throw new EmbymediaError('POLICY_DENIED', 'STRM output escapes configured root')
    try {
      await writeFile(target, `${mediaPath(mediaLibrary, relativeMediaPath)}\n`, { encoding: 'utf8', flag: 'wx', mode: 0o644 })
      cached.names.set(folded, filename)
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'EEXIST') await chmod(target, 0o644)
      throw error
    }
    await chmod(target, 0o644)
    return target
  }
}

export async function writeStrm(strmRoot: string, library: string, relativeMediaPath: string, mediaLibrary = library): Promise<string> {
  const strmMediaPath = strmRelativePath(relativeMediaPath)
  const relativeStrm = posix.join(relativeSegments(library).join('/'), strmMediaPath)
  const target = await safeUnder(strmRoot, relativeStrm)
  const parent = resolve(target, '..')
  await mkdir(parent, { recursive: true, mode: 0o755 })
  const canonicalRoot = await realpath(resolve(strmRoot))
  let current = canonicalRoot
  for (const segment of relative(canonicalRoot, parent).split(sep).filter(Boolean)) {
    current = resolve(current, segment)
    await chmod(current, 0o755)
  }
  try {
    await writeFile(target, `${mediaPath(mediaLibrary, relativeMediaPath)}\n`, { encoding: 'utf8', flag: 'wx', mode: 0o644 })
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'EEXIST') await chmod(target, 0o644)
    throw error
  }
  await chmod(target, 0o644)
  return target
}
