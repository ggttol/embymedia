#!/usr/bin/env python3
"""Plan and execute ID-bound 115 media normalization without permanent deletion."""

import concurrent.futures
import argparse
import collections
import http.client
import json
import os
import pathlib
import re
import sqlite3
import time
import urllib.parse
import urllib.error
import urllib.request

VIDEO_EXTENSIONS = {".avi", ".flv", ".iso", ".m2ts", ".m4v", ".mkv", ".mov", ".mp4", ".mpeg", ".mpg", ".rm", ".rmvb", ".ts", ".vob", ".webm", ".wmv"}
SUBTITLE_EXTENSIONS = {".ass", ".idx", ".smi", ".srt", ".ssa", ".sub", ".sup", ".vtt"}
ANIMATION_GENRES = {"Animation", "动画"}
CHILD_GENRES = ANIMATION_GENRES | {"Family", "家庭"}
VARIETY_GENRES = {"Reality", "真人秀", "Talk", "脱口秀"}
SOURCE_DATABASE = "/srv/embymedia/data/embymedia.db"
TITLE_RELEASE_SUFFIX = re.compile(r"(?i)\s+(?:4K|蓝光)\s*原盘\s*REMUX.*$")
PAGE_SIZE = 1000


def json_value(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True)


def scalar(value):
    if value is None:
        return ""
    if isinstance(value, (int, str)):
        return str(value)
    return ""


def clean_name(value):
    value = re.sub(r"[\x00-\x1f]", "", str(value)).replace("/", "／").strip(" .")
    return value or "未命名"


def trim_utf8(value, maximum=230):
    raw = value.encode("utf-8")
    if len(raw) <= maximum:
        return value
    raw = raw[:maximum]
    while True:
        try:
            return raw.decode("utf-8").rstrip(" .")
        except UnicodeDecodeError as error:
            raw = raw[:error.start]

def clean_title(value):
    return clean_name(TITLE_RELEASE_SUFFIX.sub("", str(value)))


def provider_path(nodes, root_id, node_id):
    parts = []
    seen = set()
    while node_id != root_id:
        if node_id in seen or node_id not in nodes:
            raise RuntimeError(f"broken 115 parent chain at {node_id}")
        seen.add(node_id)
        node = nodes[node_id]
        parts.append(node["name"].replace("/", "／"))
        node_id = node["parent_id"]
    return "/".join(reversed(parts))


def providers(item):
    return json.loads(item["provider_ids_json"])


def raw_item(item):
    return json.loads(item["raw_json"])


def genres(item):
    return set(raw_item(item).get("Genres") or [])


def year_of(item):
    year = item.get("production_year")
    if year:
        return int(year)
    tmdb = providers(item).get("Tmdb")
    if item["type"] == "Series" and tmdb == "1973":
        return 2001
    return None


def technical_version(item):
    raw = raw_item(item)
    sources = json.loads(item.get("media_sources_json") or "[]")
    streams = json.loads(item.get("media_streams_json") or "[]")
    source = sources[0] if sources else {}
    video = next((stream for stream in streams if stream.get("Type") == "Video"), {})
    audio = next((stream for stream in streams if stream.get("Type") == "Audio"), {})
    height = int(video.get("Height") or 0)
    label = []
    if height:
        label.append("2160p" if height >= 2000 else "1080p" if height >= 1000 else "720p" if height >= 700 else f"{height}p")
    codec = str(video.get("Codec") or "").upper()
    if codec:
        label.append({"H265": "HEVC", "H264": "AVC"}.get(codec, codec))
    range_text = " ".join(str(video.get(key) or source.get(key) or "") for key in ("VideoRange", "VideoRangeType", "VideoDoViTitle"))
    if "DOVI" in range_text.upper() or "DOLBY VISION" in range_text.upper():
        label.append("DV")
    if "HDR" in range_text.upper():
        label.append("HDR")
    audio_codec = str(audio.get("Codec") or "").upper()
    if audio_codec:
        label.append(audio_codec.replace("TRUEHD", "TrueHD").replace("DTSHD", "DTS-HD"))
    if raw.get("IndexNumberEnd"):
        label.append("Multi-Episode")
    return " ".join(dict.fromkeys(label)) or "Original"


def identity(item, series_by_id, override=None):
    owner = series_by_id.get(item["series_id"]) if item["type"] == "Episode" else item
    if not owner:
        return None, None, item
    effective_item = item
    if override:
        owner = dict(owner)
        owner["name"] = override["title"]
        owner["production_year"] = int(override["year"])
        owner["provider_ids_json"] = json_value({"Tmdb": str(override["tmdb_id"])})
        owner["type"] = "Series" if override["kind"] == "series" else "Movie"
        if override["kind"] == "movie":
            effective_item = dict(item)
            effective_item["name"] = override["title"]
            effective_item["production_year"] = int(override["year"])
            effective_item["provider_ids_json"] = owner["provider_ids_json"]
            effective_item["type"] = "Movie"
    tmdb = providers(owner).get("Tmdb")
    year = year_of(owner)
    if not tmdb or not year:
        return None, owner, effective_item
    kind = "series" if owner["type"] == "Series" else "movie"
    return f"{kind}:{tmdb}", owner, effective_item

def reviewed_file_candidate(node, specification):
    owner = {
        "id": "reviewed:" + node["file_id"],
        "type": "Series",
        "name": specification["title"],
        "production_year": int(specification["year"]),
        "provider_ids_json": json_value({"Tmdb": str(specification["tmdb_id"])}),
        "raw_json": json_value({"Genres": []}),
    }
    item = {
        "id": owner["id"] + ":episode",
        "type": "Episode",
        "name": specification["title"],
        "series_id": owner["id"],
        "production_year": int(specification["year"]),
        "parent_index_number": int(specification["season"]),
        "index_number": int(specification["episode"]),
        "provider_ids_json": "{}",
        "media_sources_json": "[]",
        "media_streams_json": "[]",
        "raw_json": json_value({"Genres": [], "IndexNumberEnd": specification.get("episode_end")}),
    }
    return {
        "item": item,
        "owner": owner,
        "identity": "series:" + str(specification["tmdb_id"]),
        "destination": specification["destination"],
        "node": node,
    }

def logical_media_key(candidate):
    item = candidate["item"]
    if item["type"] == "Movie":
        return candidate["identity"]
    raw = raw_item(item)
    return (
        candidate["identity"],
        int(item.get("parent_index_number") or 0),
        int(item.get("index_number") or 0),
        int(raw.get("IndexNumberEnd") or 0),
    )


def classify(item, owner, node, library_names, series_destinations):
    if not owner or not providers(owner).get("Tmdb") or not year_of(owner):
        return "_待整理"
    source = node["top_library"]
    if item["type"] == "Movie":
        item_genres = genres(item)
        if node["size"] >= 45_000_000_000 or int(raw_item(item).get("Bitrate") or 0) >= 45_000_000:
            return "IMAX巨幕"
        if source == "儿童" and item_genres & CHILD_GENRES:
            return "儿童"
        if source == "剧场版" or item_genres & ANIMATION_GENRES:
            return "动漫电影"
        if source == "最新电影":
            return "最新电影"
        return "电影"
    return series_destinations[owner["id"]]


