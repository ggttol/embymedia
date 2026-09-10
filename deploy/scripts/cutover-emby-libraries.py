#!/usr/bin/env python3
"""Create, verify, and cut over normalized Emby libraries with user-state preservation."""

import argparse
import base64
import collections
import copy
import json
import os
import pathlib
import re
import sqlite3
import time
import urllib.parse
import urllib.request

SOURCE_DATABASE = "/srv/embymedia/data/embymedia.db"
TEMP_PREFIX = "整理中·"

def load_connection():
    database = sqlite3.connect(f"file:{SOURCE_DATABASE}?mode=ro", uri=True)
    settings = dict(database.execute("SELECT key,value FROM system_settings WHERE key IN ('emby_url','emby_api_key')"))
    database.close()
    return settings["emby_url"].rstrip("/"), settings["emby_api_key"]


def request(base, token, method, path, body=None, content_type="application/json", params=None):
    if params:
        path += "?" + urllib.parse.urlencode(params)
    data = None
    if body is not None:
        data = body if isinstance(body, bytes) else json.dumps(body, ensure_ascii=False).encode()
    headers = {"X-Emby-Token": token}
    if data is not None:
        headers["Content-Type"] = content_type
    response = urllib.request.urlopen(urllib.request.Request(base + path, data=data, headers=headers, method=method), timeout=120)
    raw = response.read()
    if not raw:
        return None
    return json.loads(raw)


def get(base, token, path, params=None):
    return request(base, token, "GET", path, params=params)


def post(base, token, path, body=None, content_type="application/json", params=None):
    return request(base, token, "POST", path, body, content_type, params)


def paged(base, token, path, params):
    start = 0
    total = None
    while total is None or start < total:
        page = get(base, token, path, {**params, "StartIndex": start, "Limit": 1000})
        items = page.get("Items") or []
        observed = int(page.get("TotalRecordCount") or 0)
        if total is None:
            total = observed
        elif total != observed:
            raise RuntimeError(f"Emby inventory changed during verification: {total}->{observed}")
        yield from items
        if not items:
            break
        start += len(items)
    if start != total:
        raise RuntimeError(f"incomplete Emby inventory: {start}!={total}")


def libraries(base, token):
    return get(base, token, "/Library/VirtualFolders")


def wait_for_scan(base, token, started_after):
    scan_id = None
    deadline = time.monotonic() + 4 * 60 * 60
    while time.monotonic() < deadline:
        tasks = get(base, token, "/ScheduledTasks")
        scan = next((task for task in tasks if task.get("Key") == "RefreshLibrary"), None)
        if not scan:
            raise RuntimeError("Emby RefreshLibrary task is unavailable")
        scan_id = scan["Id"]
        result = scan.get("LastExecutionResult") or {}
        start_text = result.get("StartTimeUtc") or ""
        if scan.get("State") == "Idle" and start_text >= started_after:
            if result.get("Status") != "Completed":
                raise RuntimeError(f"Emby scan ended with {result.get('Status')}")
            return {"task_id": scan_id, "result": result}
        time.sleep(5)
    raise RuntimeError(f"Emby scan {scan_id or 'unknown'} did not complete before the deadline")


def write_state(path, state):
    pathlib.Path(path).write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")
    os.chmod(path, 0o600)


