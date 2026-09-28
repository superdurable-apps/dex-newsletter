"""Offline tests for the local connection helper scripts.

Run with: python3 -m unittest discover -s scripts/local-connections -p 'test_*.py'
Every test uses a temporary directory; none reads the real connection store or
calls the network.
"""

from __future__ import annotations

import contextlib
import importlib.util
import io
import json
import os
import re
import stat
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from unittest import mock

import local_connections

SCRIPT_DIRECTORY = os.path.dirname(os.path.abspath(__file__))
REPOSITORY_ROOT = os.path.dirname(os.path.dirname(SCRIPT_DIRECTORY))
NOW = datetime(2026, 1, 2, 3, 4, 5, tzinfo=timezone.utc)
# A fake token, assembled from two literals so that secret scanners do not
# mistake the source for a real Google access token.
TOKEN = "ya29." + "test-token-that-must-never-be-printed"


def load_script(file_name: str):
    """Imports a hyphenated script file as a module without running main()."""
    path = os.path.join(SCRIPT_DIRECTORY, file_name)
    spec = importlib.util.spec_from_file_location(os.path.splitext(file_name)[0].replace("-", "_"), path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def sample_document() -> dict:
    return {
        "schemaVersion": local_connections.SCHEMA_VERSION,
        "connections": [
            {
                "connectorId": "slack", "modulePath": "example/slack", "moduleVersion": "v1.0.0",
                "provider": "slack", "connectionName": "slack-workspace", "configuration": {},
                "credentials": {"bot_token": "unchanged-placeholder"},
            },
            {
                "connectorId": "gmail", "modulePath": "example/gmail", "moduleVersion": "v0.1.0",
                "provider": "google", "connectionName": "newsletter-sender", "configuration": {},
                "credentials": {"access_token": "ya29.old", "primary_email": "old@example.com"},
                "credentialExpiresAt": "2025-01-01T00:00:00.123456789Z",
            },
        ],
        "triggerBindings": [{
            "connectorId": "slack", "connectionName": "slack-workspace", "triggerName": "channelThreadCreated",
            "bindingName": "tech-blog-newsletter-request", "configuration": {"channelId": "C0123456789"},
        }],
    }


def write_store(directory: str, document: dict) -> str:
    path = os.path.join(directory, "connections.json")
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(document, handle)
    os.chmod(path, 0o600)
    return path


class RecordTest(unittest.TestCase):
    def test_gmail_record_has_exactly_the_localconfig_fields(self):
        record = local_connections.google_connection_record("gmail", TOKEN, NOW, primary_email=" sender@example.com ")
        self.assertEqual(set(record), {
            "connectorId", "modulePath", "moduleVersion", "provider", "connectionName",
            "configuration", "credentials", "credentialExpiresAt",
        })
        self.assertEqual(record["connectionName"], "newsletter-sender")
        self.assertEqual(record["credentials"], {"access_token": TOKEN, "primary_email": "sender@example.com"})
        self.assertEqual(record["credentialExpiresAt"], "2026-01-02T03:59:05Z")

    def test_gmail_record_requires_the_primary_email(self):
        record = local_connections.google_connection_record("gmail", TOKEN, NOW, connection_name="other", primary_email="a@example.com")
        self.assertEqual(record["connectionName"], "other")
        with self.assertRaises(local_connections.ConnectionsFileError):
            local_connections.google_connection_record("gmail", TOKEN, NOW)
        with self.assertRaises(local_connections.ConnectionsFileError):
            local_connections.google_connection_record("google-sheets", TOKEN, NOW)

    def test_invalid_input_is_rejected_without_echoing_the_token(self):
        for token in ["", "not-a-google-token", "ya29.with space"]:
            with self.assertRaises(local_connections.ConnectionsFileError) as raised:
                local_connections.google_connection_record("gmail", token, NOW, primary_email="sender@example.com")
            if token:
                self.assertNotIn(token, str(raised.exception))
        for address in ["", "no-at-sign", "a@localhost", "a@b@example.com", "a b@example.com"]:
            with self.assertRaises(local_connections.ConnectionsFileError):
                local_connections.google_connection_record("gmail", TOKEN, NOW, primary_email=address)

    def test_module_versions_match_go_mod(self):
        with open(os.path.join(REPOSITORY_ROOT, "go.mod"), encoding="utf-8") as handle:
            go_mod = handle.read()
        for connector_id, connector in local_connections.GOOGLE_CONNECTORS.items():
            pattern = rf"(?m)^\s*(require\s+)?{re.escape(connector['modulePath'])} (v\S+)"
            match = re.search(pattern, go_mod)
            self.assertIsNotNone(match, f"{connector['modulePath']} is not in go.mod")
            self.assertEqual(match.group(2), connector["moduleVersion"], connector_id)


class DocumentTest(unittest.TestCase):
    def test_with_connection_replaces_in_place_and_keeps_everything_else(self):
        document = sample_document()
        original = json.loads(json.dumps(document))
        record = local_connections.google_connection_record("gmail", TOKEN, NOW, primary_email="new@example.com")
        updated = local_connections.with_connection(document, record)
        self.assertEqual(document, original, "the input document must not change")
        self.assertEqual(updated["connections"][0], original["connections"][0])
        self.assertEqual(updated["connections"][1], record)
        self.assertEqual(len(updated["connections"]), 2)
        self.assertEqual(updated["triggerBindings"], original["triggerBindings"])

    def test_with_connection_appends_a_new_connection_and_drops_duplicates(self):
        document = sample_document()
        document["connections"].append(json.loads(json.dumps(document["connections"][1])))
        second_sender = local_connections.google_connection_record(
            "gmail", TOKEN, NOW, connection_name="second-sender", primary_email="second@example.com"
        )
        appended = local_connections.with_connection(document, second_sender)
        self.assertEqual(appended["connections"][-1], second_sender)
        gmail = local_connections.google_connection_record("gmail", TOKEN, NOW, primary_email="new@example.com")
        deduplicated = local_connections.with_connection(document, gmail)
        names = [(c["connectorId"], c["connectionName"]) for c in deduplicated["connections"]]
        self.assertEqual(names, [("slack", "slack-workspace"), ("gmail", "newsletter-sender")])

    def test_find_connection_and_expiry(self):
        document = sample_document()
        gmail = local_connections.find_connection(document, "gmail", "newsletter-sender")
        self.assertIsNotNone(gmail)
        self.assertIsNone(local_connections.find_connection(document, "gmail", "other"))
        self.assertTrue(local_connections.credential_expired(gmail, NOW), "nanosecond timestamps must parse")
        self.assertFalse(local_connections.credential_expired(document["connections"][0], NOW))
        fresh = dict(gmail, credentialExpiresAt=local_connections.format_timestamp(NOW + timedelta(minutes=1)))
        self.assertFalse(local_connections.credential_expired(fresh, NOW))
        self.assertTrue(local_connections.credential_expired(dict(gmail, credentialExpiresAt="soon"), NOW))

    def test_load_document_rejects_other_files(self):
        with tempfile.TemporaryDirectory() as directory:
            missing = os.path.join(directory, "missing.json")
            with self.assertRaises(local_connections.ConnectionsFileError):
                local_connections.load_document(missing)
            self.assertEqual(local_connections.load_document(missing, missing_ok=True)["connections"], [])
            for contents in ["{", "[]", json.dumps({"schemaVersion": "other", "connections": []})]:
                path = os.path.join(directory, "bad.json")
                with open(path, "w", encoding="utf-8") as handle:
                    handle.write(contents)
                with self.assertRaises(local_connections.ConnectionsFileError):
                    local_connections.load_document(path)

    def test_default_path_honors_the_environment(self):
        with mock.patch.dict(os.environ, {"DEX_CONNECTOR_CONFIG_FILE": "/tmp/example/connections.json"}):
            self.assertEqual(local_connections.default_connections_path(), "/tmp/example/connections.json")
        with mock.patch.dict(os.environ, {"DEX_CONNECTOR_CONFIG_FILE": ""}):
            self.assertTrue(local_connections.default_connections_path().endswith("/.dex/connectors/connections.json"))


class WriteTest(unittest.TestCase):
    def test_write_backs_up_and_replaces_atomically_with_mode_0600(self):
        with tempfile.TemporaryDirectory() as directory:
            path = write_store(directory, sample_document())
            with open(path, encoding="utf-8") as handle:
                before = handle.read()
            updated = local_connections.with_connection(
                sample_document(),
                local_connections.google_connection_record("gmail", TOKEN, NOW, primary_email="sender@example.com"),
            )
            backup = local_connections.write_document(path, updated, NOW)
            self.assertEqual(backup, path + ".bak-20260102030405")
            with open(backup, encoding="utf-8") as handle:
                self.assertEqual(handle.read(), before)
            self.assertEqual(local_connections.load_document(path), updated)
            for written in [path, backup]:
                self.assertEqual(stat.S_IMODE(os.stat(written).st_mode), 0o600, written)
            second_backup = local_connections.write_document(path, updated, NOW)
            self.assertEqual(second_backup, path + ".bak-20260102030405-1", "a backup is never overwritten")
            leftovers = [name for name in os.listdir(directory) if name.endswith(".tmp")]
            self.assertEqual(leftovers, [])

    def test_backup_is_private_from_creation_not_only_after_a_chmod(self):
        # With a permissive umask and every chmod disabled, a backup that was
        # created readable and tightened afterwards would stay 0644.
        previous_umask = os.umask(0o022)
        try:
            with tempfile.TemporaryDirectory() as directory, \
                    mock.patch("os.chmod"), mock.patch("os.fchmod"):
                path = write_store(directory, sample_document())
                backup = local_connections.write_document(path, sample_document(), NOW)
                self.assertEqual(stat.S_IMODE(os.stat(backup).st_mode), 0o600)
                with open(backup, encoding="utf-8") as handle:
                    self.assertEqual(json.load(handle), sample_document())
        finally:
            os.umask(previous_umask)

    def test_write_creates_a_missing_store_without_a_backup(self):
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "new", "connections.json")
            document = local_connections.load_document(path, missing_ok=True)
            self.assertIsNone(local_connections.write_document(path, document, NOW))
            self.assertEqual(stat.S_IMODE(os.stat(path).st_mode), 0o600)

    def test_write_refuses_a_symbolic_link(self):
        with tempfile.TemporaryDirectory() as directory:
            target = write_store(directory, sample_document())
            link = os.path.join(directory, "link.json")
            os.symlink(target, link)
            with self.assertRaises(local_connections.ConnectionsFileError):
                local_connections.write_document(link, sample_document(), NOW)


