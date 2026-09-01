import { EmbymediaError } from '../errors.ts'

interface Waiter {
  readonly resolve: (release: () => void) => void
  readonly reject: (error: unknown) => void
  readonly signal: AbortSignal
  readonly abort: () => void
}

export class Semaphore {
  private available: number
  private readonly waiters: Waiter[] = []

  constructor(readonly capacity: number) {
    if (!Number.isInteger(capacity) || capacity < 1) throw new EmbymediaError('INVALID_INPUT', 'semaphore capacity must be a positive integer')
    this.available = capacity
  }

  async run<T>(signal: AbortSignal, work: () => Promise<T>): Promise<T> {
    const release = await this.acquire(signal)
    try {
      return await work()
    } finally {
      release()
    }
  }

  acquire(signal: AbortSignal): Promise<() => void> {
    signal.throwIfAborted()
    if (this.available > 0) {
      this.available--
      return Promise.resolve(this.releaseOnce())
    }
    return new Promise<() => void>((resolve, reject) => {
      const waiter: Waiter = {
        resolve,
        reject,
        signal,
        abort: () => {
          const index = this.waiters.indexOf(waiter)
          if (index >= 0) this.waiters.splice(index, 1)
          reject(new EmbymediaError('CANCELLED', 'operation cancelled while waiting for concurrency slot'))
        },
      }
      signal.addEventListener('abort', waiter.abort, { once: true })
      this.waiters.push(waiter)
    })
  }

  private releaseOnce(): () => void {
    let released = false
    return () => {
      if (released) return
      released = true
      while (this.waiters.length > 0) {
        const waiter = this.waiters.shift()!
        waiter.signal.removeEventListener('abort', waiter.abort)
        if (waiter.signal.aborted) continue
        waiter.resolve(this.releaseOnce())
        return
      }
      this.available++
      if (this.available > this.capacity) throw new Error('semaphore release exceeded capacity')
    }
  }
}
