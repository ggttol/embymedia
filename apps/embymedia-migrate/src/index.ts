export const MIGRATION_COMMANDS = ['inventory', 'import', 'verify'] as const
export type MigrationCommand = (typeof MIGRATION_COMMANDS)[number]

export interface DshHomeOptions {
  readonly dshHome: string
}

export interface RedactedMigrationReport {
  readonly command: MigrationCommand
  readonly dshHome: string
  readonly configuredCredentialIds: readonly string[]
  readonly deepseekConfigured: boolean
  readonly warnings: readonly string[]
}

export * from './migration.ts'
export type * from './types.ts'
