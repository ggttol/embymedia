#!/usr/bin/env python3
import argparse
import json
import os
import pathlib
import subprocess
import tempfile
from datetime import datetime, timezone

REQUIRED_GATES = [
    "httpTunnelAccess",
    "dshTokenAuth",
    "embyHttp",
    "websocket",
    "webhookBridge",
    "webhookRealEvent",
    "databaseParity",
    "playbackMovie",
    "playbackEpisode",
    "seek",
    "softwareTranscode",
    "planTaskUndo",
    "backupRestore",
    "legacyPortsClosed",
    "nasServicesStopped",
    "noNasDependency",
    "rebootOrdering",
]


def require_snapshot(snapshot):
    environment = dict(os.environ)
    environment["RESTIC_PASSWORD_FILE"] = "/etc/embymedia/secrets/restic-local-password"
    result = subprocess.run(
        ["restic", "--repo", "/srv/embymedia/backups/restic-local", "snapshots", "--json"],
        check=True,
        text=True,
        stdout=subprocess.PIPE,
        env=environment,
    )
    snapshots = json.loads(result.stdout)
    if not any(item.get("id", "").startswith(snapshot) or item.get("short_id") == snapshot for item in snapshots):
        raise SystemExit("gate report restic snapshot is not present")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--report", required=True)
    parser.add_argument("--output", default="/opt/embymedia/current/migration/cutover-commit.json")
    args = parser.parse_args()
    report_path = pathlib.Path(args.report)
    if report_path.stat().st_mode & 0o077:
        raise SystemExit("gate report must not be readable by group or other")
    report = json.loads(report_path.read_text())
    failures = [name for name in REQUIRED_GATES if report.get("gates", {}).get(name) is not True]
    if failures:
        raise SystemExit("cutover gates incomplete: " + ", ".join(failures))
    snapshot = report.get("resticSnapshot")
    if not isinstance(snapshot, str) or not snapshot:
        raise SystemExit("gate report has no restic snapshot")
    require_snapshot(snapshot)
    commit = {
        "schemaVersion": 1,
        "committedAt": datetime.now(timezone.utc).isoformat(),
        "resticSnapshot": snapshot,
        "gates": report["gates"],
        "rollbackPolicy": "debian-restic-or-fix-forward",
        "nasFailbackLossless": False,
    }
    output = pathlib.Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile("w", dir=output.parent, delete=False) as target:
        json.dump(commit, target, indent=2)
        target.write("\n")
        temporary = pathlib.Path(target.name)
    temporary.chmod(0o600)
    temporary.replace(output)
    dsh_env = pathlib.Path("/etc/embymedia/dsh.env")
    lines = [line for line in dsh_env.read_text().splitlines() if not line.startswith("EMBYMEDIA_WRITE_MODE=")]
    lines.append("EMBYMEDIA_WRITE_MODE=enabled")
    dsh_env.write_text("\n".join(lines) + "\n")
    os.chmod(dsh_env, 0o640)
    subprocess.run(["systemctl", "restart", "embymedia-dsh.service"], check=True)
    print(json.dumps({"commit": str(output), "writeMode": "enabled", "resticSnapshot": snapshot}))


if __name__ == "__main__":
    main()
