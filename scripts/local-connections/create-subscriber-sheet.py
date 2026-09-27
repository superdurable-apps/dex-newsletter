#!/usr/bin/env python3
"""Create the newsletter subscriber spreadsheet with the local subscriber-sheets connection.

The Google Sheets connector requests only the drive.file scope, so its token
can read only spreadsheets that the same OAuth client created or was granted.
A spreadsheet made by hand in Google Sheets is invisible to it. This script
creates one with the token stored for google-sheets/subscriber-sheets (by Dex
Web or set-google-connection.py), so the connector can read it:

    scripts/local-connections/create-subscriber-sheet.py --subscriber you@example.com

The new spreadsheet has one tab with the header row "email", "status" and one
"active" row per --subscriber. The script prints the spreadsheet URL and ID,
never the token.
"""

from __future__ import annotations

import argparse
import json
import os
import ssl
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import local_connections  # noqa: E402  (the path above makes the sibling module importable)

SHEETS_CREATE_URL = "https://sheets.googleapis.com/v4/spreadsheets?fields=spreadsheetId,spreadsheetUrl"
# The python.org macOS Python ships without CA certificates until its
# "Install Certificates" step runs; the system bundle is the fallback.
SYSTEM_CA_BUNDLE = "/etc/ssl/cert.pem"


def spreadsheet_body(title: str, tab: str, subscribers: list[str]) -> dict:
    """Returns a Sheets API spreadsheets.create body with the subscriber layout."""
    rows = [["email", "status"]] + [[address, "active"] for address in subscribers]
    return {
        "properties": {"title": title},
        "sheets": [{
            "properties": {"title": tab},
            "data": [{
                "startRow": 0,
                "startColumn": 0,
                "rowData": [
                    {"values": [{"userEnteredValue": {"stringValue": cell}} for cell in row]} for row in rows
                ],
            }],
        }],
    }


def tls_context() -> ssl.SSLContext:
    """Returns the default TLS context, falling back to the system CA bundle."""
    context = ssl.create_default_context()
    if not context.get_ca_certs() and os.path.exists(SYSTEM_CA_BUNDLE):
        context.load_verify_locations(cafile=SYSTEM_CA_BUNDLE)
    return context


def google_error_summary(error: urllib.error.HTTPError) -> str:
    """Summarizes a Google API error response without echoing request headers."""
    try:
        detail = json.loads(error.read() or b"{}").get("error", {})
    except (ValueError, AttributeError):
        detail = {}
    if not isinstance(detail, dict):
        detail = {}
    summary = f"HTTP {error.code} {detail.get('status', '')} {detail.get('message', '')}".strip()
    if error.code == 401:
        summary += " (the token expired or was revoked; reconnect subscriber-sheets)"
    return summary


def parse_arguments(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Create the newsletter subscriber spreadsheet.")
    parser.add_argument("--connection-name", default=local_connections.GOOGLE_CONNECTORS["google-sheets"]["connectionName"])
    parser.add_argument("--title", default="Dex Tech Blog subscribers", help="spreadsheet title")
    parser.add_argument("--tab", default="Subscribers", help="tab (sheet) title")
    parser.add_argument(
        "--subscriber", action="append", default=[], metavar="EMAIL", help="add an active subscriber row (repeatable)"
    )
    parser.add_argument(
        "--connections-file",
        default=local_connections.default_connections_path(),
        help="connections file (default: $DEX_CONNECTOR_CONFIG_FILE or ~/.dex/connectors/connections.json)",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    arguments = parse_arguments(sys.argv[1:] if argv is None else argv)
    try:
        if "!" in arguments.tab or "'" in arguments.tab:
            raise local_connections.ConnectionsFileError("--tab must not contain ! or '")
        subscribers = [local_connections.validate_email_address(address) for address in arguments.subscriber]
        document = local_connections.load_document(arguments.connections_file)
        record = local_connections.find_connection(document, "google-sheets", arguments.connection_name)
        if record is None:
            raise local_connections.ConnectionsFileError(
                f"no google-sheets/{arguments.connection_name} connection; connect it in Dex Web Connections "
                "or with set-google-connection.py first"
            )
        if local_connections.credential_expired(record, datetime.now(timezone.utc)):
            raise local_connections.ConnectionsFileError(
                f"the google-sheets/{arguments.connection_name} token has expired; reconnect it first"
            )
        token = (record.get("credentials") or {}).get("access_token", "")
        if not token:
            raise local_connections.ConnectionsFileError(
                f"the google-sheets/{arguments.connection_name} connection has no access token; reconnect it"
            )
    except local_connections.ConnectionsFileError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1

    request = urllib.request.Request(
        SHEETS_CREATE_URL,
        data=json.dumps(spreadsheet_body(arguments.title, arguments.tab, subscribers)).encode("utf-8"),
        method="POST",
        headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30, context=tls_context()) as response:
            created = json.load(response)
    except urllib.error.HTTPError as error:
        print(f"error: Google Sheets rejected the request: {google_error_summary(error)}", file=sys.stderr)
        return 1
    except urllib.error.URLError as error:
        print(f"error: could not reach Google Sheets: {error.reason}", file=sys.stderr)
        return 1

    print(f"Spreadsheet URL: {created['spreadsheetUrl']}")
    print(f"Spreadsheet ID:  {created['spreadsheetId']}")
    print(f"Tab: {arguments.tab}   Range: A:B")
    print(
        "Set these in Dex Web (LoadNewsletterSubscribers Step configuration) or in "
        "newsletter.subscriberSheet of TECH_BLOG_CONFIG_FILE."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
