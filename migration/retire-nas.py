#!/usr/bin/env python3
import argparse
import json
import os
import pathlib
import subprocess
from datetime import datetime, timezone, timedelta

TERMS = ("emby", "clouddrive", "postgres", "emby-manager", "embymedia-manager")
DOCKER = "/usr/local/bin/docker"


def run(*args):
    subprocess.run(args, check=True)


def containers(inventory):
    result = []
    for container in inventory["containers"]:
        label = f"{container.get('name', '')} {container.get('imageReference', '')}".lower()
        if any(term in label for term in TERMS):
            result.append(container["name"])
    if not result:
        raise SystemExit("inventory contains no legacy containers")
    return sorted(set(result))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--inventory", required=True)
    parser.add_argument("--phase", choices=["stop", "delete"], required=True)
    parser.add_argument("--commit")
    parser.add_argument("--archive")
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise SystemExit("NAS retirement must run as root")
    inventory = json.loads(pathlib.Path(args.inventory).read_text())
    names = containers(inventory)
    if args.phase == "delete":
        if not args.commit or not args.archive:
            raise SystemExit("delete requires cutover commit and encrypted migration archive")
        commit = json.loads(pathlib.Path(args.commit).read_text())
        committed = datetime.fromisoformat(commit["committedAt"])
        if datetime.now(timezone.utc) < committed + timedelta(days=14):
            raise SystemExit("14-day observation period has not elapsed")
        archive = pathlib.Path(args.archive)
        if not archive.is_file() or archive.stat().st_size == 0:
            raise SystemExit("encrypted migration archive is unavailable")
        for name in names:
            run(DOCKER, "rm", name)
    else:
        for name in names:
            run(DOCKER, "update", "--restart=no", name)
            run(DOCKER, "stop", name)
        running = subprocess.run(
            [DOCKER, "ps", "--format", "{{.Names}}"],
            check=True,
            text=True,
            stdout=subprocess.PIPE,
        ).stdout.splitlines()
        remaining = sorted(set(names).intersection(running))
        if remaining:
            raise SystemExit("legacy containers remain running: " + ", ".join(remaining))
    print(json.dumps({"phase": args.phase, "containers": names, "volumesDeleted": False}))


if __name__ == "__main__":
    main()