def command_prepare(args):
    print(json.dumps({"state": "Emby library preparation started"}, ensure_ascii=False), flush=True)
    layout = json.loads(pathlib.Path(args.layout).read_text())
    base, token = load_connection()
    current = libraries(base, token)
    by_name = {library["Name"]: library for library in current}
    movie_template = next(library for library in current if library.get("CollectionType") == "movies")["LibraryOptions"]
    series_template = next(library for library in current if library.get("CollectionType") == "tvshows")["LibraryOptions"]
    state = {
        "version": 1,
        "old_libraries": current,
        "new_libraries": {},
        "posters_root": str(pathlib.Path(args.posters)),
        "user_configurations": {user["Id"]: user.get("Configuration") or {} for user in get(base, token, "/Users")},
        "prepared": False,
        "verified": False,
        "user_state_migrated": False,
        "cutover": False,
    }
    for specification in layout["libraries"]:
        name = specification["name"]
        temporary_name = TEMP_PREFIX + name
        location = "/strm-v2/" + name
        existing = by_name.get(temporary_name)
        if existing:
            if existing.get("CollectionType") != specification["collection_type"] or existing.get("Locations") != [location]:
                raise RuntimeError(f"existing temporary library differs: {temporary_name}")
        else:
            options = copy.deepcopy(movie_template if specification["collection_type"] == "movies" else series_template)
            options["ContentType"] = specification["collection_type"]
            options["PathInfos"] = [{"Path": location}]
            post(base, token, "/Library/VirtualFolders", {
                "Name": temporary_name,
                "CollectionType": specification["collection_type"],
                "RefreshLibrary": False,
                "Paths": [location],
                "LibraryOptions": options,
            })
            existing = next((library for library in libraries(base, token) if library["Name"] == temporary_name), None)
            if not existing:
                raise RuntimeError(f"Emby did not retain library {temporary_name}")
        poster = pathlib.Path(args.posters) / specification["poster"]
        if not poster.is_file():
            raise RuntimeError(f"library poster is missing: {poster}")
        post(base, token, f"/Items/{existing['ItemId']}/Images/Primary", base64.b64encode(poster.read_bytes()), "image/jpeg")
        state["new_libraries"][name] = {
            "temporary_name": temporary_name,
            "item_id": existing["ItemId"],
            "guid": existing["Guid"],
            "location": location,
            "collection_type": specification["collection_type"],
            "poster": str(poster),
        }
    state["prepared"] = True
    write_state(args.state, state)
    started_after = time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime())
    post(base, token, "/Library/Refresh")
    state["scan"] = wait_for_scan(base, token, started_after)
    write_state(args.state, state)
    print(json.dumps({"prepared": len(state["new_libraries"]), "scan": state["scan"]}, ensure_ascii=False))


def expected_catalog(plan_path):
    database = sqlite3.connect(f"file:{plan_path}?mode=ro", uri=True)
    identities = {}
    episodes = {}
    for directory_key, identity, target_name in database.execute("SELECT target_directory_key,identity,target_name FROM operations WHERE disposition='canonical' AND status='completed'"):
        library = directory_key.split(":", 2)[1]
        identity_counts = identities.setdefault(library, collections.Counter())
        if identity.startswith("movie:"):
            identity_counts[identity] += 1
        else:
            identity_counts[identity] = 1
        if identity.startswith("series:") and directory_key.startswith("season:"):
            match = re.search(r"S(\d+)E(\d+)(?:-E(\d+))?", target_name)
            if match:
                season, first, last = int(match.group(1)), int(match.group(2)), int(match.group(3) or match.group(2))
                episode_counts = episodes.setdefault(library, collections.Counter())
                for episode in range(first, last + 1):
                    episode_counts[(identity, season, episode)] += 1
    database.close()
    return identities, episodes


