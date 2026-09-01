// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ToolCallViewProps } from '@deepseek-ai/dsh-client-ui-tool/client'
import { OperationCard } from '../src/client/OperationCard.tsx'
import { EMBYMEDIA_CARD_TOOLS } from '../src/tools.ts'

afterEach(cleanup)

function props(args: object, result: object | string, toolName = 'embymedia_plan'): ToolCallViewProps {
  const text = typeof result === 'string' ? result : JSON.stringify(result)
  return {
    callId: 'call-1',
    toolName,
    sessionId: 'session-1',
    block: {
      kind: 'tool-result',
      callId: 'call-1',
      call: { argsRaw: JSON.stringify(args), name: toolName },
      content: [{ type: 'text', text }],
      isError: false,
      subCalls: [],
    },
    openFile: vi.fn(),
  } as unknown as ToolCallViewProps
}

describe('Embymedia operation cards', () => {
  it('pins thirteen replay-stable keyed card names', () => {
    expect(EMBYMEDIA_CARD_TOOLS).toHaveLength(13)
    expect(new Set(EMBYMEDIA_CARD_TOOLS).size).toBe(13)
    expect(EMBYMEDIA_CARD_TOOLS.at(-3)).toBe('embymedia_plan')
    expect(EMBYMEDIA_CARD_TOOLS.at(-1)).toBe('embymedia_verify')
  })

  it('renders canonical plan facts entirely from the durable call/result slice', () => {
    render(<OperationCard {...props({ kind: 'media.delete' }, {
      schemaVersion: 1,
      id: 'plan-1',
      kind: 'media.delete',
      status: 'previewed',
      risk: 'critical',
      correlationId: 'correlation-1',
      targets: [{ id: 'item-1', label: 'Item One' }],
      confirmation: { kind: 'media.delete', targets: [{ id: 'item-1' }] },
    })} stageCredential={vi.fn()} />)
    expect(screen.getByRole('heading', { name: '删除媒体 / media.delete' })).toBeTruthy()
    expect(screen.getAllByText('严重风险 / critical')).toHaveLength(2)
    expect(screen.getAllByText('待审批 / previewed')).toHaveLength(2)
    expect(screen.getByText('批准内容')).toBeTruthy()
    expect(document.body.textContent).toContain('"操作类型 / kind": "删除媒体 / media.delete"')
  })

  it('stages ordinary credentials over the product Remote and clears the local value', async () => {
    const stage = vi.fn(async () => ({ ok: true as const }))
    render(<OperationCard {...props({ kind: 'config.credential_rotate' }, {
      schemaVersion: 1,
      id: 'plan-credential',
      kind: 'config.credential_rotate',
      status: 'previewed',
      risk: 'high',
      confirmation: { input: { credential: 'tmdb-api-key' } },
    })} stageCredential={stage} />)
    const input = screen.getByLabelText('TMDB API 密钥 / tmdb-api-key') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'sensitive-value' } })
    fireEvent.click(screen.getByRole('button', { name: '暂存凭据' }))
    await waitFor(() => { expect(stage).toHaveBeenCalled() })
    expect(stage.mock.calls[0]?.slice(0, 3)).toEqual(['session-1', 'plan-credential', 'sensitive-value'])
    await waitFor(() => { expect(input.value).toBe('') })
    expect(document.body.textContent).not.toContain('sensitive-value')
  })

  it('does not expose an input for generated webhook rotation and falls back on malformed payloads', () => {
    const { rerender } = render(<OperationCard {...props({}, {
      schemaVersion: 1,
      id: 'plan-webhook',
      kind: 'config.credential_rotate',
      status: 'previewed',
      risk: 'high',
      confirmation: { input: { credential: 'clouddrive-webhook-secret' } },
    })} stageCredential={vi.fn()} />)
    expect(screen.queryByPlaceholderText('仅暂存到 Host，不进入聊天记录')).toBeNull()
    rerender(<OperationCard {...props({}, 'not-json', 'embymedia_library')} stageCredential={vi.fn()} />)
    expect(screen.getByText('兼容视图 / Fallback')).toBeTruthy()
    expect(screen.getByText('not-json')).toBeTruthy()
  })
})