def choose_series_destinations(series_by_id, library_names):
    grouped = collections.defaultdict(list)
    for series in series_by_id.values():
        tmdb = providers(series).get("Tmdb")
        if tmdb:
            grouped[tmdb].append(series)
    result = {}
    for candidates in grouped.values():
        sources = {library_names[item["library_id"]] for item in candidates}
        all_genres = set().union(*(genres(item) for item in candidates))
        if "电视剧追更" in sources:
            destination = "电视剧追更"
        elif "综艺追更" in sources:
            destination = "综艺追更"
        elif "纪录片" in sources:
            destination = "纪录片"
        elif all_genres & ANIMATION_GENRES:
            destination = "动漫"
        elif "综艺" in sources or all_genres & VARIETY_GENRES:
            destination = "综艺"
        else:
            destination = "电视剧"
        for item in candidates:
            result[item["id"]] = destination
    for series in series_by_id.values():
        result.setdefault(series["id"], "_待整理")
    return result


def select_title(candidates):
    weighted = collections.Counter()
    details = {}
    for item, weight in candidates:
        title = clean_title(item["name"])
        year = year_of(item)
        tmdb = providers(item).get("Tmdb")
        key = (title, year, tmdb)
        weighted[key] += weight
        details[key] = item
    if not weighted:
        raise RuntimeError("identity has no title candidate")
    def rank(entry):
        (title, year, tmdb), weight = entry
        cjk = bool(re.search(r"[\u3400-\u9fff]", title))
        return weight, cjk, -len(title), title
    key, _ = max(weighted.items(), key=rank)
    title, year, tmdb = key
    return title, year, tmdb, details[key]


def canonical_title_directory(title, year, tmdb):
    return trim_utf8(f"{clean_name(title)} ({year}) [tmdbid={tmdb}]")

def canonical_media_name(item, owner, version, extension, needs_version):
    title = clean_title(owner["name"])
    if item["type"] == "Movie":
        suffix = f" ({year_of(owner)})"
    else:
        raw = raw_item(item)
        season = int(item.get("parent_index_number") or 0)
        episode = int(item.get("index_number") or 0)
        end = int(raw.get("IndexNumberEnd") or 0)
        token = f"S{season:02d}E{episode:02d}"
        if end > episode:
            token += f"-E{end:02d}"
        suffix = " " + token
    if needs_version:
        suffix += " - " + version
    extension = extension.lower()
    maximum = 250 - len(extension.encode("utf-8")) - len(suffix.encode("utf-8"))
    if maximum < 1:
        raise RuntimeError("media filename suffix exceeds the 115 name limit")
    return trim_utf8(title, maximum) + suffix + extension


def initialize_plan(path):
    database = sqlite3.connect(path)
    database.executescript("""
        PRAGMA journal_mode=WAL;
        CREATE TABLE metadata(key TEXT PRIMARY KEY, value TEXT NOT NULL);
        CREATE TABLE directories(
            key TEXT PRIMARY KEY,
            parent_key TEXT NOT NULL,
            name TEXT NOT NULL,
            cid TEXT NOT NULL DEFAULT '',
            status TEXT NOT NULL DEFAULT 'pending',
            error TEXT NOT NULL DEFAULT ''
        );
        CREATE TABLE operations(
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            phase INTEGER NOT NULL,
            object_id TEXT NOT NULL UNIQUE,
            expected_parent_id TEXT NOT NULL,
            expected_name TEXT NOT NULL,
            expected_size INTEGER NOT NULL,
            expected_sha1 TEXT NOT NULL,
            target_directory_key TEXT NOT NULL,
            target_name TEXT NOT NULL,
            identity TEXT NOT NULL,
            disposition TEXT NOT NULL,
            status TEXT NOT NULL DEFAULT 'pending',
            error TEXT NOT NULL DEFAULT ''
        );
        CREATE TABLE findings(
            object_id TEXT NOT NULL,
            path TEXT NOT NULL,
            reason TEXT NOT NULL,
            details_json TEXT NOT NULL
        );
        CREATE INDEX operations_target ON operations(target_directory_key, status, id);
    """)
    return database


