import { EmbymediaError } from './errors.ts'

export interface PipelineStage<State> {
  readonly name: string
  readonly run: (state: State, signal: AbortSignal) => Promise<State>
}

/** Owns cooperative operations and does not settle cancellation until every operation has gone still. */
export class CancellationScope {
  private readonly controller = new AbortController()
  private readonly active = new Set<Promise<unknown>>()
  private closing = false

  constructor(parent?: AbortSignal) {
    if (parent !== undefined) {
      if (parent.aborted) this.controller.abort(parent.reason)
      else parent.addEventListener('abort', () => { this.controller.abort(parent.reason) }, { once: true })
    }
  }

  get signal(): AbortSignal {
    return this.controller.signal
  }

  run<T>(operation: (signal: AbortSignal) => Promise<T>): Promise<T> {
    if (this.closing || this.signal.aborted) return Promise.reject(new EmbymediaError('CANCELLED', 'operation scope is stopping'))
    const promise = operation(this.signal)
    this.active.add(promise)
    void promise.finally(() => { this.active.delete(promise) }).catch(() => {})
    return promise
  }

  async cancelAndWait(reason: unknown = new Error('operation scope cancelled')): Promise<void> {
    this.closing = true
    this.controller.abort(reason)
    await Promise.allSettled([...this.active])
  }
}

export async function runPipeline<State>(
  initial: State,
  stages: readonly PipelineStage<State>[],
  signal: AbortSignal,
  progress?: (stage: string, index: number, total: number) => Promise<void> | void,
): Promise<State> {
  let state = initial
  for (let index = 0; index < stages.length; index++) {
    signal.throwIfAborted()
    const stage = stages[index]!
    await progress?.(stage.name, index, stages.length)
    state = await stage.run(state, signal)
  }
  signal.throwIfAborted()
  return state
}

export function abortableSleep(milliseconds: number, signal: AbortSignal): Promise<void> {
  if (!Number.isFinite(milliseconds) || milliseconds < 0) throw new EmbymediaError('INVALID_INPUT', 'sleep duration must be non-negative')
  signal.throwIfAborted()
  return new Promise((resolve, reject) => {
    const timer = setTimeout(done, milliseconds)
    const abort = (): void => {
      clearTimeout(timer)
      signal.removeEventListener('abort', abort)
      reject(new EmbymediaError('CANCELLED', 'operation cancelled'))
    }
    function done(): void {
      signal.removeEventListener('abort', abort)
      resolve()
    }
    signal.addEventListener('abort', abort, { once: true })
  })
}
