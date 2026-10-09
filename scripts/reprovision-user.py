#!/usr/bin/env python3
"""
Ludus User Reprovision Script
==============================
Run as root on the Ludus host:

    python3 /opt/ludus/scripts/reprovision-user.py --userid BF
    python3 /opt/ludus/scripts/reprovision-user.py --all

Fixes a partially-provisioned user by running only the missing steps:

  1. If the Proxmox PAM user is missing  → run add-user.yml (idempotent)
  2. If the Proxmox API token is missing → create it via pveum, update DB

Does NOT re-run steps that are already complete.
Does NOT modify existing users that are fully healthy.

Use check-user-provisioning.py first to identify which users need fixing.
"""

import argparse
import base64
import json
import os
import pathlib
import re
import sqlite3
import subprocess
import sys
import tempfile

import yaml

# ── Config ────────────────────────────────────────────────────────────────────

DEFAULT_CONFIG    = "/opt/ludus/config.yml"
DEFAULT_WG_DIR    = "/etc/wireguard"
DEFAULT_USERS_DIR = "/opt/ludus/users"
INSTALL_PATH      = "/opt/ludus"

_USE_COLOUR = sys.stdout.isatty()
RED    = "\033[0;31m" if _USE_COLOUR else ""
GREEN  = "\033[0;32m" if _USE_COLOUR else ""
YELLOW = "\033[1;33m" if _USE_COLOUR else ""
RESET  = "\033[0m"    if _USE_COLOUR else ""


# ── Crypto helpers ────────────────────────────────────────────────────────────

def _aesgcm():
    try:
        from cryptography.hazmat.primitives.ciphers.aead import AESGCM
        return AESGCM
    except ImportError:
        print(f"{RED}Error: 'cryptography' package required. "
              f"Install with: pip3 install cryptography{RESET}", file=sys.stderr)
        sys.exit(1)


def decrypt_db_field(encrypted: str, key: bytes) -> str:
    """Decrypt a Ludus DB field. Passwords have 'enc:' prefix; token secrets don't."""
    AESGCM = _aesgcm()
    if encrypted.startswith("enc:"):
        encrypted = encrypted[4:]
    ct = base64.b64decode(encrypted)
    return AESGCM(key[:32]).decrypt(ct[:12], ct[12:], None).decode()


def encrypt_token_secret(secret: str, key: bytes) -> str:
    """Encrypt a token secret for storage in the Ludus DB (no 'enc:' prefix)."""
    AESGCM = _aesgcm()
    nonce = os.urandom(12)
    ct = AESGCM(key[:32]).encrypt(nonce, secret.encode(), None)
    return base64.b64encode(nonce + ct).decode()


# ── Config / DB helpers ───────────────────────────────────────────────────────

def load_config(path=DEFAULT_CONFIG):
    with open(path) as fh:
        return yaml.safe_load(fh) or {}


def db_path(cfg):
    return os.path.join(cfg.get("data_directory", "/opt/ludus/db"), "data.db")


def get_user(db, user_id):
    conn = sqlite3.connect(db)
    row = conn.execute(
        "SELECT userID, proxmoxUsername, userNumber, isAdmin, defaultRangeID, "
        "proxmoxPassword, proxmoxTokenId, proxmoxTokenSecret "
        "FROM users WHERE userID = ?",
        (user_id,)
    ).fetchone()
    conn.close()
    if not row:
        return None
    return {
        "userID":            row[0],
        "proxmoxUsername":   row[1],
        "userNumber":        row[2],
        "isAdmin":           bool(row[3]),
        "defaultRangeId":    row[4] or "",
        "proxmoxPassword":   row[5],
        "proxmoxTokenId":    row[6] or "",
        "proxmoxTokenSecret": row[7] or "",
    }


def get_all_user_ids(db):
    conn = sqlite3.connect(db)
    rows = conn.execute(
        "SELECT userID FROM users WHERE userID != 'ROOT' ORDER BY userNumber"
    ).fetchall()
    conn.close()
    return [r[0] for r in rows]


def update_token_secret(db, user_id, encrypted_secret):
    conn = sqlite3.connect(db)
    conn.execute(
        "UPDATE users SET proxmoxTokenSecret = ? WHERE userID = ?",
        (encrypted_secret, user_id)
    )
    conn.commit()
    conn.close()


def get_root_token(db, key):
    """Return (tokenId, tokenSecret) for the ROOT user."""
    conn = sqlite3.connect(db)
    row = conn.execute(
        "SELECT proxmoxTokenId, proxmoxTokenSecret FROM users WHERE userID = 'ROOT'"
    ).fetchone()
    conn.close()
    if not row or not row[0]:
        return None, None
    token_id = row[0]
    token_secret = decrypt_db_field(row[1], key)
    return token_id, token_secret


# ── Proxmox checks ────────────────────────────────────────────────────────────

