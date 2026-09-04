import { Remote, TypertRemoteService } from '@deepseek-ai/dsh-typert-protocol'
import type { Context } from '@deepseek-ai/cordis'
import type { JsonValue } from './schemas.ts'

export interface EmbymediaAdminSource {
  adminSnapshot(signal: AbortSignal): Promise<JsonValue>
  adminCredentialCheck(id: string, signal: AbortSignal): Promise<JsonValue>
  adminCredentialSave(id: string, value: string, signal: AbortSignal): Promise<void>
}

export class EmbymediaAdminRemote extends TypertRemoteService {
  constructor(ctx: Context, private readonly source: EmbymediaAdminSource) {
    super(ctx, 'embymediaAdmin', { namespace: 'embymedia-admin' })
  }

  @Remote
  snapshot(signal: AbortSignal): Promise<JsonValue> {
    return this.source.adminSnapshot(signal)
  }

  @Remote
  credentialCheck(id: string, signal: AbortSignal): Promise<JsonValue> {
    return this.source.adminCredentialCheck(id, signal)
  }

  @Remote
  credentialSave(id: string, value: string, signal: AbortSignal): Promise<void> {
    return this.source.adminCredentialSave(id, value, signal)
  }
}
