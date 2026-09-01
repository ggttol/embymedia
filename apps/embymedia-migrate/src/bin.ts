#!/usr/bin/env node
import { Command } from 'commander'
import { importState, inventory, verifyState } from './migration.ts'

interface InventoryOptions {
  databaseUrlFile: string
  dshHome: string
  output: string
}

interface ImportOptions {
  databaseUrlFile: string
  targetUrlFile: string
  deepseekApiKeyFile: string
  dshHome: string
  report: string
}

interface VerifyOptions {
  source: string
  targetUrlFile: string
  dshHome: string
  report: string
}

const program = new Command()
  .name('embymedia-migrate')
  .description('Inventory, import, and verify EmbyMedia state without printing secrets')
  .showHelpAfterError()

program.command('inventory')
  .requiredOption('--database-url-file <path>')
  .requiredOption('--dsh-home <path>')
  .requiredOption('--output <path>')
  .action(async (options: InventoryOptions) => {
    const report = await inventory(options)
    process.stdout.write(`${JSON.stringify({
      output: options.output,
      tables: report.tables,
      activeSchedules: report.activeSchedules,
      runningTasks: report.runningTasks,
    })}\n`)
  })

program.command('import')
  .requiredOption('--database-url-file <path>')
  .requiredOption('--target-url-file <path>')
  .requiredOption('--deepseek-api-key-file <path>')
  .requiredOption('--dsh-home <path>')
  .requiredOption('--report <path>')
  .action(async (options: ImportOptions) => {
    const report = await importState(options)
    process.stdout.write(`${JSON.stringify({
      report: options.report,
      copied: report.copied,
      credentialRecords: report.credentialRecords,
      deepseekConfigured: report.deepseekConfigured,
    })}\n`)
  })

program.command('verify')
  .requiredOption('--source <path>')
  .requiredOption('--target-url-file <path>')
  .requiredOption('--dsh-home <path>')
  .requiredOption('--report <path>')
  .action(async (options: VerifyOptions) => {
    const report = await verifyState(options)
    process.stdout.write(`${JSON.stringify({ report: options.report, ok: report.ok, failures: report.failures })}\n`)
    if (!report.ok) process.exitCode = 1
  })

await program.parseAsync(process.argv)