def pam_user_exists(proxmox_username):
    result = subprocess.run(["pveum", "user", "list"], capture_output=True, text=True)
    return f"{proxmox_username}@pam" in result.stdout


def token_exists(proxmox_username):
    result = subprocess.run(
        ["pveum", "user", "token", "list", f"{proxmox_username}@pam"],
        capture_output=True, text=True
    )
    return "ludus-token" in result.stdout


# ── Provisioning steps ────────────────────────────────────────────────────────

def run_add_user_playbook(user, cfg):
    """
    Run add-user.yml for the given user. Idempotent — skips steps that are
    already done (WireGuard keys, PAM user via failed_when guard, etc.).
    """
    playbook = f"{INSTALL_PATH}/ansible/user-management/add-user.yml"
    server_config = f"{INSTALL_PATH}/ansible/server-config.yml"
    ludus_config  = f"{INSTALL_PATH}/config.yml"

    has_default_range = bool(user["defaultRangeId"])

    extra_vars = {
        "username":               user["proxmoxUsername"],
        "user_id":                user["userID"],
        "user_number":            user["userNumber"],
        "proxmox_public_ip":      cfg.get("proxmox_public_ip", "127.0.0.1"),
        "user_is_admin":          user["isAdmin"],
        "proxmox_password":       user["_plaintextPassword"],
        "user_has_default_range": has_default_range,
        "ludus_install_path":     INSTALL_PATH,
        "ludus_users_dir":        f"{INSTALL_PATH}/users",
        "ludus_ranges_dir":       f"{INSTALL_PATH}/ranges",
        "ludus_cluster_mode":     cfg.get("cluster_mode", False),
    }

    ansible_home = f"{INSTALL_PATH}/users/root/.ansible"

    env = os.environ.copy()
    env["ANSIBLE_HOME"]    = ansible_home
    env["ANSIBLE_NOCOLOR"] = "true"
    env["ANSIBLE_FORCE_COLOR"] = "false"

    cmd = [
        "ansible-playbook",
        "--extra-vars", json.dumps(extra_vars),
        f"--extra-vars=@{ludus_config}",
        f"--extra-vars=@{server_config}",
        "--inventory", "127.0.0.1,",
        "--connection", "local",
        playbook,
    ]

    result = subprocess.run(cmd, capture_output=True, text=True, env=env)
    return result.returncode == 0, result.stdout + result.stderr


def create_proxmox_token(proxmox_username):
    """
    Create ludus-token for <proxmox_username>@pam.
    Returns the token secret UUID on success, None on failure.
    """
    result = subprocess.run(
        [
            "pveum", "user", "token", "add",
            f"{proxmox_username}@pam", "ludus-token",
            "--privsep", "0",
            "--comment", "Ludus Token - Do not modify or delete",
            "--output-format", "json",
        ],
        capture_output=True, text=True
    )
    if result.returncode != 0:
        # Token may already exist — delete and recreate
        if "already exists" in result.stderr or "already exists" in result.stdout:
            subprocess.run(
                ["pveum", "user", "token", "del", f"{proxmox_username}@pam", "ludus-token"],
                capture_output=True
            )
            result = subprocess.run(
                [
                    "pveum", "user", "token", "add",
                    f"{proxmox_username}@pam", "ludus-token",
                    "--privsep", "0",
                    "--comment", "Ludus Token - Do not modify or delete",
                    "--output-format", "json",
                ],
                capture_output=True, text=True
            )
        if result.returncode != 0:
            return None

    try:
        data = json.loads(result.stdout)
        # pveum returns {"data": {"full-tokenid": ..., "value": ...}} or flat
        if "data" in data:
            data = data["data"]
        return data.get("value")
    except Exception:
        # Try parsing key=value output as fallback
        match = re.search(r'"value"\s*:\s*"([0-9a-f-]{36})"', result.stdout)
        if match:
            return match.group(1)
        return None


# ── Main reprovision logic ────────────────────────────────────────────────────

