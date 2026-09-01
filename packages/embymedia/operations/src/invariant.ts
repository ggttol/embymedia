import type { Context } from '@deepseek-ai/cordis'
import type { InvariantInstaller } from '@deepseek-ai/dsh-invariants'
import { CAPABILITIES, OPERATION_KINDS, PRODUCT_AREAS, SCHEDULE_KINDS, WIRE_TOOLS } from './capabilities.ts'

const PACKAGE_NAME = '@embymedia/dsh-operations'

export const name = 'embymedia-operations-invariant'
export const inject = ['invariants']

const install: InvariantInstaller = (_ctx, fail) => {
  const rosters: ReadonlyArray<readonly string[]> = [
    CAPABILITIES.map(capability => capability.capabilityId),
    OPERATION_KINDS,
    PRODUCT_AREAS,
    SCHEDULE_KINDS,
    WIRE_TOOLS,
  ]
  for (const roster of rosters) {
    const seen: Record<string, true> = {}
    for (const identifier of roster) {
      if (seen[identifier]) {
        fail('embymedia operation contract contains a duplicate stable identifier')
        return
      }
      seen[identifier] = true
    }
  }
}

export const apply = (ctx: Context): Promise<() => void> =>
  Promise.resolve(ctx.invariants.register(PACKAGE_NAME, install))
