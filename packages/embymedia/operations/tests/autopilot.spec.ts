import { describe, expect, it, vi } from 'vitest'
import { SmartActionAutopilot, type AutopilotAuthorization } from '../src/autopilot.ts'
import type { SmartAction, SmartActionEngine } from '../src/domain/smart-actions.ts'

function action(id: string, type: SmartAction['type'], risk: SmartAction['risk'], libraryId: string): SmartAction {
  const now = new Date().toISOString()
  return {
    id, type, risk, status: 'ready', subject: { kind: 'item', id, label: id }, title: id, summary: id,
    recommendation: {}, evidence: { libraryId }, plan: {}, confidenceScore: 90, confidence: 'high', verification: {},
    createdAt: now, updatedAt: now,
  }
}

const authorization: AutopilotAuthorization = {
  allowedTypes: ['poster_fix', 'metadata_refresh', 'library_scan'],
  libraryIds: ['library-1'],
  policyHash: 'approved-policy-hash',
  maxActions: 2,
  maxRisk: 'medium',
}

describe('Smart Action autopilot constraints', () => {
  it('rejects destructive, transfer, move, archive, and unscoped authorization', () => {
    const autopilot = new SmartActionAutopilot({} as SmartActionEngine, async () => authorization.policyHash)
    for (const type of ['dedup_remove_old', 'transfer_add_new', 'transfer_update_series', 'archive_series', 'cleanup_empty_folder'] as const) {
      expect(() =>{  autopilot.validate({ ...authorization, allowedTypes: [type] }) }).toThrow(/non-destructive/)
    }
    expect(() =>{  autopilot.validate({ ...authorization, libraryIds: [] }) }).toThrow(/library scope/)
    expect(() =>{  autopilot.validate({ ...authorization, maxActions: 101 }) }).toThrow(/maxActions/)
  })

  it('executes exactly the three medium non-destructive types within fixed scope and limit', async () => {
    const execute = vi.fn(async (_id: string, _signal: AbortSignal) => ({} as SmartAction))
    const engine = {
      list: async () => [
        action('poster-1', 'poster_fix', 'medium', 'library-1'),
        action('metadata-1', 'metadata_refresh', 'medium', 'library-1'),
        action('scan-1', 'library_scan', 'medium', 'library-1'),
        action('wrong-scope', 'poster_fix', 'medium', 'library-2'),
        action('destructive', 'dedup_remove_old', 'critical', 'library-1'),
      ],
      execute,
    } as unknown as SmartActionEngine
    const autopilot = new SmartActionAutopilot(engine, async () => authorization.policyHash)
    await expect(autopilot.execute(authorization, new AbortController().signal)).resolves.toEqual({
      selected: 2,
      completed: ['poster-1', 'metadata-1'],
      policyHash: authorization.policyHash,
    })
    expect(execute.mock.calls.map(call => call[0])).toEqual(['poster-1', 'metadata-1'])
  })

  it('fails closed when the policy hash drifts after standing approval', async () => {
    const list = vi.fn()
    const autopilot = new SmartActionAutopilot({ list } as unknown as SmartActionEngine, async () => 'changed')
    await expect(autopilot.execute(authorization, new AbortController().signal)).rejects.toMatchObject({ code: 'CONFLICT' })
    expect(list).not.toHaveBeenCalled()
  })
})