def reprovision_user(user_id, cfg, enc_key):
    db = db_path(cfg)
    user = get_user(db, user_id)

    if not user:
        print(f"{RED}  Error: user '{user_id}' not found in database.{RESET}")
        return False

    proxmox_username = user["proxmoxUsername"]
    print(f"\nReprovisioning {user_id} ({proxmox_username})...")

    # Decrypt the stored Proxmox password — needed for the playbook
    try:
        plaintext_password = decrypt_db_field(user["proxmoxPassword"], enc_key)
    except Exception as e:
        print(f"{RED}  Error: could not decrypt Proxmox password: {e}{RESET}")
        return False

    user["_plaintextPassword"] = plaintext_password

    # ── Step 1: PAM user ──────────────────────────────────────────────────────
    pam_exists = pam_user_exists(proxmox_username)
    if not pam_exists:
        print(f"  PAM user {proxmox_username}@pam: {YELLOW}MISSING{RESET} → running add-user.yml...")
        ok, output = run_add_user_playbook(user, cfg)
        # Even if the playbook exits non-zero (e.g. wg syncconf fails when wg0
        # is not running), the pveum user add task may have succeeded. Check
        # whether the PAM user now exists before giving up.
        if not pam_user_exists(proxmox_username):
            print(f"{RED}  add-user.yml {'completed but' if ok else 'FAILED and'} PAM user still missing.{RESET}")
            print(f"  Playbook output:\n{output}")
            return False
        if not ok:
            print(f"  {YELLOW}add-user.yml had non-fatal errors (e.g. wg0 not running) — continuing.{RESET}")
        print(f"  PAM user: {GREEN}created ✓{RESET}")
    else:
        print(f"  PAM user {proxmox_username}@pam: {GREEN}already exists ✓{RESET}")

    # ── Step 2: API token ─────────────────────────────────────────────────────
    tok_exists = token_exists(proxmox_username)
    if not tok_exists:
        print(f"  API token ludus-token: {YELLOW}MISSING{RESET} → creating...")
        secret = create_proxmox_token(proxmox_username)
        if not secret:
            print(f"{RED}  Failed to create Proxmox API token.{RESET}")
            return False

        # Encrypt and store in DB — token secrets have NO 'enc:' prefix
        encrypted = encrypt_token_secret(secret, enc_key)
        token_id  = f"{proxmox_username}@pam!ludus-token"
        update_token_secret(db, user_id, encrypted)
        print(f"  API token: {GREEN}created ✓{RESET}")
        print(f"  DB token secret: {GREEN}updated ✓{RESET}")
    else:
        print(f"  API token ludus-token: {GREEN}already exists ✓{RESET}")
        # Verify the stored DB secret can decrypt to a 36-char UUID
        if user["proxmoxTokenSecret"]:
            try:
                secret = decrypt_db_field(user["proxmoxTokenSecret"], enc_key)
                if len(secret) != 36:
                    raise ValueError(f"unexpected length {len(secret)}")
                print(f"  DB token secret: {GREEN}valid ✓{RESET}")
            except Exception as e:
                print(f"  DB token secret: {YELLOW}invalid ({e}) → recreating token...{RESET}")
                new_secret = create_proxmox_token(proxmox_username)
                if not new_secret:
                    print(f"{RED}  Failed to recreate Proxmox API token.{RESET}")
                    return False
                encrypted = encrypt_token_secret(new_secret, enc_key)
                update_token_secret(db, user_id, encrypted)
                print(f"  DB token secret: {GREEN}updated ✓{RESET}")
        else:
            print(f"  DB token secret: {YELLOW}missing → recreating token...{RESET}")
            new_secret = create_proxmox_token(proxmox_username)
            if not new_secret:
                print(f"{RED}  Failed to recreate Proxmox API token.{RESET}")
                return False
            encrypted = encrypt_token_secret(new_secret, enc_key)
            update_token_secret(db, user_id, encrypted)
            print(f"  DB token secret: {GREEN}updated ✓{RESET}")

    print(f"{GREEN}  {user_id}: fully provisioned ✓{RESET}")
    return True


# ── Entry point ───────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="Re-provision a partially-provisioned Ludus user."
    )
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--userid", metavar="ID",
                       help="Ludus user ID to reprovision (e.g. BF)")
    group.add_argument("--all", action="store_true",
                       help="Check and reprovision all non-ROOT users with gaps")
    parser.add_argument("--config", default=DEFAULT_CONFIG,
                        help=f"Path to config.yml (default: {DEFAULT_CONFIG})")
    args = parser.parse_args()

    if os.geteuid() != 0:
        print(f"{RED}Error: this script must be run as root.{RESET}", file=sys.stderr)
        sys.exit(1)

    cfg = load_config(args.config)
    key = cfg.get("database_encryption_key", "").encode()
    if len(key) < 32:
        print(f"{RED}Error: database_encryption_key in config.yml must be 32 chars.{RESET}",
              file=sys.stderr)
        sys.exit(1)

    db = db_path(cfg)
    if not os.path.exists(db):
        print(f"{RED}Error: Ludus database not found at {db}{RESET}", file=sys.stderr)
        sys.exit(1)

    if args.all:
        user_ids = get_all_user_ids(db)
        if not user_ids:
            print("No non-ROOT users found.")
            sys.exit(0)
        failed = []
        for uid in user_ids:
            ok = reprovision_user(uid, cfg, key)
            if not ok:
                failed.append(uid)
        print()
        if failed:
            print(f"{RED}Failed to reprovision: {', '.join(failed)}{RESET}")
            sys.exit(1)
        print(f"{GREEN}All users provisioned successfully.{RESET}")
    else:
        ok = reprovision_user(args.userid, cfg, key)
        sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