def command_verify(args):
    state = json.loads(pathlib.Path(args.state).read_text())
    if not state.get("prepared"):
        raise RuntimeError("normalized libraries are not prepared")
    expected, expected_episodes = expected_catalog(args.plan)
    if set(state["new_libraries"]) != set(expected):
        raise RuntimeError(f"replacement library set differs from the plan: state={sorted(state['new_libraries'])} plan={sorted(expected)}")
    base, token = load_connection()
    report = {}
    for name, library in state["new_libraries"].items():
        movie_library = library["collection_type"] == "movies"
        item_types = "Movie" if movie_library else "Series,Episode"
        items = list(paged(base, token, "/Items", {"ParentId": library["item_id"], "Recursive": "true", "IncludeItemTypes": item_types, "Fields": "Path,ProviderIds,ImageTags,SeriesId,ParentIndexNumber,IndexNumber,IndexNumberEnd"}))
        observed = collections.Counter()
        missing_identity = []
        series_identity = {}
        for item in items:
            if item["Type"] not in {"Movie", "Series"}:
                continue
            tmdb = (item.get("ProviderIds") or {}).get("Tmdb")
            if not tmdb:
                missing_identity.append({"id": item["Id"], "name": item["Name"], "path": item.get("Path")})
                continue
            key = ("movie:" if item["Type"] == "Movie" else "series:") + tmdb
            observed[key] += 1
            if item["Type"] == "Series":
                series_identity[item["Id"]] = key
        observed_episodes = collections.Counter()
        if not movie_library:
            for item in items:
                if item["Type"] != "Episode":
                    continue
                identity = series_identity.get(item.get("SeriesId"))
                if identity and item.get("ParentIndexNumber") is not None and item.get("IndexNumber") is not None:
                    first = int(item["IndexNumber"])
                    last = int(item.get("IndexNumberEnd") or first)
                    for episode in range(first, last + 1):
                        observed_episodes[(identity, int(item["ParentIndexNumber"]), episode)] += 1
        expected_identities = expected.get(name, collections.Counter())
        expected_library_episodes = expected_episodes.get(name, collections.Counter())
        missing = expected_identities - observed
        unexpected = observed - expected_identities
        missing_episodes = expected_library_episodes - observed_episodes
        unexpected_episodes = observed_episodes - expected_library_episodes
        if missing or missing_identity or unexpected or missing_episodes or unexpected_episodes:
            raise RuntimeError(json.dumps({"library": name, "missing": dict(missing), "missing_identity": missing_identity[:20], "unexpected": dict(unexpected), "missing_episodes": [[*key, count] for key, count in missing_episodes.items()][:20], "unexpected_episodes": [[*key, count] for key, count in unexpected_episodes.items()][:20]}, ensure_ascii=False))
        report[name] = {"items": len(items), "identities": len(observed), "episodes": sum(observed_episodes.values())}
    state["verified"] = True
    state["verification"] = report
    write_state(args.state, state)
    print(json.dumps(report, ensure_ascii=False, indent=2))


def stable_key(item, all_items, overrides, state_overrides=None):
    if state_overrides and item["id"] in state_overrides:
        return state_overrides[item["id"]]
    providers = json.loads(item["provider_ids_json"])
    if item["type"] == "Movie":
        tmdb = providers.get("Tmdb")
        return f"movie:{tmdb}" if tmdb else None
    if item["type"] == "Series":
        override = overrides.get(item["id"])
        tmdb = str(override["tmdb_id"]) if override else providers.get("Tmdb")
        return f"series:{tmdb}" if tmdb else None
    if item["type"] == "Episode":
        series = all_items.get(item["series_id"])
        if not series:
            return None
        override = overrides.get(series["id"])
        tmdb = str(override["tmdb_id"]) if override else json.loads(series["provider_ids_json"]).get("Tmdb")
        if not tmdb or item["index_number"] is None or item["parent_index_number"] is None:
            return None
        return f"episode:{tmdb}:{int(item['parent_index_number'])}:{int(item['index_number'])}"
    return None


