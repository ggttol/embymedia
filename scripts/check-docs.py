#!/usr/bin/env python3
"""Check active documentation pairs/links and immutable Agent Note archives."""

import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
ARCHIVE = ROOT / '.agents/notes/archived'
KINDS = {'architecture', 'bug-fix', 'feature', 'process', 'simplification', 'testing'}


def blob_hash(content):
    """Return the Git SHA-1 blob identifier used by bilingual sidecars."""
    return hashlib.sha1(f'blob {len(content)}\0'.encode() + content).hexdigest()


def check_pair(sidecar):
    """Require both reviewed language files and their exact recorded bytes."""
    base = sidecar.name.removesuffix('.i18n.yaml')
    expected = {f'{base}.md', f'{base}.zh.md'}
    records = {}
    for line in sidecar.read_text().splitlines():
        if not line or line.startswith('#'):
            continue
        match = re.fullmatch(r'([^:#]+\.md): ([0-9a-f]{40})', line)
        if not match or match[1] in records:
            raise ValueError(f'{sidecar.relative_to(ROOT)}: invalid pairing record')
        records[match[1]] = match[2]
    if set(records) != expected:
        raise ValueError(f'{sidecar.relative_to(ROOT)}: expected one English/Chinese pair')
    contents = []
    for name, digest in records.items():
        content = (sidecar.parent / name).read_bytes()
        if blob_hash(content) != digest:
            raise ValueError(f'{sidecar.relative_to(ROOT)}: stale hash for {name}')
        contents.append(content)
    if ARCHIVE not in sidecar.parents and len(contents[0].splitlines()) != len(contents[1].splitlines()):
        raise ValueError(f'{sidecar.relative_to(ROOT)}: bilingual line counts differ')


def prose_lines(text):
    """Omit fenced examples when checking Markdown links and heading anchors."""
    fence = None
    for line in text.splitlines():
        match = re.match(r'^\s*(`{3,}|~{3,})', line)
        if match:
            token = match[1]
            if fence is None:
                fence = token
            elif token[0] == fence[0] and len(token) >= len(fence):
                fence = None
            continue
        if fence is None:
            yield line


def anchors(path):
    """Collect ordinary Markdown heading anchors, including duplicate suffixes."""
    result = set()
    for line in prose_lines(path.read_text()):
        match = re.match(r'^#{1,6}\s+(.+?)\s*#*$', line)
        if not match:
            continue
        title = re.sub(r'\[([^]]+)\]\([^)]*\)', r'\1', match[1])
        slug = re.sub(r'[^\w\-\s]', '', title.lower()).replace(' ', '-')
        candidate = slug
        suffix = 0
        while candidate in result:
            suffix += 1
            candidate = f'{slug}-{suffix}'
        result.add(candidate)
    return result


def check_links(path):
    """Resolve local inline and reference links without checking remote services."""
    for line in prose_lines(path.read_text()):
        links = re.findall(r'\[[^]]*\]\(([^\s)]+)(?:\s+"[^"]*")?\)', line)
        reference = re.match(r'^\s*\[[^]]+\]:\s*(\S+)', line)
        if reference:
            links.append(reference[1])
        for link in links:
            url = urlsplit(link.strip('<>'))
            if url.scheme or url.netloc:
                continue
            target = (path.parent / unquote(url.path)).resolve() if url.path else path
            if not target.is_relative_to(ROOT) or not target.exists():
                raise ValueError(f'{path.relative_to(ROOT)}: missing local link {link}')
            if url.fragment and target.suffix == '.md' and ARCHIVE not in target.parents:
                if unquote(url.fragment) not in anchors(target):
                    raise ValueError(f'{path.relative_to(ROOT)}: missing heading {link}')