def command_plan(args):
    layout = json.loads(pathlib.Path(args.layout).read_text())
    identity_overrides = layout.get("identity_overrides", {})
    deleted_identities = set(layout.get("deleted_identities", []))
    root_id = layout["provider_root"]["cid"]
    inventory = sqlite3.connect(f"file:{args.inventory}?mode=ro", uri=True)
    inventory.row_factory = sqlite3.Row
    nodes = {row["file_id"]: dict(row) for row in inventory.execute("SELECT * FROM nodes")}
    path_to_node = {provider_path(nodes, root_id, node_id): node for node_id, node in nodes.items() if not node["is_folder"]}
    emby = sqlite3.connect(f"file:{args.emby}?mode=ro", uri=True)
    emby.row_factory = sqlite3.Row
    library_names = {row["id"]: row["name"] for row in emby.execute("SELECT * FROM libraries")}
    all_items = {row["id"]: dict(row) for row in emby.execute("SELECT * FROM items")}
    series_by_id = {item_id: item for item_id, item in all_items.items() if item["type"] == "Series"}
    series_destinations = choose_series_destinations(series_by_id, library_names)
    strm = sqlite3.connect(f"file:{args.strm}?mode=ro", uri=True)
    strm.row_factory = sqlite3.Row
    strm_by_relative = {row["relative_path"]: dict(row) for row in strm.execute("SELECT * FROM strm")}
    file_identity_overrides = layout.get("file_identity_overrides", {})
    candidate_map = collections.defaultdict(list)
    findings = []
    for item in all_items.values():
        if item["type"] not in {"Movie", "Episode"} or not item["path"].startswith("/strm/"):
            continue
        stream = strm_by_relative.get(item["path"][len("/strm/"):])
        if not stream or not stream["target"].startswith("/media/"):
            findings.append((item["id"], item["path"], "emby_item_without_exact_strm", {"type": item["type"]}))
            continue
        node = path_to_node.get(stream["target"][len("/media/"):])
        if not node:
            findings.append((item["id"], stream["target"], "strm_target_absent_from_115_snapshot", {"type": item["type"]}))
            continue
        file_override = file_identity_overrides.get(node["file_id"])
        if file_override:
            candidate_map[node["file_id"]].append(reviewed_file_candidate(node, file_override))
            continue
        original_owner = series_by_id.get(item["series_id"]) if item["type"] == "Episode" else item
        override = identity_overrides.get(original_owner["id"]) if original_owner else None
        item_identity, owner, effective_item = identity(item, series_by_id, override)
        if effective_item["type"] == "Episode" and (effective_item.get("index_number") is None or int(effective_item["index_number"]) <= 0 or effective_item.get("parent_index_number") is None or int(effective_item["parent_index_number"]) < 0):
            findings.append((effective_item["id"], effective_item["path"], "invalid_episode_number", {"season": effective_item.get("parent_index_number"), "episode": effective_item.get("index_number")}))
            continue
        destination = override["destination"] if override else classify(effective_item, owner, node, library_names, series_destinations)
        candidate_map[node["file_id"]].append({"item": effective_item, "owner": owner, "identity": item_identity, "destination": destination, "node": node})
    for object_id, specification in file_identity_overrides.items():
        if object_id not in candidate_map:
            node = nodes.get(object_id)
            if not node or node["is_folder"]:
                raise RuntimeError(f"reviewed file override is absent: {object_id}")
            candidate_map[object_id].append(reviewed_file_candidate(node, specification))
    for node in nodes.values():
        node_path = provider_path(nodes, root_id, node["file_id"])
        if not node["is_folder"] and pathlib.Path(node["name"]).suffix.lower() in VIDEO_EXTENSIONS and node["file_id"] not in candidate_map and "/BDMV/" not in "/" + node_path + "/" and "/CERTIFICATE/" not in "/" + node_path + "/":
            findings.append((node["file_id"], node_path, "provider_video_without_emby_identity", {"size": node["size"], "sha1": node["sha1"]}))
    resolved = {}
    for object_id, candidates in candidate_map.items():
        keys = {(candidate["identity"], candidate["destination"], logical_media_key(candidate)) for candidate in candidates}
        if len(keys) != 1 or next(iter(keys))[0] is None:
            findings.append((object_id, provider_path(nodes, root_id, object_id), "conflicting_or_missing_media_identity", {"candidates": sorted([list(key) for key in keys], key=str)}))
            continue
        identity_key = candidates[0]["identity"]
        if identity_key in deleted_identities:
            findings.append((object_id, provider_path(nodes, root_id, object_id), "operator_deleted_identity", {"identity": identity_key}))
            continue
        resolved[object_id] = candidates[0]
    duplicate_groups = collections.defaultdict(list)
    for object_id, candidate in resolved.items():
        node = candidate["node"]
        if node["sha1"]:
            duplicate_groups[(node["sha1"], node["size"])].append(object_id)
    duplicate_disposition = {}
    for (sha1, size), object_ids in duplicate_groups.items():
        if len(object_ids) < 2:
            continue
        media_keys = {logical_media_key(resolved[object_id]) for object_id in object_ids}
        if len(media_keys) != 1:
            for object_id in object_ids:
                findings.append((object_id, provider_path(nodes, root_id, object_id), "exact_bytes_have_conflicting_identities", {"sha1": sha1, "size": size, "media_keys": sorted(media_keys, key=str)}))
                resolved.pop(object_id, None)
            continue
        keeper = min(object_ids, key=lambda object_id: (resolved[object_id]["node"]["top_library"] != resolved[object_id]["destination"], provider_path(nodes, root_id, object_id), object_id))
        for object_id in object_ids:
            if object_id != keeper:
                duplicate_disposition[object_id] = "exact_duplicate"
    identity_titles = collections.defaultdict(list)
    for candidate in resolved.values():
        identity_titles[candidate["identity"]].append((candidate["owner"], 1))
    selected_titles = {key: select_title(values) for key, values in identity_titles.items()}
    version_counts = collections.Counter()
    prepared = {}
    for object_id, candidate in resolved.items():
        item = candidate["item"]
        media_key = logical_media_key(candidate)
        version = technical_version(item)
        version_counts[(media_key, version)] += 1
        prepared[object_id] = (candidate, media_key, version)
    if pathlib.Path(args.output).exists():
        raise RuntimeError("output plan already exists")
    plan = initialize_plan(args.output)
    with plan:
        plan.executemany("INSERT INTO metadata VALUES (?, ?)", [
            ("account_id", layout["provider_root"]["account_id"]),
            ("root_cid", root_id),
            ("layout_json", json_value(layout)),
            ("inventory_path", str(pathlib.Path(args.inventory).resolve())),
            ("created_at", time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())),
        ])
        for name in layout["library_order"] + ["_待整理", "_待回收"]:
            plan.execute("INSERT INTO directories(key,parent_key,name) VALUES(?,?,?)", (f"library:{name}", "root", name))
        plan.execute("INSERT INTO directories(key,parent_key,name,cid,status) VALUES(?,?,?,?,?)", ("root", "", layout["provider_root"]["name"], root_id, "ready"))
        for identity_key, (title, year, tmdb, owner) in selected_titles.items():
            destinations = {candidate["destination"] for candidate in resolved.values() if candidate["identity"] == identity_key and candidate["node"]["file_id"] not in duplicate_disposition}
            for destination in destinations:
                directory_key = f"title:{destination}:{identity_key}"
                plan.execute("INSERT OR IGNORE INTO directories(key,parent_key,name) VALUES(?,?,?)", (directory_key, f"library:{destination}", canonical_title_directory(title, year, tmdb)))
        for object_id, (candidate, media_key, version) in prepared.items():
            node = candidate["node"]
            if object_id in duplicate_disposition:
                directory_key = "library:_待回收"
                target_name = trim_utf8(f"{node['sha1'][:12]}-{object_id}-{clean_name(node['name'])}", 250)
                disposition = "exact_duplicate"
                phase = 4
            else:
                destination = candidate["destination"]
                directory_key = f"title:{destination}:{candidate['identity']}"
                item = candidate["item"]
                owner = candidate["owner"]
                if item["type"] == "Episode":
                    season = int(item.get("parent_index_number") or 0)
                    season_key = f"season:{destination}:{candidate['identity']}:{season:02d}"
                    plan.execute("INSERT OR IGNORE INTO directories(key,parent_key,name) VALUES(?,?,?)", (season_key, directory_key, f"Season {season:02d}"))
                    directory_key = season_key
                needs_version = version_counts[(media_key, version)] > 1 or sum(1 for value in prepared.values() if value[1] == media_key) > 1
                extension = pathlib.Path(node["name"]).suffix
                target_name = canonical_media_name(item, owner, version, extension, needs_version)
                if needs_version and version_counts[(media_key, version)] > 1:
                    stem = pathlib.Path(target_name).stem
                    disambiguation = f" [{node['sha1'][:8]}]"
                    maximum = 250 - len(extension.encode("utf-8")) - len(disambiguation.encode("utf-8"))
                    target_name = trim_utf8(stem, maximum) + disambiguation + extension.lower()
                disposition = "canonical"
                phase = 3
            plan.execute(
                "INSERT INTO operations(phase,object_id,expected_parent_id,expected_name,expected_size,expected_sha1,target_directory_key,target_name,identity,disposition) VALUES(?,?,?,?,?,?,?,?,?,?)",
                (phase, object_id, node["parent_id"], node["name"], node["size"], node["sha1"], directory_key, target_name, candidate["identity"], disposition),
            )
        for object_id, path, reason, details in findings:
            plan.execute("INSERT INTO findings VALUES(?,?,?,?)", (object_id, path, reason, json_value(details)))
        augment_subtitles(plan, nodes)
        collision = plan.execute("SELECT target_directory_key,target_name,COUNT(*) FROM operations GROUP BY target_directory_key,target_name HAVING COUNT(*)>1 LIMIT 1").fetchone()
        if collision:
            raise RuntimeError(f"duplicate planned target name: {collision[0]}/{collision[1]} ({collision[2]} copies)")
    destination_counts = collections.Counter()
    for (directory_key,) in plan.execute("SELECT target_directory_key FROM operations WHERE disposition='canonical'"):
        destination_counts[directory_key.split(":", 2)[1]] += 1
    summary = {
        "directories": plan.execute("SELECT COUNT(*) FROM directories WHERE key != 'root'").fetchone()[0],
        "operations": plan.execute("SELECT COUNT(*) FROM operations").fetchone()[0],
        "canonical": plan.execute("SELECT COUNT(*) FROM operations WHERE disposition='canonical'").fetchone()[0],
        "exact_duplicates_staged": plan.execute("SELECT COUNT(*) FROM operations WHERE disposition='exact_duplicate'").fetchone()[0],
        "findings": plan.execute("SELECT COUNT(*) FROM findings").fetchone()[0],
        "destinations": dict(destination_counts),
    }
    plan.close()
    strm.close()
    emby.close()
    inventory.close()
    print(json.dumps(summary, ensure_ascii=False, indent=2))



