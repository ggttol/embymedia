import type { Context } from '@deepseek-ai/cordis'
import type { InvariantInstaller } from '@deepseek-ai/dsh-invariants'
import { EMBYMEDIA_CARD_TOOLS } from './tools.ts'

const PACKAGE_NAME = '@embymedia/dsh-client-ui-operations'
export const name = 'embymedia-client-ui-invariant'
export const inject = ['invariants']

const install: InvariantInstaller = (_ctx, fail) => {
  const seen: Record<string, true> = {}
  for (const tool of EMBYMEDIA_CARD_TOOLS) {
    if (seen[tool]) {
      fail('Embymedia client card roster contains duplicate tool names')
      return
    }
    seen[tool] = true
  }
  if (EMBYMEDIA_CARD_TOOLS.length !== 13) fail('Embymedia client must own exactly 13 tool cards')
}

export const apply = (ctx: Context): Promise<() => void> =>
  Promise.resolve(ctx.invariants.register(PACKAGE_NAME, install))
