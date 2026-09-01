import type { CredentialId } from '@embymedia/dsh-operations/schemas'

export interface CredentialInventory {
  readonly id: CredentialId
  readonly configured: boolean
  readonly sha256?: string
}

export interface DatabaseInventory {
  readonly schemaVersion: 1
  readonly generatedAt: string
  readonly tables: Readonly<Record<string, number>>
  readonly activeSchedules: number
  readonly runningTasks: number
  readonly credentials: readonly CredentialInventory[]
  readonly secretSettingKeys: readonly string[]
}

export interface ImportReport {
  readonly schemaVersion: 1
  readonly importedAt: string
  readonly copied: Readonly<Record<string, number>>
  readonly credentialRecords: readonly CredentialId[]
  readonly deepseekConfigured: boolean
  readonly activeSchedules: 0
  readonly runningTasks: 0
  readonly warnings: readonly string[]
}

export interface VerifyReport {
  readonly schemaVersion: 1
  readonly verifiedAt: string
  readonly ok: boolean
  readonly dshHome: string
  readonly credentialsFile: string
  readonly credentialsMode: string
  readonly credentialsOwnerMatchesProcess: boolean
  readonly tableCountsMatch: boolean
  readonly activeSchedules: number
  readonly runningTasks: number
  readonly credentialRecords: Readonly<Record<CredentialId, { configured: boolean; writable: boolean }>>
  readonly deepseekConfigured: boolean
  readonly secretSettingKeys: readonly string[]
  readonly failures: readonly string[]
}