def augment_subtitles(plan, nodes):
    plan.row_factory = sqlite3.Row
    existing_ids = {row[0] for row in plan.execute("SELECT object_id FROM operations")}
    media_by_parent = collections.defaultdict(list)
    target_names = collections.defaultdict(set)
    for operation in plan.execute("SELECT * FROM operations"):
        target_names[operation["target_directory_key"]].add(operation["target_name"])
        if operation["disposition"] == "canonical" and pathlib.Path(operation["expected_name"]).suffix.lower() in VIDEO_EXTENSIONS:
            media_by_parent[operation["expected_parent_id"]].append(operation)
    added = 0
    for node in nodes.values():
        extension = pathlib.Path(node["name"]).suffix.lower()
        if node["is_folder"] or extension not in SUBTITLE_EXTENSIONS or node["file_id"] in existing_ids:
            continue
        subtitle_stem = pathlib.Path(node["name"]).stem
        matches = []
        for operation in media_by_parent.get(node["parent_id"], []):
            media_stem = pathlib.Path(operation["expected_name"]).stem
            if subtitle_stem == media_stem or subtitle_stem.startswith(media_stem + "."):
                matches.append((len(media_stem), operation, subtitle_stem[len(media_stem):]))
        if not matches:
            plan.execute("INSERT INTO findings VALUES(?,?,?,?)", (node["file_id"], node["name"], "unbound_external_subtitle", json_value({"parent_id": node["parent_id"]})))
            continue
        matches.sort(key=lambda match: match[0], reverse=True)
        if len(matches) > 1 and matches[0][0] == matches[1][0]:
            plan.execute("INSERT INTO findings VALUES(?,?,?,?)", (node["file_id"], node["name"], "ambiguous_external_subtitle", json_value({"parent_id": node["parent_id"]})))
            continue
        _, media, tail = matches[0]
        target_stem = pathlib.Path(media["target_name"]).stem
        target_name = trim_utf8(target_stem + tail, 250 - len(extension.encode("utf-8"))) + extension
        occupied = target_names[media["target_directory_key"]]
        if target_name in occupied:
            target_name = trim_utf8(target_stem + tail + " [" + (node["sha1"][:8] or node["file_id"][-8:]) + "]", 250 - len(extension.encode("utf-8"))) + extension
        if target_name in occupied:
            plan.execute("INSERT INTO findings VALUES(?,?,?,?)", (node["file_id"], node["name"], "external_subtitle_name_conflict", json_value({"target": target_name})))
            continue
        plan.execute(
            "INSERT INTO operations(phase,object_id,expected_parent_id,expected_name,expected_size,expected_sha1,target_directory_key,target_name,identity,disposition) VALUES(?,?,?,?,?,?,?,?,?,?)",
            (3, node["file_id"], node["parent_id"], node["name"], node["size"], node["sha1"], media["target_directory_key"], target_name, media["identity"], "canonical_attachment"),
        )
        occupied.add(target_name)
        added += 1
    return added


def command_augment_subtitles(args):
    inventory = sqlite3.connect(f"file:{args.inventory}?mode=ro", uri=True)
    inventory.row_factory = sqlite3.Row
    nodes = {row["file_id"]: dict(row) for row in inventory.execute("SELECT * FROM nodes")}
    plan = sqlite3.connect(args.plan)
    if plan.execute("SELECT COUNT(*) FROM operations WHERE status='completed'").fetchone()[0]:
        raise RuntimeError("external subtitles must be added before media operations start")
    with plan:
        added = augment_subtitles(plan, nodes)
    print(json.dumps({"external_subtitles_added": added}, ensure_ascii=False))
    plan.close()
    inventory.close()


def command_augment_residuals(args):
    inventory = sqlite3.connect(f"file:{args.inventory}?mode=ro", uri=True)
    inventory.row_factory = sqlite3.Row
    nodes = {row["file_id"]: dict(row) for row in inventory.execute("SELECT * FROM nodes")}
    plan = sqlite3.connect(args.plan)
    plan.row_factory = sqlite3.Row
    if plan.execute("SELECT COUNT(*) FROM operations WHERE status IN ('pending','failed')").fetchone()[0]:
        raise RuntimeError("residual isolation requires every media operation to complete")
    layout = json.loads(plan.execute("SELECT value FROM metadata WHERE key='layout_json'").fetchone()[0])
    root_cid = plan.execute("SELECT value FROM metadata WHERE key='root_cid'").fetchone()[0]
    target_names = set(layout["library_order"])
    top_nodes = {node["name"]: node for node in nodes.values() if node["parent_id"] == root_cid and node["is_folder"]}
    canonical_cids = collections.defaultdict(set)
    for parent_key, cid in plan.execute("SELECT parent_key,cid FROM directories WHERE parent_key LIKE 'library:%' AND status='ready' AND cid!=''"):
        canonical_cids[parent_key.removeprefix("library:")].add(cid)
    reserved = set(layout.get("reserved_directories", []))
    operations = []
    with plan:
        plan.execute("INSERT OR IGNORE INTO directories(key,parent_key,name) VALUES('residual:root','library:_待整理','原目录残留')")
        for source_name, source in sorted(top_nodes.items()):
            if source_name in reserved:
                continue
            if source_name not in target_names:
                operations.append((source, "residual:root", source_name))
                continue
            residual_key = "residual:" + source_name
            plan.execute("INSERT OR IGNORE INTO directories(key,parent_key,name) VALUES(?,?,?)", (residual_key, "residual:root", source_name))
            occupied = collections.Counter()
            for child in nodes.values():
                if child["parent_id"] != source["file_id"] or child["file_id"] in canonical_cids[source_name]:
                    continue
                target_name = child["name"]
                occupied[target_name] += 1
                if occupied[target_name] > 1:
                    target_name = trim_utf8(f"{target_name} [{child['file_id'][-8:]}]", 250)
                operations.append((child, residual_key, target_name))
        for node, target_key, target_name in operations:
            plan.execute(
                "INSERT INTO operations(phase,object_id,expected_parent_id,expected_name,expected_size,expected_sha1,target_directory_key,target_name,identity,disposition) VALUES(?,?,?,?,?,?,?,?,?,?)",
                (5, node["file_id"], node["parent_id"], node["name"], node["size"], node["sha1"], target_key, target_name, "residual", "review_residual"),
            )
    plan.close()
    inventory.close()
    print(json.dumps({"residual_objects_added": len(operations)}, ensure_ascii=False))


class ProviderRejected(RuntimeError):
    pass


def provider_request(cookie, method, url, values=None):
    data = urllib.parse.urlencode(values or {}).encode() if values is not None else None
    request = urllib.request.Request(url, data=data, method=method, headers={
        "Cookie": cookie,
        "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36",
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
    })
    last_error = None
    for attempt in range(6):
        try:
            with urllib.request.urlopen(request, timeout=60) as response:
                payload = json.load(response)
            if not payload.get("state"):
                raise ProviderRejected(payload.get("error") or payload.get("message") or "request rejected")
            return payload
        except ProviderRejected:
            raise
        except urllib.error.HTTPError as error:
            if error.code == 405:
                raise RuntimeError("115 returned HTTP 405; refresh the account cookie before resuming") from error
            last_error = error
            if attempt == 5:
                break
            time.sleep(2 ** attempt)
        except (OSError, ValueError, RuntimeError, http.client.HTTPException) as error:
            last_error = error
            if attempt == 5:
                break
            time.sleep(2 ** attempt)
    raise RuntimeError(f"115 request failed after retries: {last_error}")


