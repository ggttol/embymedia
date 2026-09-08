# Agent Note: EmbyMedia recovers stale CloudDrive FUSE mounts

Status: implemented

English | [中文](2026-09-08-embymedia-fuse-self-recovery.zh.md)

## Problem

CloudDrive2 can remain running after its userspace FUSE session stops answering. The kernel retains the mount entry, so `findmnt` succeeds while `stat` returns `State not recoverable`. Docker records the container as unhealthy but does not restart unhealthy containers. A firewall reload also used `flush ruleset`, which removed Docker's NAT chains and could prevent the repaired container from starting.

## Decision

The Compose stack owns deterministic FUSE cleanup. Stack startup removes an existing unreadable mount before starting containers, and stack shutdown lazily unmounts any remaining CloudDrive mount after all containers stop. Manual maintenance and ordinary service restarts therefore use the same cleanup path.

`embymedia-clouddrive-recovery.timer` checks the CloudDrive container and mount canary every minute. An explicitly unhealthy container recovers immediately; other failures require three consecutive observations. Recovery shares the deployment and backup lock, stops V2 before effectful work, stops Emby before CloudDrive, removes the stale mount, starts CloudDrive and verifies the host canary, starts Emby and verifies its `/media` view, and restores V2 on both success and failure. A recovery attempt starts at most once per fifteen minutes so an upstream outage cannot create a restart loop.

The nftables file replaces only `inet embymedia_filter`. It never flushes the complete ruleset, so Docker retains the chains that implement published container ports.

## Alternatives considered

**Rely on Docker health checks and `restart: unless-stopped`.** Docker health status is observational; restart policies respond to process exit, not `unhealthy`. The failed process can therefore retain a stale FUSE mount indefinitely.

**Restart the Docker daemon during recovery.** Restarting Docker recreates missing NAT chains, but it interrupts every container on the host. Mount recovery stops only CloudDrive, Emby, and V2; preserving Docker's nftables tables removes the reason to restart the daemon.

**Enable transparent host-wide proxying or filesystem intervention.** Network routing does not restore a dead FUSE userspace session, and host-wide interception would expand the failure domain beyond the media stack.

## Consequences

A stale mount becomes a bounded service interruption instead of a persistent operator incident. Recovery intentionally interrupts active V2 work and restarts Emby because continuing against an unreadable or replaced bind mount is unsafe; interrupted effectful tasks remain failed for explicit review. Three observations suppress transient filesystem delays, while the cooldown trades up to fifteen minutes of additional recovery latency for protection against repeated restarts during a provider outage.
