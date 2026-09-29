#!/usr/bin/env python3
"""Keep the local Gmail connection supplied with delegated access tokens.

Development only. Google access tokens last one hour and Dex Web stores no
refresh token, so a Gmail connection made in Dex Web expires hourly. With
Google Workspace domain-wide delegation, a service account can mint a token
that sends as a user of the domain. This script does that on a schedule:

    scripts/local-connections/refresh-gmail-delegated-token.py \\
        --service-account-key path/to/service-account.json \\
        --sender newsletter@example.com

It signs a JWT assertion (iss: the service account, sub: the sender, scope:
gmail.send) with the key through the `openssl` command, exchanges it at
Google's token endpoint, and writes the token into the newsletter-sender
connection. It then sleeps until 15 minutes before the token expires and
repeats until stopped; --once writes one token and exits. The application
re-reads credentials before every send, so no restart is needed.

The key never leaves the machine except as a signature, and the script never
prints the key, the assertion, or a token. It refuses a key file that other
users can read. Only the first write backs up the connections file.

Setup: in Google Cloud, enable the Gmail API and create a service account key;
in the Workspace Admin console, Security > Access and data control > API
controls > Domain-wide delegation, authorize the service account's client ID
for https://www.googleapis.com/auth/gmail.send.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import shutil
import ssl
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timedelta, timezone

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import local_connections  # noqa: E402  (the path above makes the sibling module importable)

GMAIL_SEND_SCOPE = "https://www.googleapis.com/auth/gmail.send"
GOOGLE_TOKEN_ENDPOINT = "https://oauth2.googleapis.com/token"
JWT_BEARER_GRANT = "urn:ietf:params:oauth:grant-type:jwt-bearer"
ASSERTION_LIFETIME = timedelta(hours=1)
# Refresh this long before a token expires, and record the expiry this much
# earlier than Google's, so a send never starts with a token about to die.
REFRESH_MARGIN = timedelta(minutes=15)
EXPIRY_MARGIN = timedelta(minutes=5)
RETRY_DELAY = timedelta(minutes=1)
# Used when Python has no CA certificates of its own, as with a python.org
# build whose Install Certificates step never ran.
SYSTEM_CA_FILE = "/etc/ssl/cert.pem"


def base64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


def load_service_account(path: str) -> dict:
    """Reads a service account key file that only its owner can read."""
    try:
        status = os.stat(path)
    except OSError as error:
        raise local_connections.ConnectionsFileError(f"cannot read the service account key {path}: {error.strerror}") from None
    if not stat.S_ISREG(status.st_mode):
        raise local_connections.ConnectionsFileError(f"{path} is not a regular file")
    if status.st_mode & 0o077:
        raise local_connections.ConnectionsFileError(f"{path} is readable by other users; run chmod 600 {path}")
    try:
        with open(path, encoding="utf-8") as handle:
            account = json.load(handle)
    except (OSError, ValueError):
        raise local_connections.ConnectionsFileError(f"{path} is not a service account key file") from None
    if account.get("type") != "service_account" or not account.get("client_email") or not str(
        account.get("private_key", "")
    ).startswith("-----BEGIN PRIVATE KEY-----"):
        raise local_connections.ConnectionsFileError(f"{path} is not a service account key file")
    token_uri = account.get("token_uri") or GOOGLE_TOKEN_ENDPOINT
    if token_uri != GOOGLE_TOKEN_ENDPOINT:
        raise local_connections.ConnectionsFileError(f"unexpected token_uri {token_uri!r}; expected {GOOGLE_TOKEN_ENDPOINT}")
    return account


def openssl_sign(data: bytes, private_key_pem: str) -> bytes:
    """Signs data with RSASSA-PKCS1-v1_5 SHA-256 using the openssl command.

    The key is written to a private temporary file for the duration of the
    call and removed afterwards.
    """
    if not shutil.which("openssl"):
        raise local_connections.ConnectionsFileError("the openssl command is required to sign the delegation assertion")
    directory = tempfile.mkdtemp(prefix="dex-gmail-delegation-")
    key_path = os.path.join(directory, "key.pem")
    try:
        descriptor = os.open(key_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            handle.write(private_key_pem)
        result = subprocess.run(["openssl", "dgst", "-sha256", "-sign", key_path], input=data, capture_output=True, check=False)
        if result.returncode != 0 or not result.stdout:
            raise local_connections.ConnectionsFileError("openssl could not sign the delegation assertion with the key")
        return result.stdout
    finally:
        if os.path.exists(key_path):
            os.unlink(key_path)
        os.rmdir(directory)


def signed_assertion(account: dict, sender: str, now: datetime, sign=openssl_sign) -> str:
    """Returns the RS256 JWT assertion that asks for a gmail.send token as sender."""
    header = {"alg": "RS256", "typ": "JWT"}
    if account.get("private_key_id"):
        header["kid"] = account["private_key_id"]
    issued = int(now.timestamp())
    claims = {
        "iss": account["client_email"],
        "sub": sender,
        "scope": GMAIL_SEND_SCOPE,
        "aud": GOOGLE_TOKEN_ENDPOINT,
        "iat": issued,
        "exp": issued + int(ASSERTION_LIFETIME.total_seconds()),
    }
    signing_input = (
        base64url(json.dumps(header, separators=(",", ":")).encode()) + "." + base64url(json.dumps(claims, separators=(",", ":")).encode())
    )
    return signing_input + "." + base64url(sign(signing_input.encode("ascii"), account["private_key"]))


def https_context() -> ssl.SSLContext:
    """Returns a verifying TLS context, with the system roots if Python has none."""
    context = ssl.create_default_context()
    if not context.get_ca_certs() and os.path.exists(SYSTEM_CA_FILE):
        context.load_verify_locations(cafile=SYSTEM_CA_FILE)
    return context


def open_url(request: urllib.request.Request, timeout: float):
    return urllib.request.urlopen(request, timeout=timeout, context=https_context())


def exchange_assertion(assertion: str, opener=open_url) -> tuple[str, int]:
    """Exchanges the assertion for an access token and its lifetime in seconds."""
    body = urllib.parse.urlencode({"grant_type": JWT_BEARER_GRANT, "assertion": assertion}).encode("ascii")
    request = urllib.request.Request(
        GOOGLE_TOKEN_ENDPOINT, data=body, headers={"Content-Type": "application/x-www-form-urlencoded"}, method="POST"
    )
    try:
        with opener(request, timeout=30) as response:
            payload = json.load(response)
    except urllib.error.HTTPError as error:
        raise local_connections.ConnectionsFileError(google_error_summary(error)) from None
    except urllib.error.URLError as error:
        raise local_connections.ConnectionsFileError(f"could not reach Google's token endpoint: {error.reason}") from None
    token = payload.get("access_token")
    expires_in = payload.get("expires_in")
    if not isinstance(token, str) or not isinstance(expires_in, int) or expires_in <= 0:
        raise local_connections.ConnectionsFileError("Google's token response had no usable access token")
    return token, expires_in


def google_error_summary(error: urllib.error.HTTPError) -> str:
    """Describes a token endpoint rejection without echoing the request."""
    try:
        detail = json.load(error)
    except (ValueError, OSError):
        detail = {}
    code = detail.get("error", f"HTTP {error.code}")
    description = detail.get("error_description", "")
    summary = f"Google rejected the delegation: {code}" + (f" ({description})" if description else "")
    if code == "unauthorized_client":
        summary += (
            ". Authorize the service account's client ID for " + GMAIL_SEND_SCOPE + " under Domain-wide delegation in the"
            " Workspace Admin console; a new authorization can take a few minutes to apply."
        )
    elif code == "invalid_grant":
        summary += ". Check that the sender is an active user of the Workspace domain and that this machine's clock is correct."
    return summary


def store_token(connections_file: str, connection_name: str | None, sender: str, token: str, expires_in: int, now: datetime, backup: bool) -> dict:
    """Writes the token into the Gmail connection and returns its record."""
    document = local_connections.load_document(connections_file, missing_ok=True)
    lifetime = max(timedelta(seconds=expires_in) - EXPIRY_MARGIN, timedelta(minutes=1))
    record = local_connections.google_connection_record(
        "gmail", token, now, connection_name=connection_name, primary_email=sender, lifetime=lifetime
    )
    backup_path = local_connections.write_document(connections_file, local_connections.with_connection(document, record), now, backup=backup)
    if backup_path:
        print(f"Backup (contains credentials; delete it when done): {backup_path}")
    return record


def mint_and_store(arguments: argparse.Namespace, account: dict, backup: bool, sign=openssl_sign, opener=open_url) -> tuple[dict, int]:
    now = datetime.now(timezone.utc)
    token, expires_in = exchange_assertion(signed_assertion(account, arguments.sender, now, sign), opener)
    record = store_token(arguments.connections_file, arguments.connection_name, arguments.sender, token, expires_in, now, backup)
    return record, expires_in


def parse_arguments(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Keep the Gmail connection supplied with delegated access tokens.")
    parser.add_argument("--service-account-key", required=True, help="service account key JSON file (mode 0600)")
    parser.add_argument("--sender", required=True, help="Workspace address the newsletter is sent as")
    parser.add_argument("--connection-name", help="connection name (default: newsletter-sender)")
    parser.add_argument(
        "--connections-file",
        default=local_connections.default_connections_path(),
        help="connections file (default: $DEX_CONNECTOR_CONFIG_FILE or ~/.dex/connectors/connections.json)",
    )
    parser.add_argument("--once", action="store_true", help="write one token and exit")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None, sign=openssl_sign, opener=open_url, sleep=time.sleep) -> int:
    arguments = parse_arguments(sys.argv[1:] if argv is None else argv)
    try:
        arguments.sender = local_connections.validate_email_address(arguments.sender)
        account = load_service_account(os.path.expanduser(arguments.service_account_key))
    except local_connections.ConnectionsFileError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    first = True
    while True:
        try:
            record, expires_in = mint_and_store(arguments, account, backup=first, sign=sign, opener=opener)
        except local_connections.ConnectionsFileError as error:
            print(f"error: {error}", file=sys.stderr, flush=True)
            if arguments.once or first:
                return 1
            sleep(RETRY_DELAY.total_seconds())
            continue
        first = False
        print(
            f"Saved gmail/{record['connectionName']} sending as {arguments.sender}; it expires at {record['credentialExpiresAt']}.",
            flush=True,
        )
        if arguments.once:
            return 0
        sleep(max(expires_in - REFRESH_MARGIN.total_seconds(), RETRY_DELAY.total_seconds()))


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(130)