def list_directory(cookie, cid):
    result = {}
    offset = 0
    total = None
    while total is None or len(result) < total:
        query = urllib.parse.urlencode({"aid": 1, "cid": cid, "offset": offset, "limit": PAGE_SIZE, "show_dir": 1, "format": "json"})
        payload = provider_request(cookie, "GET", "https://aps.115.com/natsort/files.php?" + query)
        total = int(payload.get("count") or 0)
        page = payload.get("data") or []
        before = len(result)
        for item in page:
            file_id = scalar(item.get("fid"))
            folder = not file_id
            value = {"id": scalar(item.get("cid")) if folder else file_id, "name": str(item.get("n") or ""), "folder": folder, "size": int(scalar(item.get("s")) or 0), "sha1": str(item.get("sha") or "").upper()}
            result[value["id"]] = value
        if not page or len(result) == before:
            break
        offset += len(page)
    if len(result) != total:
        raise RuntimeError(f"incomplete directory listing for {cid}: {len(result)} != {total}")
    return list(result.values())


def get_object(cookie, object_id):
    query = urllib.parse.urlencode({"file_id": object_id})
    payload = provider_request(cookie, "GET", "https://webapi.115.com/files/file?" + query)
    items = payload.get("data") or []
    if len(items) != 1:
        raise RuntimeError(f"115 object lookup returned {len(items)} rows for {object_id}")
    item = items[0]
    return {"id": scalar(item.get("file_id")), "name": str(item.get("file_name") or ""), "folder": False, "size": int(scalar(item.get("file_size")) or 0), "sha1": str(item.get("sha1") or "").upper()}


def search_object(cookie, parent_cid, name, object_id):
    offset = 0
    while offset < 50_000:
        query = urllib.parse.urlencode({"search_value": name, "offset": offset, "limit": 200, "cid": "0", "show_dir": 1})
        payload = provider_request(cookie, "GET", "https://webapi.115.com/files/search?" + query)
        page = payload.get("data") or []
        for item in page:
            file_id = scalar(item.get("fid"))
            candidate_id = file_id or scalar(item.get("cid"))
            candidate_parent = scalar(item.get("cid")) if file_id else scalar(item.get("pid"))
            if candidate_id == object_id and candidate_parent == parent_cid and str(item.get("n") or "") == name:
                return True
        offset += len(page)
        if not page or offset >= int(payload.get("count") or 0):
            break
    return False


def locate_object(cookie, operation, target_cid, verify_parent=True):
    current = get_object(cookie, operation["object_id"])
    if current["name"] == operation["target_name"]:
        current["parent_id"] = target_cid
    elif current["name"] == operation["expected_name"]:
        current["parent_id"] = target_cid if operation["status"] in {"moved", "renamed"} else operation["expected_parent_id"]
    else:
        current["parent_id"] = ""
    return current

def find_directory(cookie, parent_cid, name):
    query = urllib.parse.urlencode({"search_value": name, "offset": 0, "limit": 200, "cid": parent_cid, "show_dir": 1})
    payload = provider_request(cookie, "GET", "https://webapi.115.com/files/search?" + query)
    matches = [item for item in payload.get("data") or [] if not scalar(item.get("fid")) and scalar(item.get("pid")) == parent_cid and str(item.get("n") or "") == name]
    if len(matches) != 1:
        raise RuntimeError(f"scoped 115 search found {len(matches)} directories for {parent_cid}/{name}")
    return scalar(matches[0].get("cid"))


def mkdir(cookie, parent_cid, name):
    payload = provider_request(cookie, "POST", "https://webapi.115.com/files/add", {"pid": parent_cid, "cname": name})
    cid = scalar(payload.get("cid"))
    if not cid:
        raise RuntimeError("mkdir response omitted CID")
    return cid


def move(cookie, object_ids, target_cid):
    values = {"pid": target_cid}
    for index, object_id in enumerate(object_ids):
        values[f"fid[{index}]"] = object_id
    provider_request(cookie, "POST", "https://webapi.115.com/files/move", values)


def rename(cookie, object_id, name):
    provider_request(cookie, "POST", "https://webapi.115.com/files/edit", {"fid": object_id, "file_name": name})


def load_cookie_and_assert_maintenance():
    database = sqlite3.connect(f"file:{SOURCE_DATABASE}?mode=ro", uri=True)
    enabled = database.execute("SELECT COUNT(*) FROM scheduled_tasks WHERE status != 'paused'").fetchone()[0]
    active = database.execute("SELECT COUNT(*) FROM async_tasks WHERE status IN ('pending','running')").fetchone()[0]
    row = database.execute("SELECT cookie FROM drive_accounts WHERE is_default=1 ORDER BY created_at LIMIT 1").fetchone()
    database.close()
    if enabled or active:
        raise RuntimeError(f"maintenance precondition failed: enabled schedules={enabled}, active tasks={active}")
    if not row or not row[0].strip():
        raise RuntimeError("default 115 account cookie is unavailable")
    return row[0].strip()


def command_prepare_directories(args):
    cookie = load_cookie_and_assert_maintenance()
    print(json.dumps({"state": "directory preparation started"}, ensure_ascii=False), flush=True)
    inventory = sqlite3.connect(f"file:{args.inventory}?mode=ro", uri=True)
    inventory.row_factory = sqlite3.Row
    children = collections.defaultdict(lambda: collections.defaultdict(list))
    for row in inventory.execute("SELECT file_id,parent_id,name,is_folder FROM nodes WHERE is_folder=1"):
        children[row["parent_id"]][row["name"]].append(row["file_id"])
    plan = sqlite3.connect(args.plan)
    plan.row_factory = sqlite3.Row
    directories = {row["key"]: dict(row) for row in plan.execute("SELECT * FROM directories")}
    unresolved = {key for key, directory in directories.items() if key != "root" and directory["status"] != "ready"}
    created = 0
    workers = max(1, min(args.workers, 4))
    while unresolved:
        ready_keys = [key for key in sorted(unresolved) if directories[directories[key]["parent_key"]]["cid"]]
        if not ready_keys:
            raise RuntimeError("normalization directory graph contains an unresolved parent cycle")
        resolved = []
        creates = []
        for key in ready_keys:
            directory = directories[key]
            parent_cid = directories[directory["parent_key"]]["cid"]
            matches = children[parent_cid][directory["name"]]
            if len(matches) > 1:
                raise RuntimeError(f"frozen inventory contains duplicate destination directories: {parent_cid}/{directory['name']}")
            if matches:
                resolved.append((key, matches[0]))
            else:
                creates.append((key, parent_cid, directory["name"]))
        def create_directory(specification):
            key, parent_cid, name = specification
            try:
                cid = mkdir(cookie, parent_cid, name)
            except ProviderRejected as error:
                if "已存在" not in str(error):
                    raise
                cid = find_directory(cookie, parent_cid, name)
            time.sleep(args.delay)
            return key, cid
        if workers == 1:
            created_values = [create_directory(specification) for specification in creates]
        else:
            with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
                created_values = list(executor.map(create_directory, creates))
        created += len(created_values)
        resolved.extend(created_values)
        with plan:
            plan.executemany("UPDATE directories SET cid=?,status='ready',error='' WHERE key=?", ((cid, key) for key, cid in resolved))
        for key, cid in resolved:
            directories[key]["cid"] = cid
            directories[key]["status"] = "ready"
            unresolved.remove(key)
    plan.close()
    inventory.close()
    print(json.dumps({"directories_ready": len(directories) - 1, "created": created}, ensure_ascii=False))


