#!/usr/bin/env python3
"""Verify live password rotation without printing passwords or login tokens."""

import argparse
import json
import os
import ssl
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--user")
    parser.add_argument("--api-key-env", default="LUDUS_API_KEY")
    parser.add_argument("--proxmox-url", default="https://127.0.0.1:8006")
    parser.add_argument("--ludus-url", default="https://127.0.0.1:8080")
    args = parser.parse_args()
    old_password = sys.stdin.read()
    if not old_password:
        raise RuntimeError("old password must be supplied on stdin")
    command = ["ludus", "user", "creds", "get", "--json"]
    if args.user:
        command += ["--user", args.user]
    result = subprocess.run(command, capture_output=True, text=True, timeout=120)
    if result.returncode:
        raise RuntimeError("cannot retrieve the rotated credentials")
    credentials = json.loads(result.stdout)["result"]
    new_password = credentials["proxmoxPassword"]
    if old_password == new_password:
        raise RuntimeError("the saved password was not rotated")

    # CI appliances use their generated self-signed certificates.
    opener = urllib.request.build_opener(
        NoRedirect(), urllib.request.HTTPSHandler(context=ssl._create_unverified_context())
    )

    def login(url, fields):
        data = urllib.parse.urlencode(fields).encode()
        request = urllib.request.Request(url, data=data)
        try:
            with opener.open(request, timeout=30) as response:
                return response.status, json.load(response)
        except urllib.error.HTTPError as error:
            return error.code, {}

    for password, expected in ((new_password, True), (old_password, False)):
        username = credentials["proxmoxUsername"] + "@" + credentials["proxmoxRealm"]
        status, result = login(args.proxmox_url + "/api2/json/access/ticket", {"username": username, "password": password})
        if expected:
            ticket = result.get("data", {})
            if status != 200 or ticket.get("username") != username or not ticket.get("CSRFPreventionToken") or "!tfa!" in ticket.get("ticket", ""):
                raise RuntimeError("new Proxmox password did not produce a full login")
        elif status != 401:
            raise RuntimeError("old Proxmox password was not rejected")
        status, result = login(args.ludus_url + "/api/collections/users/auth-with-password", {"identity": credentials["ludusEmail"], "password": password})
        if expected and (status != 200 or not result.get("token")):
            raise RuntimeError("new Ludus password did not authenticate")
        if not expected and status != 400:
            raise RuntimeError("old Ludus password was not rejected")

    key = os.environ.get(args.api_key_env)
    if not key:
        raise RuntimeError("the pre-rotation API key was not supplied")
    env = dict(os.environ, LUDUS_API_KEY=key)
    result = subprocess.run(["ludus", "user", "list", "--json"], env=env, capture_output=True, text=True, timeout=30)
    if result.returncode or not any(user["userID"] == key.split(".", 1)[0] for user in json.loads(result.stdout)):
        raise RuntimeError("the pre-rotation API key stopped working")
    print("PASS: new passwords authenticate, old passwords fail, and the API key still works")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, KeyError, ValueError, OSError, subprocess.SubprocessError) as error:
        # Third-party exceptions can contain response bodies, URLs, or command
        # arguments. Only our own fixed diagnostic strings are safe to print.
        message = str(error) if isinstance(error, RuntimeError) else type(error).__name__
        print("Password rotation check failed: " + message, file=sys.stderr)
        sys.exit(1)
