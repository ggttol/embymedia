import type { SmartAction, SmartActionEngine, SmartActionType } from './domain/smart-actions.ts'
import { AUTOPILOT_EXECUTOR_TYPES, riskAtMost } from './risk.ts'
import { EmbymediaError } from './errors.ts'
import type { JsonValue } from './schemas.ts'

export interface AutopilotAuthorization {
  readonly allowedTypes: readonly SmartActionType[]
  readonly libraryIds: readonly string[]
  readonly policyHash: string
  readonly maxActions: number
  readonly maxRisk: 'medium'
}

function actionLibraryId(action: SmartAction): string | undefined {
  if (typeof action.evidence !== 'object' || action.evidence === null || Array.isArray(action.evidence)) return undefined
  const value = (action.evidence as Readonly<Record<string, JsonValue>>).libraryId
  return typeof value === 'string' ? value : undefined
}

export class SmartActionAutopilot {
  constructor(
    private readonly engine: SmartActionEngine,
    private readonly currentPolicyHash: () => Promise<string>,
  ) {}

  validate(authorization: AutopilotAuthorization): void {
    if (authorization.maxRisk !== 'medium') throw new EmbymediaError('POLICY_DENIED', 'autopilot maxRisk must be medium')
    if (!Number.isInteger(authorization.maxActions) || authorization.maxActions < 1 || authorization.maxActions > 100) {
      throw new EmbymediaError('INVALID_INPUT', 'autopilot maxActions must be 1..100')
    }
    const permitted = AUTOPILOT_EXECUTOR_TYPES as readonly string[]
    if (authorization.allowedTypes.length === 0 || authorization.allowedTypes.some(type => !permitted.includes(type))) {
      throw new EmbymediaError('POLICY_DENIED', 'autopilot type is not a non-destructive executor')
    }
    if (authorization.libraryIds.length === 0 || authorization.libraryIds.some(id => id.trim().length === 0)) {
      throw new EmbymediaError('POLICY_DENIED', 'autopilot requires explicit library scope')
    }
    if (authorization.policyHash.trim().length === 0) throw new EmbymediaError('POLICY_DENIED', 'autopilot requires a policy hash')
  }

  async execute(authorization: AutopilotAuthorization, signal: AbortSignal): Promise<JsonValue> {
    this.validate(authorization)
    if (await this.currentPolicyHash() !== authorization.policyHash) {
      throw new EmbymediaError('CONFLICT', 'autopilot policy hash changed after approval')
    }
    const allowedType: Record<string, true> = Object.fromEntries(authorization.allowedTypes.map(type => [type, true]))
    const allowedLibrary: Record<string, true> = Object.fromEntries(authorization.libraryIds.map(id => [id, true]))
    const candidates = (await this.engine.list('ready')).filter((action) => {
      const libraryId = actionLibraryId(action)
      return allowedType[action.type] && libraryId !== undefined && allowedLibrary[libraryId] && riskAtMost(action.risk, authorization.maxRisk)
    }).slice(0, authorization.maxActions)
    const completed: string[] = []
    for (const action of candidates) {
      signal.throwIfAborted()
      await this.engine.execute(action.id, signal)
      completed.push(action.id)
    }
    return { selected: candidates.length, completed, policyHash: authorization.policyHash }
  }
}
