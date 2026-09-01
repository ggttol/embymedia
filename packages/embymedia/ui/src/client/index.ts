import { createElement } from 'react'
import type { Context } from '@deepseek-ai/cordis'
import type {} from '@deepseek-ai/dsh-api-remotes/client'
import type {} from '@deepseek-ai/dsh-api-session-controller/client'
import type {} from '@deepseek-ai/dsh-client-ui-renderer/client'
import type { ToolCallViewProps } from '@deepseek-ai/dsh-client-ui-tool/client'
import type {} from '@deepseek-ai/dsh-client-ui-layout/client'
import { OperationCard } from './OperationCard.tsx'
import { EmbyWorkspace } from './EmbyWorkspace.tsx'
import { EMBYMEDIA_CARD_TOOLS } from '../tools.ts'

export const inject = [
  'slots', 'remote', 'remote.embymedia-admin', 'remote.embymedia-secrets', 'sessions',
]


interface SecretsRemote {
  readonly 'embymedia-secrets': {
    stage(
      sessionId: string,
      planId: string,
      value: string,
      signal: AbortSignal,
    ): Promise<
      | { readonly ok: true; readonly value: { readonly configured: true } }
      | { readonly ok: false; readonly error: { readonly message?: string } }
    >
  }
}

interface AdminRemote {
  readonly 'embymedia-admin': {
    snapshot(signal: AbortSignal): Promise<
      | { readonly ok: true; readonly value: unknown }
      | { readonly ok: false; readonly error: { readonly message?: string } }
    >
    credentialCheck(id: string, signal: AbortSignal): Promise<
      | { readonly ok: true; readonly value: unknown }
      | { readonly ok: false; readonly error: { readonly message?: string } }
    >
  }
}

interface ClientSessions {
  readonly list: { getSnapshot(): { readonly current?: string } }
  binding(sessionId: string): {
    readonly session: {
      prompt(content: readonly { readonly type: 'text'; readonly text: string }[], mode: 'queue'): Promise<{ readonly ok: boolean; readonly error?: { readonly message?: string } }>
    }
  } | undefined
}

export function apply(ctx: Context): void {
  const remote = ctx.remote as unknown as SecretsRemote
  const stageCredential = async (sessionId: string, planId: string, value: string, signal: AbortSignal) => {
    const result = await remote['embymedia-secrets'].stage(sessionId, planId, value, signal)
    return result.ok ? { ok: true } : { ok: false, error: result.error }
  }
  ctx.slots.inject('tool.call.toolview', function* () {
    for (const tool of EMBYMEDIA_CARD_TOOLS) {
      const Card = (props: ToolCallViewProps) => createElement(OperationCard, { ...props, stageCredential })
      yield ctx.slots.register({ name: 'tool.call.toolview', key: tool }, Card)
    }
  })

  const adminRemote = ctx.remote as unknown as AdminRemote
  const sessions = ctx.sessions as unknown as ClientSessions
  ctx.slots.inject('shell.overlay', () => ctx.slots.register({
    name: 'shell.overlay',
    id: 'embymedia-workspace',
    order: -100,
    inject: () => ({
      loadSnapshot: async (signal: AbortSignal) => {
        const admin = adminRemote['embymedia-admin']
        if (admin === undefined) throw new Error('EmbyMedia Admin Remote 尚未就绪')
        const result = await admin.snapshot(signal)
        if (!result.ok) throw new Error(result.error.message ?? 'EmbyMedia 状态读取失败')
        return result.value
      },
      checkCredential: async (id: string, signal: AbortSignal) => {
        const admin = adminRemote['embymedia-admin']
        if (admin === undefined) throw new Error('EmbyMedia Admin Remote 尚未就绪')
        const result = await admin.credentialCheck(id, signal)
        if (!result.ok) throw new Error(result.error.message ?? '凭据可用性检查失败')
        return result.value
      },
      sendPrompt: async (text: string) => {
        const current = sessions.list.getSnapshot().current
        if (current === undefined) throw new Error('请先在 DSH 中新建或选择一个会话')
        const session = sessions.binding(current)?.session
        if (session === undefined) throw new Error('当前 DSH 会话不可用')
        const result = await session.prompt([{ type: 'text', text }], 'queue')
        if (!result.ok) throw new Error(result.error?.message ?? '无法提交到 AI 对话')
      },
    }),
  }, EmbyWorkspace))
}

export { EMBYMEDIA_CARD_TOOLS }