class ScriptTest(unittest.TestCase):
    def test_set_google_connection_never_prints_the_token(self):
        script = load_script("set-google-connection.py")
        with tempfile.TemporaryDirectory() as directory:
            path = write_store(directory, sample_document())
            output = io.StringIO()
            with mock.patch("getpass.getpass", return_value=TOKEN), \
                    mock.patch("builtins.input", return_value="sender@example.com"), \
                    contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
                status = script.main(["--connector", "gmail", "--connections-file", path])
            self.assertEqual(status, 0, output.getvalue())
            self.assertNotIn(TOKEN, output.getvalue())
            stored = local_connections.find_connection(
                local_connections.load_document(path), "gmail", "newsletter-sender"
            )
            self.assertEqual(stored["credentials"], {"access_token": TOKEN, "primary_email": "sender@example.com"})

    def test_set_google_connection_leaves_the_store_unchanged_on_a_bad_token(self):
        script = load_script("set-google-connection.py")
        with tempfile.TemporaryDirectory() as directory:
            path = write_store(directory, sample_document())
            with open(path, encoding="utf-8") as handle:
                before = handle.read()
            output = io.StringIO()
            with mock.patch("getpass.getpass", return_value="not-a-token"), \
                    contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
                status = script.main(["--connector", "gmail", "--primary-email", "sender@example.com", "--connections-file", path])
            self.assertEqual(status, 1)
            with open(path, encoding="utf-8") as handle:
                self.assertEqual(handle.read(), before)
            self.assertEqual(sorted(os.listdir(directory)), ["connections.json"], "no backup for a failed run")


if __name__ == "__main__":
    unittest.main()
