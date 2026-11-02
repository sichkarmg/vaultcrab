#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 sichkarmg
"""
Export SecureCRT sessions (login/password) to a JSON file for import into Bitwarden.

Requires:  pip install pycryptodome

Usage:
  python securecrt_to_bitwarden.py                       # find the Sessions folder automatically
  python securecrt_to_bitwarden.py -d "C:\\path\\Sessions" -o crt.json
  python securecrt_to_bitwarden.py --ask-passphrase      # if SecureCRT has a Config Passphrase

Then:
  bw login
  export BW_SESSION="$(bw unlock --raw)"
  bw import bitwardenjson crt.json
  shred -u crt.json      # (Windows: sdelete, or delete the file and empty the recycle bin)

IMPORTANT: crt.json contains the passwords in plain text. Delete it right after the import.
"""
import argparse
import getpass
import hashlib
import json
import os
import platform
import sys
import uuid
from pathlib import Path

try:
    from Crypto.Cipher import AES, Blowfish
    from Crypto.Hash import SHA512
    from Crypto.Protocol import KDF
except ImportError:
    sys.exit("Missing module: pip install pycryptodome")

SKIP_FILES = {"__FolderData__.ini", "Default.ini"}

# Fixed keys of the old format ("Password", prefix 'u')
_BF_KEY1 = bytes.fromhex("5FB045A29417D916C6C6A2FF064182B7")
_BF_KEY2 = bytes.fromhex("24A63DDE5BD3B3829C7E06F40816AA07")


# ------------------------------------------------------------------ decryption
def _bcrypt_hash(password: bytes, salt: bytes) -> bytes:
    # As in the reference implementation (HyperSine/how-does-SecureCRT-encrypt-password).
    # pycryptodome is used because the public bcrypt.kdf does not accept an empty
    # passphrase, and SecureCRT's default passphrase is empty.
    password = SHA512.new(password).digest()
    salt = SHA512.new(salt).digest()
    digest = KDF._bcrypt_hash(password, 6, salt, b"OxychromaticBlowfishSwatDynamite", False)
    return b"".join(digest[i:i + 4][::-1] for i in range(0, len(digest), 4))


