import { describe, expect, it, vi } from 'vitest'
import { abortableSleep, CancellationScope, runPipeline } from '../src/cancellation.ts'
import { EmbymediaError } from '../src/errors.ts'

describe('cancellation and errors', () => {
  it('waits for in-process work to become still before cancellation settles', async () => {
    const scope = new CancellationScope()
    const stopped = vi.fn()
    const running = scope.run(async (signal) => {
      try {
        await abortableSleep(10_000, signal)
      } finally {
        await new Promise(resolve => setTimeout(resolve, 10))
        stopped()
      }
    })
    await scope.cancelAndWait()
    expect(stopped).toHaveBeenCalledOnce()
    await expect(running).rejects.toMatchObject({ code: 'CANCELLED' })
    await expect(scope.run(() => Promise.resolve('late'))).rejects.toMatchObject({ code: 'CANCELLED' })
  })

  it('propagates one signal through deterministic pipeline stages', async () => {
    const signals: AbortSignal[] = []
    const result = await runPipeline(1, [
      { name: 'first', run: async (state, signal) => { signals.push(signal); return state + 1 } },
      { name: 'second', run: async (state, signal) => { signals.push(signal); return state * 3 } },
    ], new AbortController().signal)
    expect(result).toBe(6)
    expect(signals[0]).toBe(signals[1])
  })

  it('removes secret-shaped fields from actionable error detail', () => {
    const error = new EmbymediaError('UPSTREAM_UNAVAILABLE', 'request failed', {
      upstream: 'fixture',
      status: 503,
      token: 'must-not-leak',
      nested: { password: 'must-not-leak', retryAfter: 10 },
    })
    const serialized = JSON.stringify(error.toJSON())
    expect(serialized).not.toContain('must-not-leak')
    expect(error.detail).toEqual({ upstream: 'fixture', status: 503, nested: { retryAfter: 10 } })
  })
})