def command_apply(args):
    cookie = load_cookie_and_assert_maintenance()
    print(json.dumps({"state": "normalization apply started"}, ensure_ascii=False), flush=True)
    plan = sqlite3.connect(args.plan)
    plan.row_factory = sqlite3.Row
    directories = {row["key"]: dict(row) for row in plan.execute("SELECT * FROM directories")}
    unresolved = {key for key, directory in directories.items() if key != "root" and directory["status"] != "ready"}
    workers = max(1, min(args.workers, 8))
    while unresolved:
        ready_keys = [key for key in sorted(unresolved) if directories[directories[key]["parent_key"]]["cid"]]
        if not ready_keys:
            raise RuntimeError("normalization directory graph contains an unresolved parent cycle")
        by_parent = collections.defaultdict(list)
        for key in ready_keys:
            parent = directories[directories[key]["parent_key"]]
            by_parent[parent["cid"]].append(key)
        resolved = []
        creates = []
        for parent_cid, keys in by_parent.items():
            existing = collections.defaultdict(list)
            for item in list_directory(cookie, parent_cid):
                if item["folder"]:
                    existing[item["name"]].append(item)
            for key in keys:
                directory = directories[key]
                matches = existing[directory["name"]]
                if len(matches) > 1:
                    raise RuntimeError(f"duplicate target directories under {parent_cid}: {directory['name']}")
                if matches:
                    resolved.append((key, matches[0]["id"]))
                else:
                    creates.append((key, parent_cid, directory["name"]))
        def create_directory(specification):
            key, parent_cid, name = specification
            cid = mkdir(cookie, parent_cid, name)
            time.sleep(args.delay)
            return key, cid
        if workers == 1:
            resolved.extend(create_directory(specification) for specification in creates)
        else:
            with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
                resolved.extend(executor.map(create_directory, creates))
        with plan:
            plan.executemany("UPDATE directories SET cid=?,status='ready',error='' WHERE key=?", ((cid, key) for key, cid in resolved))
        for key, cid in resolved:
            directories[key]["cid"] = cid
            directories[key]["status"] = "ready"
            unresolved.remove(key)
    pending = plan.execute("SELECT * FROM operations WHERE status IN ('pending','moved','renamed') ORDER BY phase,target_directory_key,id").fetchall()
    groups = collections.defaultdict(list)
    for operation in pending:
        groups[(operation["phase"], operation["target_directory_key"])].append(operation)
    completed_count = plan.execute("SELECT COUNT(*) FROM operations WHERE status='completed'").fetchone()[0]
    total_count = completed_count + len(pending)
    for (phase, directory_key), operations in groups.items():
        target_cid = directories[directory_key]["cid"]
        planned_names = collections.Counter(operation["target_name"] for operation in operations)
        duplicate_names = [name for name, count in planned_names.items() if count > 1]
        if duplicate_names:
            raise RuntimeError(f"plan contains duplicate target names in {directory_key}: {duplicate_names[:5]}")
        def read_current(operation):
            return operation, locate_object(cookie, operation, target_cid)
        if workers == 1:
            current_values = [read_current(operation) for operation in operations]
        else:
            with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
                current_values = list(executor.map(read_current, operations))
        prepared = []
        for operation, current in current_values:
            if current["parent_id"] not in {operation["expected_parent_id"], target_cid}:
                with plan:
                    plan.execute("UPDATE operations SET status='failed',error='object absent from expected source and target' WHERE id=?", (operation["id"],))
                raise RuntimeError(f"115 object moved outside the plan: {operation['object_id']}")
            if current["size"] != operation["expected_size"] or (operation["expected_sha1"] and current["sha1"] != operation["expected_sha1"]):
                with plan:
                    plan.execute("UPDATE operations SET status='failed',error='object bytes changed' WHERE id=?", (operation["id"],))
                raise RuntimeError(f"115 object bytes changed: {operation['object_id']}")
            already_target = current["parent_id"] == target_cid
            allowed_names = {operation["expected_name"]}
            if already_target:
                allowed_names.add(operation["target_name"])
            if current["name"] not in allowed_names:
                with plan:
                    plan.execute("UPDATE operations SET status='failed',error='object name changed' WHERE id=?", (operation["id"],))
                raise RuntimeError(f"115 object name changed: {operation['object_id']}")
            prepared.append((operation, current, already_target))
        moves = collections.defaultdict(list)
        for operation, _, already_target in prepared:
            if not already_target:
                moves[operation["expected_parent_id"]].append(operation["object_id"])
        for object_ids in moves.values():
            for start in range(0, len(object_ids), 50):
                move(cookie, object_ids[start:start + 50], target_cid)
                time.sleep(args.delay)
        moved_ids = {object_id for object_ids in moves.values() for object_id in object_ids}
        if moved_ids:
            with plan:
                plan.executemany("UPDATE operations SET status='moved',error='' WHERE object_id=?", ((object_id,) for object_id in moved_ids))
        def rename_operation(entry):
            operation, current, _ = entry
            if current["name"] != operation["target_name"]:
                rename(cookie, operation["object_id"], operation["target_name"])
                time.sleep(args.delay)
        if workers == 1:
            for entry in prepared:
                rename_operation(entry)
        else:
            with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
                list(executor.map(rename_operation, prepared))
        with plan:
            plan.executemany("UPDATE operations SET status='renamed',error='' WHERE id=?", ((operation["id"],) for operation, _, _ in prepared))
        def read_verified(entry):
            item = get_object(cookie, entry[0]["object_id"])
            item["parent_id"] = target_cid
            return item
        verified = None
        for attempt in range(6):
            if workers == 1:
                values = [read_verified(entry) for entry in prepared]
            else:
                with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
                    values = list(executor.map(read_verified, prepared))
            verified = {item["id"]: item for item in values}
            if all(operation["object_id"] in verified and verified[operation["object_id"]]["name"] == operation["target_name"] for operation, _, _ in prepared):
                break
            time.sleep(2 ** attempt)
        for operation, _, _ in prepared:
            item = verified.get(operation["object_id"]) if verified else None
            if not item or item["parent_id"] != target_cid or item["name"] != operation["target_name"] or item["size"] != operation["expected_size"] or (operation["expected_sha1"] and item["sha1"] != operation["expected_sha1"]):
                raise RuntimeError(f"postcondition failed for {operation['object_id']}")
        with plan:
            plan.executemany("UPDATE operations SET status='completed',error='' WHERE id=?", ((operation["id"],) for operation, _, _ in prepared))
        completed_count += len(prepared)
        print(json.dumps({"completed": completed_count, "total": total_count, "target": directory_key, "objects": len(prepared)}, ensure_ascii=False), flush=True)
    plan.close()


