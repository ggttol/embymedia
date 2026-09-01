import { createServer, type Server, type Socket } from 'node:net'
import { chmod, lstat, rm } from 'node:fs/promises'
import type { ControlRequest } from '@embymedia/dsh-operations/schemas'
import { WebhookControlHelper, redactedFailure } from './helper.ts'

const MAX_REQUEST_BYTES = 64 * 1024

export async function listen(socketPath: string, helper: WebhookControlHelper): Promise<Server> {
  try {
    const existing = await lstat(socketPath)
    if (!existing.isSocket()) throw new Error('control socket path exists and is not a socket')
    await rm(socketPath)
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
  }
  const server = createServer((socket) => { handleSocket(socket, helper) })
  const listening = Promise.withResolvers<void>()
  server.once('error', listening.reject)
  server.listen(socketPath, () => { listening.resolve() })
  await listening.promise
  await chmod(socketPath, 0o660)
  return server
}

function handleSocket(socket: Socket, helper: WebhookControlHelper): void {
  let data = ''
  let handled = false
  socket.setEncoding('utf8')
  socket.on('data', (chunk) => {
    if (handled) return
    data += chunk
    if (Buffer.byteLength(data) > MAX_REQUEST_BYTES) {
      handled = true
      socket.end(`${JSON.stringify(redactedFailure('', 'apply', new Error('request exceeds 64 KiB')))}\n`)
      return
    }
    const newline = data.indexOf('\n')
    if (newline === -1) return
    handled = true
    const text = data.slice(0, newline)
    void dispatch(text, helper).then((response) => {
      socket.end(`${JSON.stringify(response)}\n`)
    })
  })
  socket.on('error', () => {})
}

async function dispatch(text: string, helper: WebhookControlHelper) {
  let value: unknown
  try {
    value = JSON.parse(text) as unknown
  } catch (error) {
    return redactedFailure('', 'apply', error)
  }
  const record = typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Partial<ControlRequest> : {}
  const planId = typeof record.planId === 'string' ? record.planId : ''
  const phase = record.phase === 'restore' ? 'restore' : 'apply'
  try {
    return await helper.handle(value)
  } catch (error) {
    return redactedFailure(planId, phase, error)
  }
}