def command_migrate_state(args):
    state = json.loads(pathlib.Path(args.state).read_text())
    if not state.get("verified"):
        raise RuntimeError("normalized libraries have not passed identity verification")
    layout = json.loads(pathlib.Path(args.layout).read_text())
    overrides = layout.get("identity_overrides", {})
    state_overrides = layout.get("user_state_identity_overrides", {})
    deleted_identities = set(layout.get("deleted_identities", []))
    snapshot = sqlite3.connect(f"file:{args.emby_snapshot}?mode=ro", uri=True)
    snapshot.row_factory = sqlite3.Row
    old_items = {row["id"]: dict(row) for row in snapshot.execute("SELECT * FROM items")}
    base, token = load_connection()
    new_items = {}
    series_tmdb = {}
    for library in state["new_libraries"].values():
        for item in paged(base, token, "/Items", {"ParentId": library["item_id"], "Recursive": "true", "Fields": "ProviderIds,ParentIndexNumber,IndexNumber,IndexNumberEnd,SeriesId"}):
            if item["Type"] == "Series":
                tmdb = (item.get("ProviderIds") or {}).get("Tmdb")
                if tmdb:
                    series_tmdb[item["Id"]] = tmdb
            new_items[item["Id"]] = item
    by_key = {}
    for item in new_items.values():
        keys = []
        if item["Type"] == "Movie":
            tmdb = (item.get("ProviderIds") or {}).get("Tmdb")
            if tmdb:
                keys.append(f"movie:{tmdb}")
        elif item["Type"] == "Series":
            tmdb = (item.get("ProviderIds") or {}).get("Tmdb")
            if tmdb:
                keys.append(f"series:{tmdb}")
        elif item["Type"] == "Episode":
            tmdb = series_tmdb.get(item.get("SeriesId"))
            if tmdb and item.get("ParentIndexNumber") is not None and item.get("IndexNumber") is not None:
                first = int(item["IndexNumber"])
                last = int(item.get("IndexNumberEnd") or first)
                keys.extend(f"episode:{tmdb}:{int(item['ParentIndexNumber'])}:{episode}" for episode in range(first, last + 1))
        for key in keys:
            by_key.setdefault(key, []).append(item["Id"])
    series_episodes = collections.defaultdict(list)
    for item in new_items.values():
        if item["Type"] == "Episode" and item.get("SeriesId"):
            series_episodes[item["SeriesId"]].append(item["Id"])
    missing = []
    aggregate = {}
    discarded = 0
    source_rows = 0
    merged = 0
    def merge_state(destination, played, position_ticks, favorite, play_count):
        nonlocal merged
        if destination in aggregate:
            merged += 1
        value = aggregate.setdefault(destination, {"Played": False, "PlaybackPositionTicks": 0, "IsFavorite": False, "PlayCount": 0})
        value["Played"] = value["Played"] or played
        value["PlaybackPositionTicks"] = max(value["PlaybackPositionTicks"], position_ticks)
        value["IsFavorite"] = value["IsFavorite"] or favorite
        value["PlayCount"] = max(value["PlayCount"], play_count)

    for row in snapshot.execute("SELECT * FROM user_state ORDER BY user_id,item_id"):
        source_rows += 1
        old = old_items.get(row["item_id"])
        key = stable_key(old, old_items, overrides, state_overrides) if old else None
        matches = by_key.get(key, []) if key else []
        deleted_key = "series:" + key.split(":", 2)[1] if key and key.startswith("episode:") else key
        if deleted_key in deleted_identities:
            discarded += 1
            continue
        if not matches:
            missing.append({"user_id": row["user_id"], "old_item_id": row["item_id"], "key": key})
            continue
        for new_id in matches:
            merge_state((row["user_id"], new_id), bool(row["played"]), int(row["position_ticks"]), bool(row["is_favorite"]), int(row["play_count"]))
        if key.startswith("series:") and row["played"]:
            for series_id in matches:
                for episode_id in series_episodes.get(series_id, []):
                    merge_state((row["user_id"], episode_id), True, 0, False, max(1, int(row["play_count"])))
    snapshot.close()
    if missing:
        raise RuntimeError(json.dumps({"unmapped_user_state": missing}, ensure_ascii=False))
    for (user_id, new_id), payload in aggregate.items():
        post(base, token, f"/Users/{user_id}/Items/{new_id}/UserData", payload)
    migrated = len(aggregate)
    state["user_state_migrated"] = True
    state["migrated_user_state_rows"] = migrated
    state["source_user_state_rows"] = source_rows
    state["merged_user_state_rows"] = merged
    state["discarded_deleted_user_state_rows"] = discarded
    write_state(args.state, state)
    print(json.dumps({"migrated": migrated, "merged": merged, "discarded_deleted": discarded}, ensure_ascii=False))


