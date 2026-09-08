# Agent Note: EmbyMedia backups use online consistent snapshots

Status: implemented

English | [中文](2026-09-08-embymedia-online-backups.zh.md)

## Problem

A file-level Restic read cannot make live SQLite main, WAL, and SHM files transaction-consistent. Stopping the complete Compose stack made those files quiet, but interrupted playback, disconnected CloudDrive's FUSE process, and turned a daily backup into a recurring availability and mount-recovery event.

## Decision

The backup process holds the existing deployment lock and builds a private tree under `/srv/embymedia/backups`. It leaves V2 and every container running. `snapshot-live.py` preserves each selected absolute path beneath that tree, copies regular files only when their device, inode, size, and modification time remain stable across the read, and retries a changing file three times. Emby cache, logs and transcoding scratch plus CloudDrive logs, temporary files and updates are derived or volatile and stay outside the recovery snapshot.

A file with the SQLite header is copied through SQLite's online backup API and validated with `PRAGMA quick_check`. The staged tree omits the corresponding WAL, SHM, and rollback-journal companions because the copied database already contains one committed snapshot. File modes, ownership, timestamps, directories, and symlinks are preserved. Restic ignores staged inode and ctime values because every staging run creates new files; preserved size and modification time let unchanged files reuse the parent snapshot. A writable Restic cache keeps index work out of the service's protected home directory. A format marker identifies the staged layout before Restic records it, and cleanup removes both complete and partial staging trees on every exit.

Isolated restore accepts both historical direct-path snapshots and the online staged format. It materializes staged canonical paths before validating browser identity and running the standalone database check. Production recovery therefore keeps the same final filesystem layout without making Restic read databases while they change.

## Alternatives considered

**Continue stopping the Compose stack.** This gives a simple quiet filesystem but unnecessarily interrupts independent services and deliberately tears down the FUSE owner every day.

**Copy SQLite database, WAL, and SHM files together while writers run.** File copies occur at different instants; a matching filename set does not establish one committed database state.

**Back up live paths directly and accept Restic exit code 3.** Incomplete snapshots are not a recovery mechanism. Retrying stable regular files and using SQLite's backup API makes inconsistency fail before a snapshot is published.

**Store one tar stream through Restic stdin.** A stream can preserve canonical archive paths, but it gives up Restic's file-level browsing and makes isolated validation depend on an additional archive extraction format.

## Consequences

Daily backup no longer creates application or media downtime. The temporary snapshot consumes up to approximately the selected data size until Restic finishes, and regular files do not share one global transaction point; mutable databases do. A file that changes through all retries fails the run without replacing a valid Restic snapshot. Restore code carries one explicit staged-layout normalization path while retaining historical snapshot support.
