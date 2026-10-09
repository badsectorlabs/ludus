#!/usr/bin/env python3
"""
Ludus User Provisioning Health Check
=====================================
Run as root on the Ludus host:

    python3 /opt/ludus/scripts/check-user-provisioning.py

Reads every non-ROOT user from the Ludus database and checks that each
one has been fully provisioned:

    - Proxmox PAM user  (<proxmoxUsername>@pam)
    - Proxmox API token (<proxmoxUsername>@pam!ludus-token)
    - OS user on the host
    - WireGuard key files (/etc/wireguard/<USERID>-client-{private,public}-key)
    - WireGuard peer block in wg0.conf
    - User directory (/opt/ludus/users/<proxmoxUsername>)

Exits 0 if all users are healthy, 1 if any gaps are detected.
Broken users should be fixed with reprovision-user.py.
"""

import subprocess
import sqlite3
import sys
import os
import pathlib
import re
import yaml

# ── Config ────────────────────────────────────────────────────────────────────

DEFAULT_CONFIG   = "/opt/ludus/config.yml"
DEFAULT_WG_CONF  = "/etc/wireguard/wg0.conf"
DEFAULT_WG_DIR   = "/etc/wireguard"
DEFAULT_USERS_DIR = "/opt/ludus/users"

# ANSI colours (disabled when stdout is not a tty)
_USE_COLOUR = sys.stdout.isatty()
RED    = "\033[0;31m" if _USE_COLOUR else ""
GREEN  = "\033[0;32m" if _USE_COLOUR else ""
YELLOW = "\033[1;33m" if _USE_COLOUR else ""
RESET  = "\033[0m"    if _USE_COLOUR else ""

# ── Helpers ───────────────────────────────────────────────────────────────────

def load_config(path=DEFAULT_CONFIG):
    with open(path) as fh:
        return yaml.safe_load(fh) or {}


def db_path(cfg):
    return os.path.join(cfg.get("data_directory", "/opt/ludus/db"), "data.db")


def get_users(db):
    conn = sqlite3.connect(db)
    rows = conn.execute(
        "SELECT userID, proxmoxUsername, userNumber FROM users "
        "WHERE userID != 'ROOT' ORDER BY userNumber"
    ).fetchall()
    conn.close()
    return rows  # [(userID, proxmoxUsername, userNumber), ...]


def pveum_user_exists(proxmox_username):
    try:
        out = subprocess.check_output(
            ["pveum", "user", "list", "--output-format", "json"],
            stderr=subprocess.DEVNULL,
            text=True,
        )
        import json
        users = json.loads(out).get("data", json.loads(out)) if out.strip().startswith("{") else json.loads(out)
        # pveum user list returns a list or {"data": [...]}
        if isinstance(users, dict):
            users = users.get("data", [])
        return any(u.get("userid", "") == f"{proxmox_username}@pam" for u in users)
    except Exception:
        # Fallback: plain text grep
        try:
            result = subprocess.run(
                ["pveum", "user", "list"],
                capture_output=True, text=True
            )
            return f"{proxmox_username}@pam" in result.stdout
        except Exception:
            return False


def pveum_token_exists(proxmox_username):
    try:
        result = subprocess.run(
            ["pveum", "user", "token", "list", f"{proxmox_username}@pam"],
            capture_output=True, text=True
        )
        return "ludus-token" in result.stdout
    except Exception:
        return False


def os_user_exists(username):
    try:
        result = subprocess.run(["id", username], capture_output=True)
        return result.returncode == 0
    except Exception:
        return False


def wg_keys_exist(user_id, wg_dir=DEFAULT_WG_DIR):
    priv = pathlib.Path(wg_dir) / f"{user_id}-client-private-key"
    pub  = pathlib.Path(wg_dir) / f"{user_id}-client-public-key"
    return priv.exists() and pub.exists()


def wg_peer_exists(user_id, wg_conf=DEFAULT_WG_CONF):
    try:
        content = pathlib.Path(wg_conf).read_text()
        marker = f"# BEGIN ANSIBLE MANAGED BLOCK FOR {user_id}"
        return marker in content
    except Exception:
        return False


def user_dir_exists(proxmox_username, users_dir=DEFAULT_USERS_DIR):
    return pathlib.Path(users_dir, proxmox_username).is_dir()


# ── Main ──────────────────────────────────────────────────────────────────────

def check_mark(ok):
    if ok:
        return f"{GREEN}✓{RESET}"
    return f"{RED}✗{RESET}"


def main():
    if os.geteuid() != 0:
        print(f"{RED}Error: this script must be run as root.{RESET}", file=sys.stderr)
        sys.exit(1)

    cfg = load_config()
    db  = db_path(cfg)

    if not os.path.exists(db):
        print(f"{RED}Error: Ludus database not found at {db}{RESET}", file=sys.stderr)
        sys.exit(1)

    users = get_users(db)
    if not users:
        print("No non-ROOT users found in database.")
        sys.exit(0)

    # Header
    header = f"{'USER':<8} {'USERNAME':<22} {'PAM':<5} {'TOKEN':<7} {'OS':<5} {'WG_KEYS':<9} {'WG_PEER':<9} {'DIR':<5}"
    print()
    print("Ludus User Provisioning Health Check")
    print("=" * len(header))
    print(header)
    print("-" * len(header))

    broken = []
    for user_id, proxmox_username, user_number in users:
        pam   = pveum_user_exists(proxmox_username)
        token = pveum_token_exists(proxmox_username)
        os_u  = os_user_exists(proxmox_username)
        keys  = wg_keys_exist(user_id)
        peer  = wg_peer_exists(user_id)
        udir  = user_dir_exists(proxmox_username)

        all_ok = all([pam, token, os_u, keys, peer, udir])
        if not all_ok:
            broken.append(user_id)

        suffix = "" if all_ok else f"  {YELLOW}← BROKEN{RESET}"
        print(
            f"{user_id:<8} {proxmox_username:<22} "
            f"{check_mark(pam):<14} {check_mark(token):<16} {check_mark(os_u):<14} "
            f"{check_mark(keys):<18} {check_mark(peer):<18} {check_mark(udir):<14}"
            f"{suffix}"
        )

    print("-" * len(header))
    healthy = len(users) - len(broken)
    if broken:
        print(
            f"\n{RED}SUMMARY: {healthy} healthy, {len(broken)} broken "
            f"({', '.join(broken)}){RESET}"
        )
        print(
            f"\nRun {YELLOW}python3 reprovision-user.py --userid <ID>{RESET} "
            f"to fix broken users."
        )
        sys.exit(1)
    else:
        print(f"\n{GREEN}SUMMARY: all {healthy} user(s) fully provisioned.{RESET}")
        sys.exit(0)


if __name__ == "__main__":
    main()