def command_cutover(args):
    state = json.loads(pathlib.Path(args.state).read_text())
    if not state.get("verified") or not state.get("user_state_migrated"):
        raise RuntimeError("identity verification and user-state migration must complete before cutover")
    command_verify(argparse.Namespace(state=args.state, plan=args.plan))
    state = json.loads(pathlib.Path(args.state).read_text())
    layout = json.loads(pathlib.Path(args.layout).read_text())
    base, token = load_connection()
    new_ids = {library["item_id"] for library in state["new_libraries"].values()}
    old_ids = {library["ItemId"] for library in state["old_libraries"] if library.get("CollectionType") != "boxsets"}
    current = libraries(base, token)
    current_by_id = {library["ItemId"]: library for library in current}
    for name, recorded in state["new_libraries"].items():
        live = current_by_id.get(recorded["item_id"])
        if not live or live.get("CollectionType") != recorded["collection_type"] or live.get("Locations") != [recorded["location"]] or live.get("Name") not in {recorded["temporary_name"], name}:
            raise RuntimeError(f"replacement library changed before cutover: {name}")
    unexpected = [library["Name"] for library in current if library["ItemId"] not in new_ids | old_ids and library.get("CollectionType") != "boxsets"]
    if unexpected:
        raise RuntimeError(f"unrecorded libraries block cutover: {unexpected}")
    removed = []
    for item_id in sorted(old_ids):
        library = current_by_id.get(item_id)
        if not library or item_id in new_ids:
            continue
        post(base, token, "/Library/VirtualFolders/Delete", {"Id": item_id, "RefreshLibrary": False})
        removed.append(library["Name"])
    for name in layout["library_order"]:
        library = state["new_libraries"][name]
        post(base, token, "/Library/VirtualFolders/Name", {"Id": library["item_id"], "NewName": name})
        poster = pathlib.Path(library["poster"])
        post(base, token, f"/Items/{library['item_id']}/Images/Primary", base64.b64encode(poster.read_bytes()), "image/jpeg")
    remaining = libraries(base, token)
    for collection_type, poster_name in layout.get("system_library_posters", {}).items():
        matching = [library for library in remaining if library.get("CollectionType") == collection_type]
        if len(matching) != 1:
            raise RuntimeError(f"expected one {collection_type} system library, found {len(matching)}")
        poster = pathlib.Path(state["posters_root"]) / poster_name
        if not poster.is_file():
            raise RuntimeError(f"system library poster is missing: {poster}")
        post(base, token, f"/Items/{matching[0]['ItemId']}/Images/Primary", base64.b64encode(poster.read_bytes()), "image/jpeg")
    by_item = {library["ItemId"]: library for library in remaining}
    ordered_guids = [by_item[state["new_libraries"][name]["item_id"]]["Guid"] for name in layout["library_order"]]
    remaining_guids = {library["Guid"] for library in remaining}
    users = get(base, token, "/Users")
    for user in users:
        configuration = user.get("Configuration") or {}
        extras = [guid for guid in configuration.get("OrderedViews") or [] if guid in remaining_guids and guid not in ordered_guids]
        configuration["OrderedViews"] = ordered_guids + extras
        post(base, token, f"/Users/{user['Id']}/Configuration", configuration)
    final_names = [library["Name"] for library in libraries(base, token) if library.get("CollectionType") != "boxsets"]
    if set(final_names) != set(layout["library_order"]):
        raise RuntimeError(f"final library set differs: {final_names}")
    state["cutover"] = True
    state["removed_libraries"] = removed
    state["final_libraries"] = layout["library_order"]
    write_state(args.state, state)
    print(json.dumps({"removed": removed, "final": layout["library_order"]}, ensure_ascii=False, indent=2))


def parser():
    root = argparse.ArgumentParser()
    commands = root.add_subparsers(dest="command", required=True)
    prepare = commands.add_parser("prepare")
    prepare.add_argument("--layout", required=True)
    prepare.add_argument("--posters", required=True)
    prepare.add_argument("--state", required=True)
    verify = commands.add_parser("verify")
    verify.add_argument("--plan", required=True)
    verify.add_argument("--state", required=True)
    migrate = commands.add_parser("migrate-state")
    migrate.add_argument("--layout", required=True)
    migrate.add_argument("--emby-snapshot", required=True)
    migrate.add_argument("--state", required=True)
    cutover = commands.add_parser("cutover")
    cutover.add_argument("--layout", required=True)
    cutover.add_argument("--state", required=True)
    cutover.add_argument("--plan", required=True)
    return root


def main():
    args = parser().parse_args()
    if args.command == "prepare":
        command_prepare(args)
    elif args.command == "verify":
        command_verify(args)
    elif args.command == "migrate-state":
        command_migrate_state(args)
    else:
        command_cutover(args)


if __name__ == "__main__":
    main()
