#!/usr/bin/env python3
import argparse
import json
import pathlib
import subprocess

ROLE_SERVICES = {
    "postgres": "postgres",
    "postgres_backup": "postgres-backup",
    "clouddrive2": "clouddrive2",
    "emby": "emby",
    "manager": "emby-manager-rs",
}


def roles(inventory):
    output = {}
    for role, service in ROLE_SERVICES.items():
        matches = []
        for container in inventory["containers"]:
            labels = container.get("labels") or {}
            if labels.get("com.docker.compose.service") == service or container.get("name") == service:
                matches.append(container["name"])
        if len(matches) != 1:
            raise SystemExit(f"inventory does not resolve exactly one {role}: {matches}")
        output[role] = matches[0]
    return output


def published_port(inventory, name):
    container = next(item for item in inventory["containers"] if item["name"] == name)
    ports = []
    for bindings in container.get("ports", {}).values():
        for binding in bindings or []:
            if binding.get("HostPort"):
                ports.append(int(binding["HostPort"]))
    if not ports:
        raise SystemExit(f"inventory contains no published port for {name}")
    return min(ports)


def run(*args):
    subprocess.run(args, check=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--inventory", required=True)
    parser.add_argument("--nas-host", default="nas1821.local")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--commit-file", default="/opt/embymedia/current/migration/cutover-commit.json")
    args = parser.parse_args()
    inventory = json.loads(pathlib.Path(args.inventory).read_text())
    containers = roles(inventory)
    order = [containers["postgres"], containers["clouddrive2"], containers["emby"], containers["postgres_backup"], containers["manager"]]
    manager_port = published_port(inventory, containers["manager"])
    plan = {
        "stopDebian": ["embymedia-dsh.service", "embymedia-stack.service"],
        "nasHost": args.nas_host,
        "nasStartOrder": order,
        "managerHealth": f"http://127.0.0.1:{manager_port}/health",
        "network": inventory["rollbackNetwork"],
        "verify": ["legacy /health", "legacy login", "legacy libraries", "legacy tasks"],
    }
    print(json.dumps(plan, ensure_ascii=False, indent=2))
    if args.dry_run:
        return
    if pathlib.Path(args.commit_file).exists():
        raise SystemExit("cutover commit exists; lossless NAS failback is forbidden")
    network_hook = pathlib.Path("/etc/embymedia/rollback-network.sh")
    if not network_hook.is_file():
        raise SystemExit("/etc/embymedia/rollback-network.sh is required to restore DNS and port forwarding")
    run("systemctl", "stop", "embymedia-dsh.service", "embymedia-stack.service")
    for container in order:
        run("ssh", f"root@{args.nas_host}", "/usr/local/bin/docker", "start", container)
    run(str(network_hook), json.dumps(inventory["rollbackNetwork"], separators=(",", ":")))
    run("ssh", f"root@{args.nas_host}", "curl", "-fsS", f"http://127.0.0.1:{manager_port}/health")


if __name__ == "__main__":
    main()