def command_apply_singletons(args):
    cookie = load_cookie_and_assert_maintenance()
    print(json.dumps({"state": "singleton normalization started"}, ensure_ascii=False), flush=True)
    plan = sqlite3.connect(args.plan)
    plan.row_factory = sqlite3.Row
    directories = {row["key"]: dict(row) for row in plan.execute("SELECT * FROM directories")}
    rows = plan.execute("SELECT * FROM operations WHERE status IN ('pending','moved','renamed') AND disposition!='review_residual' ORDER BY phase,target_directory_key,id").fetchall()
    grouped = collections.defaultdict(list)
    for operation in rows:
        grouped[operation["target_directory_key"]].append(operation)
    operations = [values[0] for values in grouped.values() if len(values) == 1]
    workers = max(1, min(args.workers, 8))

    def parallel(entries, function):
        if workers == 1:
            return [function(entry) for entry in entries]
        results = []
        errors = []
        with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
            futures = {executor.submit(function, entry): entry for entry in entries}
            for future in concurrent.futures.as_completed(futures):
                try:
                    results.append(future.result())
                except BaseException as error:
                    errors.append(error)
        if errors:
            raise errors[0]
        return results

    completed = 0
    for start in range(0, len(operations), args.batch_size):
        batch = operations[start:start + args.batch_size]

        def read_entry(operation):
            target_cid = directories[operation["target_directory_key"]]["cid"]
            return operation, target_cid, locate_object(cookie, operation, target_cid, verify_parent=False)

        current = parallel(batch, read_entry)
        for operation, target_cid, item in current:
            if item["parent_id"] not in {operation["expected_parent_id"], target_cid} or item["name"] not in {operation["expected_name"], operation["target_name"]}:
                raise RuntimeError(f"115 singleton object changed: {operation['object_id']}")
            if item["size"] != operation["expected_size"] or (operation["expected_sha1"] and item["sha1"] != operation["expected_sha1"]):
                raise RuntimeError(f"115 singleton bytes changed: {operation['object_id']}")

        def move_entry(entry):
            operation, target_cid, item = entry
            if item["parent_id"] != target_cid:
                move(cookie, [operation["object_id"]], target_cid)
                time.sleep(args.delay)
            return operation

        moved = parallel(current, move_entry)
        with plan:
            plan.executemany("UPDATE operations SET status='moved',error='' WHERE id=?", ((operation["id"],) for operation in moved))

        def rename_entry(entry):
            operation, _, item = entry
            if item["name"] != operation["target_name"]:
                rename(cookie, operation["object_id"], operation["target_name"])
                time.sleep(args.delay)
            return operation

        renamed = parallel(current, rename_entry)
        with plan:
            plan.executemany("UPDATE operations SET status='renamed',error='' WHERE id=?", ((operation["id"],) for operation in renamed))

        def verify_entry(entry):
            operation, _, _ = entry
            item = get_object(cookie, operation["object_id"])
            if item["name"] != operation["target_name"] or item["size"] != operation["expected_size"] or (operation["expected_sha1"] and item["sha1"] != operation["expected_sha1"]):
                raise RuntimeError(f"singleton postcondition failed: {operation['object_id']}")
            return operation

        verified = parallel(current, verify_entry)
        with plan:
            plan.executemany("UPDATE operations SET status='completed',error='' WHERE id=?", ((operation["id"],) for operation in verified))
        completed += len(verified)
        print(json.dumps({"completed_singletons": completed, "total_singletons": len(operations)}, ensure_ascii=False), flush=True)
    plan.close()



def command_stage_duplicates(args):
    cookie = load_cookie_and_assert_maintenance()
    print(json.dumps({"state": "duplicate staging started"}, ensure_ascii=False), flush=True)
    plan = sqlite3.connect(args.plan)
    plan.row_factory = sqlite3.Row
    target = plan.execute("SELECT cid FROM directories WHERE key='library:_待回收' AND status='ready'").fetchone()
    if not target or not target["cid"]:
        raise RuntimeError("_待回收 directory is unavailable")
    target_cid = target["cid"]
    rows = plan.execute("SELECT * FROM operations WHERE disposition='exact_duplicate' AND status IN ('pending','moved','renamed') ORDER BY id").fetchall()
    pending = [operation for operation in rows if operation["status"] == "pending"]
    for start in range(0, len(pending), 50):
        batch = pending[start:start + 50]
        move(cookie, [operation["object_id"] for operation in batch], target_cid)
        with plan:
            plan.executemany("UPDATE operations SET status='moved',error='' WHERE id=?", ((operation["id"],) for operation in batch))
        time.sleep(args.delay)
        print(json.dumps({"moved_duplicates": min(start + len(batch), len(pending)), "total_to_move": len(pending)}, ensure_ascii=False), flush=True)
    rename_rows = plan.execute("SELECT * FROM operations WHERE disposition='exact_duplicate' AND status='moved' ORDER BY id").fetchall()
    workers = max(1, min(args.workers, 8))
    for start in range(0, len(rename_rows), 64):
        batch = rename_rows[start:start + 64]
        def rename_entry(operation):
            rename(cookie, operation["object_id"], operation["target_name"])
            time.sleep(args.delay)
            return operation
        if workers == 1:
            renamed = [rename_entry(operation) for operation in batch]
        else:
            renamed = []
            errors = []
            with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
                futures = {executor.submit(rename_entry, operation): operation for operation in batch}
                for future in concurrent.futures.as_completed(futures):
                    try:
                        renamed.append(future.result())
                    except BaseException as error:
                        errors.append(error)
            with plan:
                plan.executemany("UPDATE operations SET status='completed',error='' WHERE id=?", ((operation["id"],) for operation in renamed))
            if errors:
                raise errors[0]
        with plan:
            plan.executemany("UPDATE operations SET status='completed',error='' WHERE id=?", ((operation["id"],) for operation in renamed))
        print(json.dumps({"completed_duplicates": min(start + len(batch), len(rename_rows)), "total_to_rename": len(rename_rows)}, ensure_ascii=False), flush=True)
    with plan:
        plan.execute("UPDATE operations SET status='completed',error='' WHERE disposition='exact_duplicate' AND status='renamed'")
    plan.close()


def command_stage_duplicates_fs(args):
    print(json.dumps({"state": "CloudDrive duplicate staging started"}, ensure_ascii=False), flush=True)
    media_root = pathlib.Path(args.media_root).resolve(strict=True)
    if not os.path.ismount(media_root):
        raise RuntimeError(f"media root is not a mounted filesystem: {media_root}")
    target_root = (media_root / "_待回收").resolve(strict=True)
    target_root.relative_to(media_root)
    inventory = sqlite3.connect(f"file:{args.inventory}?mode=ro", uri=True)
    inventory.row_factory = sqlite3.Row
    nodes = {row["file_id"]: dict(row) for row in inventory.execute("SELECT * FROM nodes")}
    plan = sqlite3.connect(args.plan)
    plan.row_factory = sqlite3.Row
    root_id = plan.execute("SELECT value FROM metadata WHERE key='root_cid'").fetchone()[0]
    operations = plan.execute("SELECT * FROM operations WHERE disposition='exact_duplicate' AND status IN ('pending','moved','renamed') ORDER BY id").fetchall()
    workers = max(1, min(args.workers, 8))

    def apply_operation(operation):
        node = nodes[operation["object_id"]]
        source = media_root / provider_path(nodes, root_id, node["file_id"])
        target = target_root / operation["target_name"]
        if target.exists():
            if target.is_symlink() or not target.is_file() or target.stat().st_size != operation["expected_size"]:
                raise RuntimeError(f"CloudDrive duplicate target changed: {target}")
            return operation
        if source.is_symlink() or not source.is_file():
            raise RuntimeError(f"CloudDrive duplicate source is unavailable: {source}")
        if source.stat().st_size != operation["expected_size"]:
            raise RuntimeError(f"CloudDrive duplicate source size changed: {source}")
        if target.exists():
            raise RuntimeError(f"CloudDrive duplicate target appeared: {target}")
        os.rename(source, target)
        if not target.is_file() or target.stat().st_size != operation["expected_size"] or source.exists():
            raise RuntimeError(f"CloudDrive duplicate move postcondition failed: {operation['object_id']}")
        return operation

    completed = 0
    for start in range(0, len(operations), args.batch_size):
        batch = operations[start:start + args.batch_size]
        results = []
        errors = []
        if workers == 1:
            for operation in batch:
                try:
                    results.append(apply_operation(operation))
                except BaseException as error:
                    errors.append(error)
                    break
        else:
            with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
                futures = {executor.submit(apply_operation, operation): operation for operation in batch}
                for future in concurrent.futures.as_completed(futures):
                    try:
                        results.append(future.result())
                    except BaseException as error:
                        errors.append(error)
        with plan:
            plan.executemany("UPDATE operations SET status='completed',error='' WHERE id=?", ((operation["id"],) for operation in results))
        completed += len(results)
        print(json.dumps({"completed_duplicates": completed, "total_duplicates": len(operations)}, ensure_ascii=False), flush=True)
        if errors:
            raise errors[0]
    plan.close()
    inventory.close()


