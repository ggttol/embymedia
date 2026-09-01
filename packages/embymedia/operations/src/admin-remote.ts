import { Remote, TypertRemoteService } from '@deepseek-ai/dsh-typert-protocol'
import type { Context } from '@deepseek-ai/cordis'
import type { JsonValue } from './schemas.ts'

export interface EmbymediaAdminSource {
  adminSnapshot(signal: AbortSignal): Promise<JsonValue>
  adminCredentialCheck(id: string, signal: AbortSignal): Promise<JsonValue>
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
}
