#!/usr/bin/env python3
import argparse
import hashlib
import json
import os
import re
import stat
import subprocess
import tempfile
from datetime import datetime, timezone
from pathlib import Path

PATH_MAP = {
    "/volume1/docker/emby/config": "/srv/embymedia/data/emby/config",
    "/volume1/docker/clouddrive2/config": "/srv/embymedia/data/clouddrive/config",
    "/volume1/docker/clouddrive2/CloudNAS": "/srv/embymedia/data/clouddrive/CloudNAS",
    "/volume1/strm": "/srv/embymedia/data/strm",
}
BASELINES = {
    "/volume1/docker/emby/config": 1.9 * 1024**3,
    "/volume1/docker/clouddrive2/config": 97 * 1024**2,
    "/volume1/strm": 409 * 1024**2,
    "/volume1/docker/emby-manager-rs": 2.6 * 1024**3,
}
WEBHOOK = Path("/volume1/docker/clouddrive2/config/webhooks/webhook.toml")
SENSITIVE = re.compile(r"password|cookie|secret|api[_-]?key|token", re.I)
VOLUME1 = re.compile(r"/volume1(?:/[A-Za-z0-9._()\[\] @+,=-]+)+")
DOCKER = "/usr/local/bin/docker"


def command(*args):
    return subprocess.run(args, check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout


def sha256(value):
    return hashlib.sha256(value.encode()).hexdigest()


def directory_size(path):
    try:
        return int(command("du", "-sb", path).split()[0])
    except Exception:
        return None


def file_facts(path):
    target = Path(path)
    try:
        info = target.stat()
    except FileNotFoundError:
        return {"exists": False}
    return {
        "exists": True,
        "uid": info.st_uid,
        "gid": info.st_gid,
        "mode": oct(stat.S_IMODE(info.st_mode)),
        "bytes": directory_size(path) if target.is_dir() else info.st_size,
    }


def sanitize_env(values):
    output = []
    for item in values or []:
        key, separator, value = item.partition("=")
        output.append({
            "key": key,
            "present": bool(separator and value),
            **({"sha256": sha256(value)} if separator and value else {}),
            "sensitive": bool(SENSITIVE.search(key)),
        })
    return sorted(output, key=lambda row: row["key"])


def sanitized_container(raw):
    config = raw.get("Config") or {}
    host = raw.get("HostConfig") or {}
    network = raw.get("NetworkSettings") or {}
    state = raw.get("State") or {}
    return {
        "id": raw.get("Id"),
        "name": str(raw.get("Name", "")).lstrip("/"),
        "imageReference": config.get("Image"),
        "imageId": raw.get("Image"),
        "labels": {key: value for key, value in (config.get("Labels") or {}).items() if key.startswith("com.docker.compose.")},
        "environment": sanitize_env(config.get("Env")),
        "ports": network.get("Ports") or {},
        "mounts": [{key: mount.get(key) for key in ["Type", "Source", "Destination", "Mode", "RW", "Propagation"]} for mount in raw.get("Mounts") or []],
        "devices": host.get("Devices") or [],
        "capAdd": host.get("CapAdd") or [],
        "privileged": bool(host.get("Privileged")),
        "pidMode": host.get("PidMode") or "",
        "ipcMode": host.get("IpcMode") or "",
        "securityOpt": host.get("SecurityOpt") or [],
        "restartPolicy": host.get("RestartPolicy") or {},
        "networks": sorted((network.get("Networks") or {}).keys()),
        "running": bool(state.get("Running")),
        "health": (state.get("Health") or {}).get("Status"),
    }


def docker_inventory():
    ids = [line for line in command(DOCKER, "ps", "-aq").splitlines() if line]
    if not ids:
        return []
    inspected = json.loads(command(DOCKER, "inspect", *ids))
    relevant = []
    for raw in inspected:
        name = str(raw.get("Name", "")).lower()
        image = str((raw.get("Config") or {}).get("Image", "")).lower()
        if any(term in f"{name} {image}" for term in ["emby", "clouddrive", "postgres"]):
            container = sanitized_container(raw)
            image_info = json.loads(command(DOCKER, "image", "inspect", raw.get("Image")))[0]
            container["repoTags"] = image_info.get("RepoTags") or []
            container["repoDigests"] = image_info.get("RepoDigests") or []
            container["imageCreated"] = image_info.get("Created")
            relevant.append(container)
    return relevant


def webhook_inventory():
    text = WEBHOOK.read_text()
    fields = {}
    for name in ["enabled", "url", "method"]:
        match = re.search(rf"(?m)^\s*{name}\s*=\s*(.+?)\s*$", text)
        fields[name] = match.group(1) if match else None
    url = str(fields.get("url") or "")
    key = re.search(r"[?&]key=([^&\"']+)", url)
    return {
        "path": str(WEBHOOK),
        "fieldsPresent": {name: value is not None for name, value in fields.items()},
        "method": str(fields.get("method") or "").strip("\"'"),
        "enabled": str(fields.get("enabled") or "").lower() == "true",
        "urlPath": re.sub(r"\?.*$", "", url.strip("\"'")),
        "queryKeyPresent": key is not None,
        **({"queryKeySha256": sha256(key.group(1))} if key else {}),
    }


def scan_volume1_references(roots):
    findings = []
    suffixes = {".json", ".yml", ".yaml", ".toml", ".xml", ".conf", ".ini", ".strm", ".txt", ".env"}
    for root in roots:
        for directory, names, files in os.walk(root):
            names[:] = [name for name in names if name not in {"cache", "logs", "transcoding-temp", "metadata"}]
            for filename in files:
                if filename.startswith("container-inspect"):
                    continue
                path = Path(directory, filename)
                if path.suffix.lower() not in suffixes:
                    continue
                try:
                    if path.stat().st_size > 16 * 1024**2:
                        continue
                    text = path.read_text(errors="ignore")
                except OSError:
                    continue
                references = sorted(set(VOLUME1.findall(text)))
                if references:
                    findings.append({"file": str(path), "references": references})
    return findings


def explained(reference):
    return any(reference == source or reference.startswith(source + "/") for source in PATH_MAP)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True)
    parser.add_argument("--public-ip", required=True)
    parser.add_argument("--router-target", required=True)
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise SystemExit("NAS inventory must run as root for read-only Docker inspection")
    sizes = {path: file_facts(path) for path in BASELINES}
    warnings = []
    for path, baseline in BASELINES.items():
        actual = sizes[path].get("bytes")
        if actual is not None and abs(actual - baseline) / baseline > 0.10:
            warnings.append({"path": path, "baselineBytes": int(baseline), "actualBytes": actual})
    references = scan_volume1_references([
        "/volume1/docker/emby/config",
        "/volume1/docker/clouddrive2/config",
        "/volume1/strm",
        "/volume1/docker/emby-manager-rs",
    ])
    unexplained = sorted({reference for finding in references for reference in finding["references"] if not explained(reference)})
    filesystem = os.statvfs("/volume1")
    report = {
        "schemaVersion": 1,
        "generatedAt": datetime.now(timezone.utc).isoformat(),
        "hostname": os.uname().nodename,
        "rollbackNetwork": {
            "publicIp": args.public_ip,
            "routerTarget": args.router_target,
            "hostnames": ["auth.gaotao.cc", "dsh.gaotao.cc", "emby.gaotao.cc"],
            "ports": [80, 443],
        },
        "pathMap": PATH_MAP,
        "containers": docker_inventory(),
        "paths": sizes,
        "volume1": {
            "totalBytes": filesystem.f_blocks * filesystem.f_frsize,
            "availableBytes": filesystem.f_bavail * filesystem.f_frsize,
        },
        "webhook": webhook_inventory(),
        "volume1References": references,
        "unexplainedAbsolutePaths": unexplained,
        "baselineDifferences": warnings,
        "failClosed": bool(unexplained),
    }
    output = Path(args.output).resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = Path(tempfile.mkstemp(prefix=output.name + ".", dir=output.parent)[1])
    try:
        temporary.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        os.chmod(temporary, 0o600)
        os.replace(temporary, output)
    finally:
        temporary.unlink(missing_ok=True)
    print(json.dumps({
        "output": str(output),
        "containers": len(report["containers"]),
        "unexplainedAbsolutePaths": len(unexplained),
        "baselineDifferences": len(warnings),
        "failClosed": report["failClosed"],
    }))


if __name__ == "__main__":
    main()
