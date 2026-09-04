// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { EmbyWorkspace } from '../src/client/EmbyWorkspace.tsx'

const snapshot = {
  schemaVersion: 1,
  generatedAt: '2026-08-30T10:00:00Z',
  health: { status: 'ok', writeMode: 'enabled', scheduler: 'disabled-for-cutover' },
  libraries: [{ id: 'lib-1', name: '综艺', collectionType: 'tvshows', locations: ['/strm/综艺'] }],
  users: [{ Id: 'user-1', Name: 'gaotao', Policy: { IsDisabled: false } }],
  tasks: { counts: { done: 3, running: 1 }, recent: [{ id: 'task-1', kind: 'library.scan', label: '扫描综艺', status: 'running', progress: 2, total: 5, status_text: '扫描中', updated_at: '2026-08-30T10:00:00Z' }] },
  schedules: [],
  audit: [{ id: 1, actor: 'gaotao', action: 'operation.verified', detail: {}, destructive: false, created_at: '2026-08-30T10:00:00Z' }],
  settings: { resource_api_base_url: 'http://gaotao.cc:8100', tmdb_base_url: 'https://api.themoviedb.org', tmdb_timeout_seconds: 45 },
  credentials: [
    { id: 'emby-api-key', configured: true, writable: true },
    { id: 'tmdb-api-key', configured: false, writable: true },
    { id: 'clouddrive-webhook-secret', configured: true, writable: true },
  ],
  operations: { supported: ['library.scan', 'resource.add_new', 'config.update', 'config.credential_rotate'] },
}

const checkCredential = async (id: string) => ({ id, ok: true, message: '检查通过', latencyMs: 1 })

beforeEach(() => { localStorage.clear() })
afterEach(cleanup)

describe('Emby operations workspace', () => {
  it('renders real status and navigates to the configuration matrix', async () => {
    render(<EmbyWorkspace loadSnapshot={vi.fn(async () => snapshot)} checkCredential={checkCredential} sendPrompt={vi.fn(async () => {})} />)
    expect(await screen.findByText('媒体信号脊柱')).toBeTruthy()
    expect(screen.getByText('CloudDrive2')).toBeTruthy()
    expect(screen.getByText('扫描综艺')).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: /配置中心/ }))
    expect(screen.getByRole('heading', { name: '配置与上游凭据' })).toBeTruthy()
    expect(screen.getByText('TMDB API Key')).toBeTruthy()
    expect(screen.getByText('未配置 / missing')).toBeTruthy()
    expect(screen.getByDisplayValue('http://gaotao.cc:8100')).toBeTruthy()
  })

  it('portals the full-screen workspace outside the width-constrained shell slot', async () => {
    const { container } = render(<EmbyWorkspace loadSnapshot={vi.fn(async () => snapshot)} checkCredential={checkCredential} sendPrompt={vi.fn(async () => {})} />)
    const workspace = await screen.findByRole('region', { name: 'Emby 运营工作台' })
    expect(container.contains(workspace)).toBe(false)
    expect(workspace.parentElement).toBe(document.body)
  })

  it('hands mutations to the current AI conversation instead of writing directly', async () => {
    const sendPrompt = vi.fn(async () => {})
    render(<EmbyWorkspace loadSnapshot={vi.fn(async () => snapshot)} checkCredential={checkCredential} sendPrompt={sendPrompt} />)
    await screen.findByText('媒体信号脊柱')
    fireEvent.click(screen.getByRole('button', { name: /资源入库/ }))
    fireEvent.click(screen.getByRole('button', { name: '添加 115 资源' }))
    await waitFor(() => { expect(sendPrompt).toHaveBeenCalledOnce() })
    expect(sendPrompt).toHaveBeenCalledWith(expect.stringContaining('resource.add_new'))
    expect(await screen.findByRole('button', { name: /打开 Emby 运营台/ })).toBeTruthy()
  })

  it('supports direct inline paste and save for credentials', async () => {
    const saveCredential = vi.fn(async () => {})
    render(<EmbyWorkspace loadSnapshot={vi.fn(async () => snapshot)} checkCredential={checkCredential} saveCredential={saveCredential} sendPrompt={vi.fn(async () => {})} />)
    await screen.findByText('媒体信号脊柱')
    fireEvent.click(screen.getByRole('button', { name: /配置中心/ }))
    const buttons = screen.getAllByRole('button', { name: '粘贴配置' })
    fireEvent.click(buttons[1]!)
    const input = screen.getByPlaceholderText(/直接在此粘贴新的/)
    fireEvent.change(input, { target: { value: 'new-secret-value' } })
    fireEvent.click(screen.getByRole('button', { name: '直接保存' }))
    await waitFor(() => { expect(saveCredential).toHaveBeenCalledOnce() })
    expect(saveCredential).toHaveBeenCalledWith('tmdb-api-key', 'new-secret-value', expect.any(AbortSignal))
  })

  it('checks one configured credential and renders only redacted availability feedback', async () => {
    const probe = vi.fn(async (id: string) => ({ id, ok: true, message: 'Emby 可访问，API Key 有效', latencyMs: 12 }))
    render(<EmbyWorkspace loadSnapshot={vi.fn(async () => snapshot)} checkCredential={probe} sendPrompt={vi.fn(async () => {})} />)
    await screen.findByText('媒体信号脊柱')
    fireEvent.click(screen.getByRole('button', { name: /配置中心/ }))
    fireEvent.click(screen.getAllByRole('button', { name: '检查可用性' })[0]!)
    await waitFor(() => { expect(probe).toHaveBeenCalledWith('emby-api-key', expect.any(AbortSignal)) })
    expect(await screen.findByText(/Emby 可访问，API Key 有效 · 12 ms/)).toBeTruthy()
    expect(document.body.textContent).not.toContain('formal-secret')
  })
})
