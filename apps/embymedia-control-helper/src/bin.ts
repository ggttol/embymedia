#!/usr/bin/env node
import { Command } from 'commander'
import { WebhookControlHelper } from './helper.ts'
import { listen } from './server.ts'

interface Options {
  readonly socket: string
  readonly compose: string
  readonly envFile: string
  readonly webhookToml: string
}

const program = new Command()
  .name('embymedia-control-helper')
  .requiredOption('--socket <path>')
  .requiredOption('--compose <path>')
  .requiredOption('--env-file <path>')
  .requiredOption('--webhook-toml <path>')

program.parse()
const options = program.opts<Options>()
const helper = new WebhookControlHelper({
  compose: options.compose,
  envFile: options.envFile,
  webhookToml: options.webhookToml,
  mountPath: '/srv/embymedia/data/clouddrive/CloudNAS/CloudDrive',
  canaryPath: '/srv/embymedia/data/clouddrive/CloudNAS/CloudDrive/.embymedia-health-canary',
})
const server = await listen(options.socket, helper)
const stopping = Promise.withResolvers<void>()
const stop = (): void => {
  server.close((error) => { if (error) stopping.reject(error); else stopping.resolve() })
}
process.once('SIGTERM', stop)
process.once('SIGINT', stop)
await stopping.promise
