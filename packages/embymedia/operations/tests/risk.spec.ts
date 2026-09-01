import { describe, expect, it } from 'vitest'
import { AUTOPILOT_EXECUTOR_TYPES, assertExplicitTargets, assertStandingAuthorization, riskAtMost, SCHEDULE_RISKS } from '../src/risk.ts'
import { SCHEDULE_KINDS } from '../src/capabilities.ts'
import { SAFE_AUTO_OPERATION_KINDS } from '../src/execution.ts'

describe('risk and target policies', () => {
  it('pins all eight schedule risks at medium or below', () => {
    expect(Object.keys(SCHEDULE_RISKS).sort()).toEqual([...SCHEDULE_KINDS].sort())
    expect(SCHEDULE_KINDS.every(kind => riskAtMost(SCHEDULE_RISKS[kind], 'medium'))).toBe(true)
    expect(AUTOPILOT_EXECUTOR_TYPES).toEqual(['poster_fix', 'metadata_refresh', 'library_scan'])
  })

  it('auto-approves only the bounded non-destructive workflow allowlist', () => {
    expect([...SAFE_AUTO_OPERATION_KINDS].sort()).toEqual([
      'library.scan', 'metadata.refresh', 'poster.apply', 'poster.fix_batch', 'resource.add_new',
    ])
    for (const kind of ['media.delete', 'media.move', 'dedup.replace', 'user.policy_update', 'config.update', 'config.credential_rotate', 'schedule.upsert']) {
      expect(SAFE_AUTO_OPERATION_KINDS.has(kind as never)).toBe(false)
    }
  })

  it('allows standing authorization only for maintenance operations', () => {
    expect(() =>{  assertStandingAuthorization('scan_all', 'library.scan', 'medium') }).not.toThrow()
    expect(() =>{  assertStandingAuthorization('fix_posters_all', 'poster.fix_batch', 'medium') }).not.toThrow()
    for (const operation of ['media.delete', 'dedup.replace', 'resource.add_new', 'series.archive', 'user.policy_update', 'config.update'] as const) {
      expect(() =>{  assertStandingAuthorization('doctor', operation, 'medium') }).toThrow(/standing authorization/)
    }
  })

  it('requires explicit unique destructive targets and rejects hidden summaries', () => {
    expect(() =>{  assertExplicitTargets([], true) }).toThrow(/explicit targets/)
    expect(() =>{  assertExplicitTargets([{ id: '1', label: 'Item 1' }, { id: '1', label: 'Item 1 again' }], true) }).toThrow(/duplicate target/)
    expect(() =>{  assertExplicitTargets([{ id: '1', label: 'Item 1' }, { id: 'rest', label: '其余 27 项' }], true) }).toThrow(/hide destructive/)
    expect(() =>{  assertExplicitTargets(Array.from({ length: 101 }, (_, index) => ({ id: String(index), label: `Item ${String(index)}` })), true) }).toThrow(/at most 100/)
    expect(() =>{  assertExplicitTargets([{ id: '1', label: 'Concrete Item' }], true) }).not.toThrow()
  })
})
