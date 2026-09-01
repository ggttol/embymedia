import type { Agent } from 'node:http'
import fetch, { type RequestInit, type Response } from 'node-fetch'
import { HttpProxyAgent } from 'http-proxy-agent'
import { HttpsProxyAgent } from 'https-proxy-agent'
import { SocksProxyAgent } from 'socks-proxy-agent'
import { EmbymediaError } from '../errors.ts'

export interface ProxyHttpTransportOptions {
  readonly proxyUrl?: string
  readonly timeoutMs?: number
}

export class ProxyHttpTransport {
  private readonly timeoutMs: number
  private readonly proxyUrl: URL | undefined
  private readonly httpAgent: Agent | undefined
  private readonly httpsAgent: Agent | undefined

  constructor(options: ProxyHttpTransportOptions = {}) {
    this.timeoutMs = options.timeoutMs ?? 45_000
    if (this.timeoutMs <= 0) throw new EmbymediaError('INVALID_INPUT', 'HTTP timeout must be positive')
    if (options.proxyUrl === undefined || options.proxyUrl.trim().length === 0) {
      this.proxyUrl = undefined
      this.httpAgent = undefined
      this.httpsAgent = undefined
      return
    }
    const parsedProxyUrl = new URL(options.proxyUrl)
    this.proxyUrl = parsedProxyUrl.protocol === 'socks5:'
      ? new URL(parsedProxyUrl.href.replace(/^socks5:/, 'socks5h:'))
      : parsedProxyUrl
    if (['socks:', 'socks4:', 'socks4a:', 'socks5:', 'socks5h:'].includes(this.proxyUrl.protocol)) {
      const agent = new SocksProxyAgent(this.proxyUrl)
      this.httpAgent = agent
      this.httpsAgent = agent
    } else if (this.proxyUrl.protocol === 'http:' || this.proxyUrl.protocol === 'https:') {
      this.httpAgent = new HttpProxyAgent(this.proxyUrl)
      this.httpsAgent = new HttpsProxyAgent(this.proxyUrl)
    } else {
      throw new EmbymediaError('INVALID_INPUT', 'proxy protocol must be HTTP, HTTPS, or SOCKS')
    }
  }

  get proxied(): boolean {
    return this.proxyUrl !== undefined
  }

  async request(url: URL, signal: AbortSignal, init: RequestInit = {}): Promise<Response> {
    signal.throwIfAborted()
    if (url.protocol !== 'http:' && url.protocol !== 'https:') throw new EmbymediaError('INVALID_INPUT', 'upstream URL must use HTTP or HTTPS')
    const timeout = AbortSignal.timeout(this.timeoutMs)
    try {
      return await fetch(url, {
        ...init,
        signal: AbortSignal.any([signal, timeout]),
        redirect: init.redirect ?? 'error',
        ...(this.proxyUrl === undefined ? {} : { agent: url.protocol === 'https:' ? this.httpsAgent : this.httpAgent }),
      })
    } catch (error) {
      if (signal.aborted) throw new EmbymediaError('CANCELLED', 'HTTP request cancelled')
      if (error instanceof EmbymediaError) throw error
      throw new EmbymediaError('UPSTREAM_UNAVAILABLE', 'HTTP request timed out or failed')
    }
  }
}
