# EmbyMedia safety

English | [中文](SAFETY.zh.md)

## Summary

EmbyMedia can mutate external media services. Authentication, target validation, durable outcomes, backups, and operator review address different risks; none substitutes for the others.

## Table of Contents

- [Credentials and access](#credentials-and-access)
- [Destructive work](#destructive-work)
- [State and recovery](#state-and-recovery)

## Credentials and access

Keep 115 cookies, Emby keys, CloudDrive tokens, webhook secrets, Agent token plaintext, browser-user databases, and recovery passwords out of commits and logs. Use a separately named Agent token for each installation; shared tokens cannot distinguish callers. Treat browser and provider sessions as secrets.

Keep backend listeners on loopback behind the login proxy. Plain HTTP does not encrypt credentials; use a protected network or separately configured TLS when the network is not trusted. Public Agent requests cannot supply their own browser identity, and invalid explicit credentials must fail closed.

## Destructive work

Browser deletion requires the dangerous-action setting and confirmation. MCP deletion binds exact targets to a request, requires one authenticated browser approval within fifteen minutes, and consumes approval once. Agent tokens cannot grant that approval, enable the setting, or call the browser REST delete route. Native Emby deletion requires an authorized Emby administrator device session, not an application API key.

The explicitly enabled completed-pack replacement task may delete an old root only after verifying a complete replacement and while dangerous actions are enabled. Do not interpret ordinary read/write scope as permission to bypass these checks. Original media identity and generated STRM identity must be revalidated separately.

A task accepted into the queue is not a completed operation. Read terminal results and errors. A restart or partial outcome may leave accepted external effects; inspect provider state before retry rather than replaying blindly. Tool discovery proves only registration.

## State and recovery

Never delete databases, WAL/SHM files, credentials, ignored local state, or original media as part of repository cleanup. Preserve the production SQLite format and the exclusive database-owner lock. STRM output needs its shared Emby ACLs; do not weaken original-media permissions to repair output access.

Use the online backup and isolated restore procedures in [operations](docs/operations.md). A raw live database copy is not a consistent backup, and a restored directory is not authorization to replace production. Keep recovery secrets and independent media backups outside the source repository.