def command_isolate_residuals_fs(args):
    print(json.dumps({"state": "CloudDrive residual isolation started"}, ensure_ascii=False), flush=True)
    media_root = pathlib.Path(args.media_root).resolve(strict=True)
    if not os.path.ismount(media_root):
        raise RuntimeError(f"media root is not a mounted filesystem: {media_root}")
    plan = sqlite3.connect(f"file:{args.plan}?mode=ro", uri=True)
    plan.row_factory = sqlite3.Row
    layout = json.loads(plan.execute("SELECT value FROM metadata WHERE key='layout_json'").fetchone()[0])
    directories = {row["key"]: dict(row) for row in plan.execute("SELECT * FROM directories")}
    canonical = collections.defaultdict(set)
    for row in plan.execute("SELECT target_directory_key FROM operations WHERE status='completed' AND disposition IN ('canonical','canonical_attachment')"):
        key = row["target_directory_key"]
        while directories[key]["parent_key"] and not directories[key]["parent_key"].startswith("library:"):
            key = directories[key]["parent_key"]
        parent = directories[key]["parent_key"]
        if parent.startswith("library:"):
            canonical[parent.removeprefix("library:")].add(directories[key]["name"])
    plan.close()
    target_libraries = set(layout["library_order"])
    reserved = set(layout.get("reserved_directories", [])) | {"_待整理", "_待回收"}
    review_root = media_root / "_待整理" / "原目录残留"
    review_root.mkdir(parents=True, exist_ok=True)
    reserved_targets = set()

    def unique_target(parent, name):
        target = parent / name
        index = 2
        while target.exists() or target in reserved_targets:
            target = parent / f"{name} [残留-{index}]"
            index += 1
        reserved_targets.add(target)
        return target

    planned = []
    for source in sorted(media_root.iterdir(), key=lambda path: path.name):
        if source.name in reserved or source.name.startswith(".embymedia-"):
            continue
        if source.name not in target_libraries:
            planned.append((source, unique_target(review_root, source.name)))
            continue
        allowed = canonical[source.name]
        leftovers = [child for child in source.iterdir() if child.name not in allowed]
        if not leftovers:
            continue
        library_review = review_root / source.name
        library_review.mkdir(parents=True, exist_ok=True)
        for child in leftovers:
            planned.append((child, unique_target(library_review, child.name)))

    def move_residual(pair):
        source, target = pair
        if source.is_symlink():
            raise RuntimeError(f"CloudDrive residual source is a symlink: {source}")
        os.rename(source, target)
        if source.exists() or not target.exists():
            raise RuntimeError(f"CloudDrive residual move failed: {source}")
        return {"source": str(source), "target": str(target)}

    workers = max(1, min(args.workers, 8))
    moves = []
    for start in range(0, len(planned), 64):
        batch = planned[start:start + 64]
        errors = []
        if workers == 1:
            for pair in batch:
                try:
                    moves.append(move_residual(pair))
                except BaseException as error:
                    errors.append(error)
                    break
        else:
            with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
                futures = [executor.submit(move_residual, pair) for pair in batch]
                for future in concurrent.futures.as_completed(futures):
                    try:
                        moves.append(future.result())
                    except BaseException as error:
                        errors.append(error)
        pathlib.Path(args.manifest).write_text(json.dumps({"moves": moves}, ensure_ascii=False, indent=2) + "\n")
        os.chmod(args.manifest, 0o600)
        print(json.dumps({"residual_objects_moved": len(moves), "total_residual_objects": len(planned)}, ensure_ascii=False), flush=True)
        if errors:
            raise errors[0]
    print(json.dumps({"residual_objects_moved": len(moves), "manifest": args.manifest}, ensure_ascii=False), flush=True)

def build_parser():
    parser = argparse.ArgumentParser()
    commands = parser.add_subparsers(dest="command", required=True)
    plan = commands.add_parser("plan")


    plan.add_argument("--inventory", required=True)
    plan.add_argument("--emby", required=True)
    plan.add_argument("--strm", required=True)
    plan.add_argument("--layout", required=True)
    plan.add_argument("--output", required=True)
    apply = commands.add_parser("apply")
    apply.add_argument("--plan", required=True)
    singletons = commands.add_parser("apply-singletons")
    singletons.add_argument("--plan", required=True)
    singletons.add_argument("--delay", type=float, default=0.2)
    singletons.add_argument("--workers", type=int, choices=range(1, 9), default=8)
    singletons.add_argument("--batch-size", type=int, choices=range(1, 129), default=64)
    duplicates = commands.add_parser("stage-duplicates")
    duplicates.add_argument("--plan", required=True)
    duplicates.add_argument("--delay", type=float, default=0.2)
    duplicates.add_argument("--workers", type=int, choices=range(1, 9), default=4)
    duplicates_fs = commands.add_parser("stage-duplicates-fs")
    duplicates_fs.add_argument("--plan", required=True)
    duplicates_fs.add_argument("--inventory", required=True)
    duplicates_fs.add_argument("--media-root", required=True)
    duplicates_fs.add_argument("--workers", type=int, choices=range(1, 9), default=4)
    duplicates_fs.add_argument("--batch-size", type=int, choices=range(1, 129), default=32)
    residuals_fs = commands.add_parser("isolate-residuals-fs")
    residuals_fs.add_argument("--plan", required=True)
    residuals_fs.add_argument("--media-root", required=True)
    residuals_fs.add_argument("--manifest", required=True)
    residuals_fs.add_argument("--workers", type=int, choices=range(1, 9), default=8)
    subtitles = commands.add_parser("augment-subtitles")
    subtitles.add_argument("--inventory", required=True)
    subtitles.add_argument("--plan", required=True)
    residuals = commands.add_parser("augment-residuals")
    residuals.add_argument("--inventory", required=True)
    residuals.add_argument("--plan", required=True)
    prepare = commands.add_parser("prepare-directories")
    prepare.add_argument("--inventory", required=True)
    prepare.add_argument("--plan", required=True)
    prepare.add_argument("--delay", type=float, default=0.5)
    prepare.add_argument("--workers", type=int, choices=range(1, 5), default=4)
    apply.add_argument("--delay", type=float, default=0.25)
    apply.add_argument("--workers", type=int, choices=range(1, 9), default=8)
    return parser


def main():
    args = build_parser().parse_args()
    if args.command == "plan":
        command_plan(args)
    elif args.command == "augment-subtitles":
        command_augment_subtitles(args)
    elif args.command == "augment-residuals":
        command_augment_residuals(args)
    elif args.command == "prepare-directories":
        command_prepare_directories(args)
    elif args.command == "apply-singletons":
        command_apply_singletons(args)
    elif args.command == "stage-duplicates":
        command_stage_duplicates(args)
    elif args.command == "stage-duplicates-fs":
        command_stage_duplicates_fs(args)
    elif args.command == "isolate-residuals-fs":
        command_isolate_residuals_fs(args)
    else:
        command_apply(args)
if __name__ == "__main__":
    main()
