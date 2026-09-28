#!/usr/bin/env python3
"""Store a Google OAuth access token as a local Dex connection.

Fallback for when Dex Web Connections cannot save a Google grant (for example
Gmail connector v0.11.1 and earlier with Dex CLI v0.13.8, which fail with
CONNECTOR_OAUTH_SCOPE_INSUFFICIENT because they request the `email` scope
alias). Authorize the connector's scopes with your
own OAuth client in the Google OAuth 2.0 Playground, copy the access token, and
run:

    scripts/local-connections/set-google-connection.py --connector gmail

The token is read with a hidden prompt, or with --from-clipboard from the macOS
clipboard (which is then cleared) when there is no terminal, so it never
appears on screen or in shell history. The script backs up the connections
file, replaces only the chosen connection, writes the file atomically with
mode 0600, and records credentialExpiresAt 55 minutes ahead because Google
access tokens last one hour. It never prints the token.
"""

from __future__ import annotations

import argparse
import getpass
import os
import shutil
import subprocess
import sys
from datetime import datetime, timezone

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import local_connections  # noqa: E402  (the path above makes the sibling module importable)


def read_clipboard_token() -> str:
    """Reads and then clears the macOS clipboard."""
    if not shutil.which("pbpaste") or not shutil.which("pbcopy"):
        raise local_connections.ConnectionsFileError("--from-clipboard needs macOS pbpaste and pbcopy")
    token = subprocess.run(["pbpaste"], capture_output=True, text=True, check=True).stdout
    subprocess.run(["pbcopy"], input="", text=True, check=True)
    return token


def parse_arguments(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Store a Google OAuth access token as a local Dex connection.",
        epilog="Scopes to authorize: "
        + "; ".join(
            f"{name}: {' '.join(connector['scopes'])}"
            for name, connector in local_connections.GOOGLE_CONNECTORS.items()
        ),
    )
    parser.add_argument("--connector", required=True, choices=sorted(local_connections.GOOGLE_CONNECTORS))
    parser.add_argument(
        "--connection-name",
        help="connection name (default: newsletter-sender)",
    )
    parser.add_argument("--primary-email", help="gmail only: the address of the account you authorized")
    parser.add_argument(
        "--from-clipboard",
        action="store_true",
        help="read the token from the macOS clipboard and clear it, instead of a hidden prompt",
    )
    parser.add_argument(
        "--connections-file",
        default=local_connections.default_connections_path(),
        help="connections file (default: $DEX_CONNECTOR_CONFIG_FILE or ~/.dex/connectors/connections.json)",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    arguments = parse_arguments(sys.argv[1:] if argv is None else argv)
    connector = local_connections.GOOGLE_CONNECTORS[arguments.connector]
    try:
        # Read the store first so a bad path fails before the token is requested.
        document = local_connections.load_document(arguments.connections_file)
        primary_email = arguments.primary_email
        if arguments.from_clipboard:
            if connector["requiresPrimaryEmail"] and not primary_email:
                raise local_connections.ConnectionsFileError("--from-clipboard with gmail needs --primary-email")
            access_token = read_clipboard_token()
        else:
            print(f"Scopes: {' '.join(connector['scopes'])}")
            access_token = getpass.getpass(f"{arguments.connector} access token (hidden): ")
            if connector["requiresPrimaryEmail"] and not primary_email:
                primary_email = input("Email address of the account you authorized: ")
        now = datetime.now(timezone.utc)
        record = local_connections.google_connection_record(
            arguments.connector,
            access_token,
            now,
            connection_name=arguments.connection_name,
            primary_email=primary_email,
        )
        backup = local_connections.write_document(
            arguments.connections_file, local_connections.with_connection(document, record), now
        )
    except local_connections.ConnectionsFileError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1

    account = f" for {record['credentials']['primary_email']}" if "primary_email" in record["credentials"] else ""
    print(f"Saved {record['connectorId']}/{record['connectionName']}{account} in {arguments.connections_file}.")
    print(f"The token expires at {record['credentialExpiresAt']}.")
    if backup:
        print(f"Backup (contains credentials; delete it when done): {backup}")
    print(
        "The application re-reads credentials before every call. If this connection did not exist when "
        "the application started, restart it (make dev-app)."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
