# Agent Note: EmbyMedia deploys Caddy without environment logging or restarts

Status: implemented

English | [中文](2026-09-08-embymedia-caddy-reload-hardening.zh.md)

## Problem

The Debian Caddy package starts the server with `caddy run --environ`. A systemd drop-in also loaded an obsolete DSH upstream cookie, causing the process to copy that credential into journald at every start. Full Caddy restarts then waited for the process stop timeout and briefly interrupted all host routes even though an EmbyMedia release changes only configuration.

## Decision

EmbyMedia installs `/etc/systemd/system/caddy.service.d/embymedia-login.conf` as part of the rollback-tracked deployment configuration. The drop-in clears inherited `EnvironmentFile` directives and replaces `ExecStart` with `caddy run --config /etc/caddy/Caddyfile`, so Caddy does not enumerate its environment into journald and does not receive the obsolete DSH cookie.

The installer reloads Caddy after the new Caddyfile and systemd drop-in pass validation. Rollback restores both files, reloads systemd, and reloads an active Caddy process. Caddy restarts remain reserved for process or package maintenance.

## Alternatives considered

**Retain `--environ` and remove only the obsolete cookie file.** This removes the observed credential but keeps environment enumeration as a recurring disclosure risk for every variable later added to the service.

**Restart Caddy after each release.** Restarting applies configuration, but it interrupts unrelated Emby, CloudDrive, and resource routes and can block until the stop timeout. Caddy's reload path applies validated configuration without replacing the process.

**Manage the distribution package unit directly.** Package upgrades can replace that file. A project-owned drop-in preserves package ownership while making the security-sensitive command explicit.

## Consequences

Routine EmbyMedia releases do not expose service environment values or interrupt shared Caddy routes. Deployment rollback must track the drop-in together with the Caddyfile; a malformed replacement fails before reload, while a reload failure restores both files and leaves the previous release active. Operators must restart Caddy separately when upgrading the binary or changing process-level settings.
