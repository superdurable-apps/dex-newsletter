"""Read and update the local Dex connector connection store.

The store is the file Dex Web writes in Connections (by default
~/.dex/connectors/connections.json, or $DEX_CONNECTOR_CONFIG_FILE). It holds
plaintext development credentials. The application loads it with the
connector SDK's localconfig package, which requires a regular file with mode
0600 and schema version connectors.dex.dev/local-connections/v1alpha1, rejects
unknown fields, and rejects two connections with the same connector ID and
connection name. Every write here keeps those rules.

Nothing in this module prints, logs, or returns a credential in a message.
Python 3 standard library only.
"""

from __future__ import annotations

import contextlib
import copy
import json
import os
import shutil
import tempfile
from datetime import datetime, timedelta, timezone

SCHEMA_VERSION = "connectors.dex.dev/local-connections/v1alpha1"
FILE_MODE = 0o600
DIRECTORY_MODE = 0o700

# Google OAuth access tokens last one hour. The recorded expiry is a little
# earlier, so the application reports "credentials are expired" instead of
# calling Google with a token that is about to die.
GOOGLE_ACCESS_TOKEN_LIFETIME = timedelta(minutes=55)

# The Google connectors this application uses. modulePath and moduleVersion
# must match go.mod (test_local_connections checks it); connectionName is the
# name the application loads at startup.
GOOGLE_CONNECTORS = {
    "gmail": {
        "modulePath": "github.com/superdurable/dex-connectors-library/connectors/google/gmail",
        "moduleVersion": "v0.13.0",
        "connectionName": "newsletter-sender",
        "scopes": [
            "openid",
            "https://www.googleapis.com/auth/userinfo.email",
            "https://www.googleapis.com/auth/gmail.readonly",
            "https://www.googleapis.com/auth/gmail.send",
        ],
        "requiresPrimaryEmail": True,
    },
}


class ConnectionsFileError(Exception):
    """A connections file or credential value that cannot be used."""


def default_connections_path() -> str:
    """Returns $DEX_CONNECTOR_CONFIG_FILE, or Dex Web's default store path."""
    configured = os.environ.get("DEX_CONNECTOR_CONFIG_FILE", "").strip()
    return configured or os.path.expanduser("~/.dex/connectors/connections.json")


def load_document(path: str, *, missing_ok: bool = False) -> dict:
    """Reads a connections file.

    A missing file is an error unless missing_ok, in which case an empty store
    is returned.
    """
    try:
        with open(path, encoding="utf-8") as handle:
            document = json.load(handle)
    except FileNotFoundError:
        if missing_ok:
            return {"schemaVersion": SCHEMA_VERSION, "connections": []}
        raise ConnectionsFileError(
            f"{path} does not exist; open Dex Web Connections once or pass --connections-file"
        ) from None
    except json.JSONDecodeError as error:
        raise ConnectionsFileError(f"{path} is not valid JSON (line {error.lineno})") from None
    if not isinstance(document, dict) or document.get("schemaVersion") != SCHEMA_VERSION:
        raise ConnectionsFileError(f"{path} is not a {SCHEMA_VERSION} connections file")
    if not isinstance(document.get("connections", []), list):
        raise ConnectionsFileError(f"{path}: connections must be a list")
    return document


def find_connection(document: dict, connector_id: str, connection_name: str) -> dict | None:
    """Returns the connection record for connector_id and connection_name."""
    for record in document.get("connections") or []:
        if record.get("connectorId") == connector_id and record.get("connectionName") == connection_name:
            return record
    return None


def with_connection(document: dict, record: dict) -> dict:
    """Returns a copy of document with record stored.

    A connection with the same connector ID and connection name is replaced in
    place; otherwise record is appended. Every other connection and every
    Trigger binding is kept unchanged. document itself is not modified.
    """
    updated = copy.deepcopy(document)
    connections = updated.setdefault("connections", [])
    key = (record["connectorId"], record["connectionName"])
    matches = [
        index for index, existing in enumerate(connections)
        if (existing.get("connectorId"), existing.get("connectionName")) == key
    ]
    if matches:
        connections[matches[0]] = copy.deepcopy(record)
        # The application rejects a duplicated connection, so drop any extra copies.
        for index in reversed(matches[1:]):
            del connections[index]
    else:
        connections.append(copy.deepcopy(record))
    return updated


def format_timestamp(moment: datetime) -> str:
    """Formats moment as an RFC 3339 UTC timestamp such as 2026-01-02T03:04:05Z."""
    if moment.tzinfo is None:
        raise ValueError("timestamp must be timezone-aware")
    return moment.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def parse_timestamp(text: str) -> datetime:
    """Parses an RFC 3339 timestamp as written by Dex Web or format_timestamp."""
    normalized = text.strip()
    if normalized.endswith(("Z", "z")):
        normalized = normalized[:-1] + "+00:00"
    # Dex Web may write up to nine fractional digits; Python 3.10 and earlier
    # accept exactly three or six.
    if "." in normalized:
        whole, rest = normalized.split(".", 1)
        digits = len(rest) - len(rest.lstrip("0123456789"))
        normalized = whole + "." + rest[:digits][:6].ljust(6, "0") + rest[digits:]
    parsed = datetime.fromisoformat(normalized)
    if parsed.tzinfo is None:
        raise ValueError(f"timestamp {text!r} has no time zone")
    return parsed