def check_archive():
    """Enforce sealed bytes, complete triplets, and append-only baseline entries."""
    manifest_path = ARCHIVE / 'manifest.json'
    manifest = json.loads(manifest_path.read_text())
    if set(manifest) != {'version', 'files'} or manifest['version'] != 1 or not isinstance(manifest['files'], dict):
        raise ValueError('archive manifest: expected version 1 and files map')
    files = manifest['files']
    observed = set()
    pairs = set()
    for directory in ARCHIVE.iterdir():
        if directory.name in {'AGENTS.md', 'manifest.json'} and directory.is_file() and not directory.is_symlink():
            continue
        if directory.name not in KINDS or not directory.is_dir() or directory.is_symlink():
            raise ValueError(f'archive: invalid root entry {directory.name}')
        for path in directory.iterdir():
            name = path.relative_to(ARCHIVE).as_posix()
            match = re.fullmatch(r'(\d{4}-\d{2}-\d{2}-.+?)(\.zh\.md|\.i18n\.yaml|\.md)', path.name)
            if not match or not path.is_file() or path.is_symlink():
                raise ValueError(f'archive: invalid artifact {name}')
            observed.add(name)
            digest = 'sha256:' + hashlib.sha256(path.read_bytes()).hexdigest()
            if files.get(name) != digest:
                raise ValueError(f'archive: unsealed or modified artifact {name}')
            base, extension = match.groups()
            pairs.add(directory / f'{base}.i18n.yaml')
            if extension != '.i18n.yaml':
                lines = path.read_text().splitlines()
                switcher = f'[English]({base}.md) | 中文' if extension == '.zh.md' else f'English | [中文]({base}.zh.md)'
                if len(lines) < 6 or not lines[0].startswith('# Agent Note: ') or lines[1:3] != ['', 'Status: implemented'] or lines[4:6] != ['', switcher]:
                    raise ValueError(f'archive: invalid implemented header {name}')
                archived = datetime.date.fromisoformat(lines[3].removeprefix('Archived: '))
                if lines[3] != f'Archived: {archived.isoformat()}' or archived < datetime.date.fromisoformat(base[:10]):
                    raise ValueError(f'archive: invalid archive date {name}')
                counterpart = directory / (f'{base}.md' if extension == '.zh.md' else f'{base}.zh.md')
                if counterpart.read_text().splitlines()[3] != lines[3]:
                    raise ValueError(f'archive: language archive dates differ for {name}')
    if set(files) != observed:
        raise ValueError('archive: manifest entries and files differ')
    if {path.name for path in ARCHIVE.iterdir() if path.is_dir()} != KINDS:
        raise ValueError('archive: required kind directory is missing')
    for sidecar in sorted(pairs):
        check_pair(sidecar)
    baseline = os.environ.get('EMBYMEDIA_ARCHIVE_BASE_REF')
    if baseline or (ROOT / '.git').exists():
        baseline = baseline or 'HEAD'
        previous = subprocess.run(['git', 'show', f'{baseline}:.agents/notes/archived/manifest.json'], cwd=ROOT, capture_output=True, text=True, check=True)
        for name, digest in json.loads(previous.stdout)['files'].items():
            if files.get(name) != digest:
                raise ValueError(f'archive: frozen baseline entry changed or removed: {name}')
    return len(observed)


def main():
    """Check current documentation without following removed-workspace residue."""
    documents = set(ROOT.glob('*.md'))
    for directory in ('docs', '.agents/notes', 'deploy'):
        for parent, folders, files in os.walk(ROOT / directory):
            folders[:] = [name for name in folders if name not in {'node_modules', '__pycache__', 'archived'} and not name.startswith('.')]
            documents.update(Path(parent) / name for name in files if name.endswith('.md'))
    documents = sorted(path for path in documents if not path.is_symlink())
    pairs = set()
    for path in documents:
        content = path.read_bytes()
        if not content.endswith(b'\n') or content.endswith(b'\n\n'):
            raise ValueError(f'{path.relative_to(ROOT)}: expected exactly one trailing newline')
        check_links(path)
        if path.name.endswith('.zh.md'):
            pairs.add(path.with_name(path.name.removesuffix('.zh.md') + '.i18n.yaml'))
        elif path.with_suffix('.zh.md').exists():
            pairs.add(path.with_suffix('.i18n.yaml'))
        elif path.name not in {'AGENTS.md', 'CLAUDE.md', 'PRODUCT.md', 'DESIGN.md', 'EVIDENCE.md', 'THIRD_PARTY_NOTICES.md', 'SKILL.md'} and path.parent != ROOT / '.agents/notes':
            raise ValueError(f'{path.relative_to(ROOT)}: missing Chinese counterpart')
    for sidecar in sorted(pairs):
        check_pair(sidecar)
    archived = check_archive()
    print(f'Documentation: {len(documents)} active files, {len(pairs)} bilingual pairs, {archived} frozen artifacts checked.')


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, IndexError, subprocess.CalledProcessError) as error:
        print(f'check-docs: {error}', file=sys.stderr)
        sys.exit(1)
