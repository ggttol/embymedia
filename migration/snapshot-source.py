#!/usr/bin/env python3
import argparse
import hashlib
import json
import os
import subprocess
import tarfile
from datetime import datetime, timezone
import time
from pathlib import Path

DOCKER = "/usr/local/bin/docker"
ROLES = {
    "emby": "emby",
    "clouddrive2": "clouddrive2",
    "postgres": "postgres",
    "postgres_backup": "postgres-backup",
    "manager": "emby-manager-rs",
}
ARTIFACTS = {
    "emby-config.tar": "/volume1/docker/emby/config",
    "clouddrive-config.tar": "/volume1/docker/clouddrive2/config",
    "strm.tar": "/volume1/strm",
}


def run(*args, capture=False):
    completed = subprocess.run(args, check=True, stdout=subprocess.PIPE if capture else None)
    return completed.stdout if capture else None


def digest(path):
    value = hashlib.sha256()
    with open(path, "rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def archive_filter(info):
    if Path(info.name).name.startswith("container-inspect"):
        return None
    return info


def role_containers(inventory):
    result = {}
    for role, service in ROLES.items():
        matches = []
        for container in inventory.get("containers", []):
            labels = container.get("labels") or {}
            if labels.get("com.docker.compose.service") == service or container.get("name") == service:
                matches.append(container["name"])
        if len(matches) != 1:
            raise RuntimeError(f"inventory must resolve exactly one {role} container, got {matches}")
        result[role] = matches[0]
    return result


def build_manifest(output, inventory_path, containers, image_ids, phase):
    artifacts = {}
    for path in sorted(output.iterdir()):
        if path.is_file() and path.name != "manifest.json":
            artifacts[path.name] = {"bytes": path.stat().st_size, "sha256": digest(path)}
    return {
        "schemaVersion": 1,
        "phase": phase,
        "createdAt": datetime.now(timezone.utc).isoformat(),
        "inventorySha256": digest(inventory_path),
        "artifacts": artifacts,
        "sourceContainers": containers,
        "clouddriveImageId": image_ids["clouddrive2"],
        "imageIds": image_ids,
        "excludedSourceFiles": ["clouddrive config historical container-inspect* artifacts"],
        "restartOrder": [containers["postgres"], containers["clouddrive2"], containers["emby"], containers["postgres_backup"], containers["manager"]],
    }


def write_manifest(output, manifest):
    (output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    os.chmod(output / "manifest.json", 0o600)


def snapshot(inventory, containers, output, phase, inventory_path, authelia_image_id):
    stopped = []
    try:
        run(DOCKER, "stop", containers["manager"], containers["postgres_backup"])
        stopped.extend([containers["manager"], containers["postgres_backup"]])
        run(DOCKER, "stop", containers["emby"], containers["clouddrive2"])
        stopped.extend([containers["clouddrive2"], containers["emby"]])
        for artifact, source in ARTIFACTS.items():
            path = output / artifact
            with tarfile.open(path, "w") as archive:
                archive.add(source, arcname=".", recursive=True, filter=archive_filter)
        database = output / "legacy-database.dump"
        with database.open("wb") as target:
            subprocess.run([
                DOCKER, "exec", containers["postgres"], "sh", "-c",
                'exec pg_dump -Fc -U "$POSTGRES_USER" "$POSTGRES_DB"',
            ], check=True, stdout=target)
        image_ids = {}
        for role, artifact in {
            "clouddrive2": "clouddrive-image.tar",
            "emby": "emby-image.tar",
            "postgres": "postgres-image.tar",
        }.items():
            container = next(item for item in inventory["containers"] if item["name"] == containers[role])
            image_ids[role] = container["imageId"]
            run(DOCKER, "save", "-o", str(output / artifact), container["imageId"])
        image_ids["authelia"] = authelia_image_id
        run(DOCKER, "save", "-o", str(output / "authelia-image.tar"), authelia_image_id)
        write_manifest(output, build_manifest(output, inventory_path, containers, image_ids, phase))
    finally:
        if phase == "pre" and stopped:
            failures = []
            try:
                run(DOCKER, "start", containers["clouddrive2"])
                deadline = time.monotonic() + 180
                while not os.path.ismount("/volume1/docker/clouddrive2/CloudNAS/CloudDrive"):
                    if time.monotonic() >= deadline:
                        raise RuntimeError("CloudDrive mount did not recover within 180 seconds")
                    time.sleep(2)
            except Exception as error:
                failures.append(str(error))
            for role in ["emby", "postgres_backup", "manager"]:
                try:
                    run(DOCKER, "start", containers[role])
                except Exception as error:
                    failures.append(f"{role}: {error}")
            if failures:
                raise RuntimeError("source recovery failed: " + "; ".join(failures))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--inventory", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--phase", choices=["pre", "final"], required=True)
    parser.add_argument("--authelia-image-id", default="sha256:4a87c7d1276f351a9d2b2139a676bb9aba16692825c858523c9c002744d0aa59")
    parser.add_argument("--refresh", action="store_true")
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise SystemExit("source snapshot must run as root")
    inventory = json.loads(Path(args.inventory).read_text())
    if inventory.get("failClosed"):
        raise SystemExit("source inventory has unexplained absolute paths")
    containers = role_containers(inventory)
    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    inventory_path = str(Path(args.inventory).resolve())
    if args.refresh:
        image_ids = {}
        for role in ["clouddrive2", "emby", "postgres"]:
            container = next(item for item in inventory["containers"] if item["name"] == containers[role])
            image_ids[role] = container["imageId"]
        image_ids["authelia"] = args.authelia_image_id
        manifest = build_manifest(output, inventory_path, containers, image_ids, args.phase)
        write_manifest(output, manifest)
        print(json.dumps({"refresh": True, "artifacts": len(manifest["artifacts"])}))
        return
    snapshot(inventory, containers, output, args.phase, inventory_path, args.authelia_image_id)
    print(json.dumps({"phase": args.phase, "output": str(output)}))


if __name__ == "__main__":
    main()