def credential_expired(record: dict, now: datetime) -> bool:
    """Reports whether record's credentialExpiresAt is at or before now."""
    expires_at = record.get("credentialExpiresAt")
    if not expires_at:
        return False
    try:
        return parse_timestamp(expires_at) <= now
    except ValueError:
        return True


def validate_google_access_token(token: str) -> str:
    """Returns token stripped, or raises without echoing it."""
    token = token.strip()
    if not token.startswith("ya29.") or any(character.isspace() for character in token):
        raise ConnectionsFileError(
            "expected a Google OAuth access token (it starts with ya29.); nothing changed"
        )
    return token


def validate_email_address(address: str) -> str:
    """Returns address stripped, or raises when it is clearly not one address."""
    address = address.strip()
    local, separator, domain = address.partition("@")
    if not separator or not local or "." not in domain or "@" in domain or any(c.isspace() for c in address):
        raise ConnectionsFileError(f"{address!r} is not an email address; nothing changed")
    return address


def google_connection_record(
    connector_id: str,
    access_token: str,
    now: datetime,
    *,
    connection_name: str | None = None,
    primary_email: str | None = None,
    lifetime: timedelta = GOOGLE_ACCESS_TOKEN_LIFETIME,
) -> dict:
    """Builds the connections-file record Dex Web would store for a Google grant."""
    if connector_id not in GOOGLE_CONNECTORS:
        raise ConnectionsFileError(f"unknown Google connector {connector_id!r}")
    connector = GOOGLE_CONNECTORS[connector_id]
    credentials = {"access_token": validate_google_access_token(access_token)}
    if connector["requiresPrimaryEmail"]:
        if not primary_email:
            raise ConnectionsFileError(f"{connector_id} needs the authorized account's email address")
        credentials["primary_email"] = validate_email_address(primary_email)
    elif primary_email:
        raise ConnectionsFileError(f"{connector_id} does not take a primary email address")
    return {
        "connectorId": connector_id,
        "modulePath": connector["modulePath"],
        "moduleVersion": connector["moduleVersion"],
        "provider": "google",
        "connectionName": connection_name or connector["connectionName"],
        "configuration": {},
        "credentials": credentials,
        "credentialExpiresAt": format_timestamp(now + lifetime),
    }


def backup_path_for(path: str, now: datetime) -> str:
    """Returns an unused path.bak-YYYYmmddHHMMSS[-N] next to path."""
    base = f"{path}.bak-{now.astimezone(timezone.utc).strftime('%Y%m%d%H%M%S')}"
    candidate, counter = base, 1
    while os.path.lexists(candidate):
        candidate, counter = f"{base}-{counter}", counter + 1
    return candidate


def copy_to_private_file(source: str, destination: str) -> None:
    """Copies source to a new file destination that is mode 0600 from creation.

    The file is created with O_EXCL (an existing path, including a symbolic
    link, is never reused or followed) and made private before any byte is
    written, so the plaintext credentials are never readable by other users,
    whatever the umask. A partial copy is removed.
    """
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(destination, flags, FILE_MODE)
    try:
        with os.fdopen(descriptor, "wb") as target:
            # The umask can only clear bits of FILE_MODE; set it exactly.
            os.fchmod(target.fileno(), FILE_MODE)
            with open(source, "rb") as original:
                shutil.copyfileobj(original, target)
            target.flush()
            os.fsync(target.fileno())
    except BaseException:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(destination)
        raise


def write_document(path: str, document: dict, now: datetime, *, backup: bool = True) -> str | None:
    """Atomically replaces path with document, mode 0600.

    Unless backup is False, the current file, if any, is first copied to a
    backup next to it that is mode 0600 from the moment it is created. The
    backup holds the same plaintext credentials; delete it when you no longer
    need it. Returns the backup path, or None when none was made. An existing
    directory keeps its mode; only a directory created here is made 0700.
    """
    if os.path.islink(path):
        raise ConnectionsFileError(f"{path} is a symbolic link; the application requires a regular file")
    directory = os.path.dirname(os.path.abspath(path))
    os.makedirs(directory, mode=DIRECTORY_MODE, exist_ok=True)

    backup_path = None
    if backup and os.path.exists(path):
        backup_path = backup_path_for(path, now)
        copy_to_private_file(path, backup_path)

    descriptor, temporary = tempfile.mkstemp(dir=directory, prefix=".connections.", suffix=".tmp")
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            json.dump(document, handle, indent=2)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temporary, FILE_MODE)
        os.replace(temporary, path)
    except BaseException:
        if os.path.exists(temporary):
            os.unlink(temporary)
        raise
    return backup_path
