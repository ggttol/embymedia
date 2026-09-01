import type { ControlRequest, ControlResponse } from '@embymedia/dsh-operations/schemas'

export const CONTROL_SOCKET = '/run/embymedia-control/control.sock' as const
export const WEBHOOK_TOML = '/srv/embymedia/data/clouddrive/config/webhooks/webhook.toml' as const

export type { ControlRequest, ControlResponse }

export * from './helper.ts'
export * from './server.ts'
