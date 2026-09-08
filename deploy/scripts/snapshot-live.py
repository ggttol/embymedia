#!/usr/bin/env python3
"""Create a stable, application-consistent file snapshot without stopping writers."""

import argparse
import json
import os
from pathlib import Path
import shutil
import sqlite3
import stat
from datetime import datetime, timezone
from urllib.parse import quote

SQLITE_HEADER = b"SQLite format 3\x00"
SQLITE_COMPANIONS = ("-wal", "-shm", "-journal")
COPY_ATTEMPTS = 3


def sqlite_database(path: Path) -> bool:
    try:
        with path.open("rb") as source:
            return source.read(len(SQLITE_HEADER)) == SQLITE_HEADER
    except FileNotFoundError:
        return False


def sqlite_companion(path: Path) -> bool:
    for suffix in SQLITE_COMPANIONS:
        if path.name.endswith(suffix) and sqlite_database(path.with_name(path.name[: -len(suffix)])):
            return True
    return False


def apply_metadata(source: Path, target: Path, source_stat: os.stat_result) -> None:
    os.chmod(target, stat.S_IMODE(source_stat.st_mode), follow_symlinks=False)
    os.chown(target, source_stat.st_uid, source_stat.st_gid, follow_symlinks=False)
    shutil.copystat(source, target, follow_symlinks=False)


def copy_regular(source: Path, target: Path) -> None:
    target.parent.mkdir(parents=True, exist_ok=True)
    for attempt in range(COPY_ATTEMPTS):
        before = source.stat(follow_symlinks=False)
        temporary = target.with_name(f".{target.name}.copy-{os.getpid()}-{attempt}")
        try:
            with source.open("rb") as reader, temporary.open("xb") as writer:
                shutil.copyfileobj(reader, writer, length=1024 * 1024)
                writer.flush()
                os.fsync(writer.fileno())
            after = source.stat(follow_symlinks=False)
            stable = (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns) == (
                after.st_dev,
                after.st_ino,
                after.st_size,
                after.st_mtime_ns,
            )
            if stable:
                apply_metadata(source, temporary, after)
                os.replace(temporary, target)
                return
        finally:
            temporary.unlink(missing_ok=True)
    raise RuntimeError(f"source changed during all copy attempts: {source}")


def backup_sqlite(source: Path, target: Path) -> None:
    target.parent.mkdir(parents=True, exist_ok=True)
    source_stat = source.stat(follow_symlinks=False)
    temporary = target.with_name(f".{target.name}.sqlite-{os.getpid()}")
    temporary.unlink(missing_ok=True)
    source_uri = f"file:{quote(str(source), safe='/')}?mode=ro"
    try:
        with sqlite3.connect(source_uri, uri=True, timeout=30) as reader:
            reader.execute("PRAGMA query_only = ON")
            with sqlite3.connect(temporary, timeout=30) as writer:
                reader.backup(writer, pages=1024, sleep=0.01)
                result = writer.execute("PRAGMA quick_check").fetchone()
                if result != ("ok",):
                    raise RuntimeError(f"SQLite quick_check failed for {source}: {result}")
        apply_metadata(source, temporary, source_stat)
        os.replace(temporary, target)
    finally:
        temporary.unlink(missing_ok=True)


def copy_entry(
    source: Path,
    output: Path,
    strip_prefix: Path,
    exclusions: tuple[Path, ...],
    sqlite_files: list[str],
) -> None:
    if any(source == exclusion or source.is_relative_to(exclusion) for exclusion in exclusions):
        return
    relative = source.absolute().relative_to(strip_prefix)
    target = output / relative
    source_stat = source.lstat()
    if stat.S_ISLNK(source_stat.st_mode):
        target.parent.mkdir(parents=True, exist_ok=True)
        target.symlink_to(os.readlink(source))
        os.lchown(target, source_stat.st_uid, source_stat.st_gid)
        shutil.copystat(source, target, follow_symlinks=False)
        return
    if stat.S_ISDIR(source_stat.st_mode):
        target.mkdir(parents=True, exist_ok=True)
        for child in sorted(source.iterdir(), key=lambda item: item.name):
            if sqlite_companion(child):
                continue
            copy_entry(child, output, strip_prefix, exclusions, sqlite_files)
        apply_metadata(source, target, source_stat)
        return
    if not stat.S_ISREG(source_stat.st_mode):
        raise RuntimeError(f"unsupported file type in backup source: {source}")
    if sqlite_database(source):
        backup_sqlite(source, target)
        sqlite_files.append(str(source))
    else:
        copy_regular(source, target)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--strip-prefix", default=Path("/"), type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--exclude", action="append", default=[], type=Path)
    parser.add_argument("sources", nargs="+", type=Path)
    args = parser.parse_args()
    strip_prefix = args.strip_prefix.absolute()
    output = args.output.absolute()
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    exclusions = tuple(path.absolute() for path in args.exclude)
    sqlite_files: list[str] = []
    for source in args.sources:
        source = source.absolute()
        if not source.is_relative_to(strip_prefix):
            raise SystemExit(f"backup source is outside strip prefix: {source}")
        if not source.exists():
            raise SystemExit(f"backup source does not exist: {source}")
        if output == source or output.is_relative_to(source):
            raise SystemExit(f"backup output must be outside source: {source}")
        copy_entry(source, output, strip_prefix, exclusions, sqlite_files)
    marker = {
        "format_version": 1,
        "created_at": datetime.now(timezone.utc).isoformat(),
        "excluded_paths": sorted(str(path) for path in exclusions),
        "sqlite_databases": sorted(sqlite_files),
    }
    marker_path = output / ".embymedia-online-backup.json"
    marker_path.write_text(json.dumps(marker, ensure_ascii=False, sort_keys=True) + "\n", encoding="utf-8")
    marker_path.chmod(0o600)


if __name__ == "__main__":
    main()