def bcrypt_pbkdf(password: bytes, salt: bytes, key_length: int, rounds: int) -> bytes:
    """bcrypt_pbkdf as in OpenSSH (a PBKDF2-like scheme with a bcrypt hash as the PRF)."""
    hashsize = 32
    out_len = (key_length + hashsize - 1) // hashsize * hashsize
    out = KDF.PBKDF2(password, salt, out_len, rounds, prf=_bcrypt_hash)
    stride = (key_length + hashsize - 1) // hashsize
    return bytes(out[(i // stride) + (i % stride) * hashsize] for i in range(key_length))


def decrypt_v2(value: str, passphrase: str = "") -> str:
    """'Password V2' = '02:<hex>' or '03:<hex>'."""
    prefix, _, hexdata = value.partition(":")
    data = bytes.fromhex(hexdata)
    pw = passphrase.encode("utf-8")
    if prefix == "02":
        key, iv = hashlib.sha256(pw).digest(), b"\x00" * 16
    elif prefix == "03":
        salt, data = data[:16], data[16:]
        kdf = bcrypt_pbkdf(pw, salt, 48, 16)
        key, iv = kdf[:32], kdf[32:48]
    else:
        raise ValueError(f"unknown prefix {prefix!r}")
    padded = AES.new(key, AES.MODE_CBC, iv).decrypt(data)
    n = int.from_bytes(padded[:4], "little")
    plain = padded[4:4 + n]
    checksum = padded[4 + n:4 + n + 32]
    if len(plain) != n or hashlib.sha256(plain).digest() != checksum:
        raise ValueError("checksum mismatch (wrong Config Passphrase?)")
    return plain.decode("utf-8")


def decrypt_v1(value: str) -> str:
    """Old 'Password' (hex, sometimes with a leading 'u')."""
    if value.startswith("u"):
        value = value[1:]
    raw = bytes.fromhex(value)
    c1 = Blowfish.new(_BF_KEY1, Blowfish.MODE_CBC, b"\x00" * 8)
    c2 = Blowfish.new(_BF_KEY2, Blowfish.MODE_CBC, b"\x00" * 8)
    padded = c1.decrypt(c2.decrypt(raw)[4:-4])
    out = bytearray()
    for i in range(0, len(padded) - 1, 2):
        if padded[i:i + 2] == b"\x00\x00":
            break
        out += padded[i:i + 2]
    return out.decode("utf-16-le")


# --------------------------------------------------------------------- parsing
def parse_ini(path: Path) -> dict:
    """Lines like  S:"Key"=value  and  D:"Key"=hex."""
    d = {}
    text = path.read_text(encoding="utf-8-sig", errors="replace")
    for line in text.splitlines():
        line = line.strip()
        if len(line) < 5 or line[1] != ":" or line[2] != '"':
            continue
        end = line.find('"=', 3)
        if end == -1:
            continue
        d[line[3:end]] = (line[0], line[end + 2:])
    return d


def get_s(d, key, default=""):
    return d.get(key, ("S", default))[1]


def get_port(d, proto):
    for key in (f"[{proto}] Port", "Port"):
        if key in d:
            try:
                return int(d[key][1], 16)
            except ValueError:
                pass
    return None


def default_sessions_dir():
    system = platform.system()
    home = Path.home()
    candidates = []
    if system == "Windows":
        appdata = Path(os.environ.get("APPDATA", home / "AppData/Roaming"))
        candidates += [appdata / "VanDyke/Config/Sessions"]
    elif system == "Darwin":
        candidates += [home / "Library/Application Support/VanDyke/SecureCRT/Config/Sessions"]
    else:
        candidates += [home / ".vandyke/SecureCRT/Config/Sessions"]
    for c in candidates:
        if c.is_dir():
            return c
    return None


def build_item(rel: Path, d: dict, passphrase: str, folder_id, scheme_map):
    host = get_s(d, "Hostname")
    if not host:
        return None, "no Hostname"
    proto = get_s(d, "Protocol Name", "SSH2")
    user = get_s(d, "Username")
    port = get_port(d, proto)

    password, warn = "", None
    try:
        if get_s(d, "Password V2"):
            password = decrypt_v2(get_s(d, "Password V2"), passphrase)
        elif get_s(d, "Password"):
            password = decrypt_v1(get_s(d, "Password"))
    except Exception as e:  # noqa: BLE001
        warn = f"could not decrypt the password: {e}"

    scheme = scheme_map.get(proto.lower(), "ssh")
    uri = f"{scheme}://{host}" + (f":{port}" if port else "")

    notes = [f"SecureCRT: {rel.with_suffix('').as_posix()}", f"Protocol: {proto}"]
    key_file = get_s(d, "Identity Filename V2") or get_s(d, "Identity Filename")
    if key_file:
        notes.append(f"Identity file: {key_file}")

    item = {
        "id": str(uuid.uuid4()),
        "organizationId": None,
        "folderId": folder_id,
        "type": 1,
        "reprompt": 0,
        "name": rel.stem,
        "notes": "\n".join(notes),
        "favorite": False,
        "login": {
            "uris": [{"match": None, "uri": uri}],
            "username": user or None,
            "password": password or None,
            "totp": None,
        },
        "collectionIds": None,
    }
    return item, warn


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("-d", "--sessions-dir", type=Path, help="the Config/Sessions folder of SecureCRT")
    ap.add_argument("-o", "--output", type=Path, default=Path("securecrt_bitwarden.json"))
    ap.add_argument("-p", "--passphrase", default="", help="Config Passphrase (unsafe: stays in the shell history)")
    ap.add_argument("--ask-passphrase", action="store_true", help="ask for the Config Passphrase")
    ap.add_argument("--root-folder", default="SecureCRT",
                    help="root folder in Bitwarden ('' = none; default SecureCRT)")
    ap.add_argument("--skip-no-password", action="store_true", help="skip sessions without a saved password")
    ap.add_argument("--dry-run", action="store_true", help="only list the sessions, do not write the file")
    args = ap.parse_args()

    sessions = args.sessions_dir or default_sessions_dir()
    if not sessions or not sessions.is_dir():
        sys.exit("Sessions folder not found, give it with -d")
    passphrase = getpass.getpass("Config Passphrase: ") if args.ask_passphrase else args.passphrase

    scheme_map = {"ssh2": "ssh", "ssh1": "ssh", "telnet": "telnet", "rdp": "rdp", "serial": "serial"}
    folders, items, problems = {}, [], []

    def folder_for(rel_dir: Path):
        parts = [p for p in ([args.root_folder] if args.root_folder else []) + list(rel_dir.parts) if p]
        if not parts:
            return None
        name = "/".join(parts)  # nesting in Bitwarden = '/' in the folder name
        if name not in folders:
            folders[name] = str(uuid.uuid4())
        return folders[name]

    for path in sorted(sessions.rglob("*.ini")):
        if path.name in SKIP_FILES:
            continue
        rel = path.relative_to(sessions)
        try:
            d = parse_ini(path)
            has_pw = bool(get_s(d, "Password V2") or get_s(d, "Password"))
            if args.skip_no_password and not has_pw:
                continue
            item, warn = build_item(rel, d, passphrase, folder_for(rel.parent), scheme_map)
        except Exception as e:  # noqa: BLE001
            problems.append((rel, f"parse error: {e}"))
            continue
        if item is None:
            problems.append((rel, warn))
            continue
        if warn:
            problems.append((rel, warn))
        items.append(item)

    print(f"Sessions processed: {len(items)}", file=sys.stderr)
    for rel, msg in problems:
        print(f"  ! {rel.as_posix()}: {msg}", file=sys.stderr)

    if args.dry_run:
        for it in items:
            print(f"  {it['name']:<30} {it['login']['uris'][0]['uri']:<35} "
                  f"user={it['login']['username']} pw={'yes' if it['login']['password'] else 'no'}")
        return

    out = {"encrypted": False,
           "folders": [{"id": i, "name": n} for n, i in folders.items()],
           "items": items}
    fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(out, f, ensure_ascii=False, indent=2)
    print(f"Written: {args.output}  (passwords in plain text - delete it after the import!)", file=sys.stderr)
    print(f'Import:   bw import bitwardenjson "{args.output}"', file=sys.stderr)


if __name__ == "__main__":
    main()
