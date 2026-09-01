import { readFileSync, readdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import * as yaml from 'js-yaml'
import { entryListSchema } from '@deepseek-ai/cordis-plugin-include'
import { describe, expect, it } from 'vitest'

const root = fileURLToPath(new URL('..', import.meta.url))
const repo = resolve(root, '../../..')

interface PatchRow {
  readonly id?: string
  readonly name?: string
  readonly disabled?: boolean
  readonly config?: Record<string, unknown>
  readonly insert?: unknown[]
}

function rows(path: string): PatchRow[] {
  return yaml.load(readFileSync(path, 'utf8'), { schema: entryListSchema }) as PatchRow[]
}

describe('Emby operator preset', () => {
  it('contains only the persona, product tools, skills, and ask surface', () => {
    const composition = rows(resolve(root, 'presets/emby-operator/agent.cordis.yml'))
    expect(composition.map(row => row.name)).toEqual([
      '@deepseek-ai/dsh-persona',
      '@embymedia/dsh-operations/tools',
      '@deepseek-ai/dsh-skill-filesystem',
      '@deepseek-ai/dsh-tool-skill',
      '@deepseek-ai/dsh-tool-ask-user',
    ])
    const text = JSON.stringify(composition)
    for (const forbidden of ['tool-bash', 'tool-fs', 'web-fetch', 'mcp', 'cordis-host-runner', 'subagent', 'workflow']) {
      expect(text).not.toContain(forbidden)
    }
  })

  it('routes missing episodes through in-place updates or verified ended-Series replacement', () => {
    const composition = rows(resolve(root, 'presets/emby-operator/agent.cordis.yml'))
    const rawPersona = composition.find(row => row.id === 'persona')?.config?.text
    const persona = typeof rawPersona === 'string' ? rawPersona : ''
    expect(persona).toContain('gaps_summary 和 resource_plan')
    expect(persona).toContain('{libraryId,seriesId,numberingMode?,candidate,requestedEpisodes:[{season,episode,absolute?}]}')
    expect(persona).toContain('tmdbStatus=Ended')
    expect(persona).toContain('新 Series missingCount=0')
    expect(persona).toContain('另建 dedup.delete critical 计划')
    expect(persona).toContain('替换验证完成前绝不删除旧资源')
    expect(persona).toContain('{candidate:{candidateId,targetCid},scan:{libraryId,libraryName,mediaFolder}}')
    expect(persona).toContain('不得另建 library.scan')
    expect(persona).toContain('inspect_candidate')
    expect(persona).toContain('实际叶文件名证明')
    expect(persona).toContain('入库扫描后以新 Series missingCount=0 证明')
    expect(persona).toContain('最终验证不得依赖 115 访问码')
    const followup = readFileSync(resolve(root, 'presets/emby-operator/skills/emby-series-followup/SKILL.md'), 'utf8')
    expect(followup).toContain('a single-root Series pack may replace the root through two separate operations')
    expect(followup).toContain('never use it for an airing Series')
    expect(followup).toContain('new Series to have `missingCount=0`')
    expect(followup).toContain('do not include `seriesId`, `numberingMode`, or `requestedEpisodes`')
    expect(followup).toContain('do not create a separate `library.scan`')
    expect(followup).toContain('does not need to enumerate or prove every episode')
    expect(followup).toContain('completeness is established only from post-scan Emby facts')
    expect(followup).toContain('critical `dedup.delete` plan')
    expect(followup).toContain('embymedia_resource inspect_candidate')
    expect(followup).toContain('never derive coverage from a “complete pack” title')
    const onboarding = readFileSync(resolve(root, 'presets/emby-operator/skills/emby-resource-onboarding/SKILL.md'), 'utf8')
    expect(onboarding).toContain('`accessCodeStaged=true` is normal')
    expect(onboarding).toContain('never ask the user to provide it')
    expect(onboarding).toContain('`candidates:[{candidateId}]`')
    expect(onboarding).toContain('Never run one write per episode')
    expect(onboarding).toContain('Do not call `inspect_candidate` separately for every episode')
    const cleanup = readFileSync(resolve(root, 'presets/emby-operator/skills/emby-cleanup-dedup/SKILL.md'), 'utf8')
    expect(cleanup).toContain('Full removal always uses `media.delete`; it never invents a keeper')
    expect(cleanup).toContain('use at most two lookup calls')
    expect(cleanup).toContain('Do not create `library.scan` as deletion recovery')
    expect(cleanup).toContain('terminal `done`, `partial`, `failed`, or `cancelled` result ends the task immediately')
    expect(persona).toContain('不得复用旧任务的 itemId、planId、错误或结论')
  })

  it('locks the model, telemetry, preset trust roots, Host, and Client instances', () => {
    const patch = rows(resolve(repo, 'deploy/cordis/embymedia.patch.yml'))
    expect(patch.find(row => row.id === 'agent-default-model')?.config).toEqual({ provider: 'deepseek-official', model: 'deepseek-v4-flash' })
    expect(patch.find(row => row.id === 'llm-deepseek')?.config).toMatchObject({ apiKeyEnv: 'DEEPSEEK_API_KEY', thinking: 'enabled', reasoningEffort: 'high' })
    expect(patch.find(row => row.id === 'llm-pi-ai')?.config).toEqual({
      providers: { 'minimax-cn': { apiKeyEnv: 'MINIMAX_CN_API_KEY', models: [{ id: 'MiniMax-M3' }] } },
    })
    expect(JSON.stringify(patch)).not.toContain('sk-cp-')
    expect(patch.find(row => row.id === 'session-telemetry-otel')?.disabled).toBe(true)
    expect(patch.find(row => row.id === 'message-feedback')?.disabled).toBe(true)
    expect(patch.find(row => row.id === 'ui-message-feedback')?.disabled).toBe(true)
    expect(patch.find(row => row.id === 'agent-presets')?.config).toEqual({
      default: 'emby-operator',
      includeShippedRoot: false,
      includeUserRoot: false,
      roots: [{ path: { __jsExpr: 'process.env.EMBYMEDIA_PRESET_ROOT' }, trust: 'system' }],
    })
    expect(patch.find(row => row.id === 'web-runtime')?.config).toMatchObject({ trustedHosts: ['dsh.gaotao.cc', 'gaotao.cc'], surfaceContext: false })
    const inserted = patch.flatMap(row => row.insert ?? []) as Array<{ id: string; name: string }>
    expect(inserted.filter(row => row.name === '@embymedia/dsh-operations')).toHaveLength(1)
    expect(inserted.filter(row => row.name === '@embymedia/dsh-client-ui-operations')).toHaveLength(0)
  })

  it('declares the complete runtime dependency closure', () => {
    const manifest = JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8')) as { dependencies: Record<string, string> }
    expect(Object.keys(manifest.dependencies).sort()).toEqual([
      '@deepseek-ai/dsh-persona',
      '@deepseek-ai/dsh-skill-filesystem',
      '@deepseek-ai/dsh-tool-ask-user',
      '@deepseek-ai/dsh-tool-skill',
      '@embymedia/dsh-client-ui-operations',
      '@embymedia/dsh-operations',
    ])
  })
  it('ships exactly eight secret-free operation skills', () => {
    const skillsRoot = resolve(root, 'presets/emby-operator/skills')
    const names = readdirSync(skillsRoot).sort()
    expect(names).toEqual([
      'emby-cleanup-dedup',
      'emby-library-operations',
      'emby-metadata-repair',
      'emby-resource-onboarding',
      'emby-series-followup',
      'emby-user-policy',
      'embymedia-backup-restore',
      'embymedia-incident-diagnosis',
    ])
    for (const name of names) {
      const content = readFileSync(resolve(skillsRoot, name, 'SKILL.md'), 'utf8')
      expect(content).toContain(`name: ${name}`)
      expect(content).not.toMatch(/(?:api[_-]?key|cookie|secret)\s*[:=]\s*\S+/i)
      expect(content).not.toMatch(/`(?:bash|curl|systemctl|docker)\b/i)
    }
  })

})
