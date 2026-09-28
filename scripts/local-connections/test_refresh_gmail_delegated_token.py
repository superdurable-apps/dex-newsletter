"""Offline tests for refresh-gmail-delegated-token.py.

Every test uses a temporary directory and a fake token endpoint; none reads
the real connection store or a real key, or calls the network. The signing
test needs the openssl command and generates a throwaway RSA key.
"""

from __future__ import annotations

import base64
import contextlib
import io
import json
import os
import shutil
import subprocess
import tempfile
import unittest
import urllib.error
import urllib.parse
from datetime import datetime, timezone

import local_connections
from test_local_connections import TOKEN, load_script, sample_document, write_store

SENDER = "newsletter@example.com"
SERVICE_ACCOUNT = "sender@example-project.iam.gserviceaccount.com"
# A placeholder, never a usable key; only the signing test uses a real one. It
# is assembled from literals so that secret scanners do not mistake it for one.
PLACEHOLDER_KEY = "-----BEGIN " + "PRIVATE KEY-----\nplaceholder\n-----END " + "PRIVATE KEY-----\n"


def decode_segment(segment: str) -> dict:
    return json.loads(base64.urlsafe_b64decode(segment + "=" * (-len(segment) % 4)))


def write_key_file(directory: str, private_key: str = PLACEHOLDER_KEY, mode: int = 0o600) -> str:
    path = os.path.join(directory, "service-account.json")
    with open(path, "w", encoding="utf-8") as handle:
        json.dump({
            "type": "service_account", "client_email": SERVICE_ACCOUNT, "private_key_id": "key-id",
            "private_key": private_key, "token_uri": "https://oauth2.googleapis.com/token",
        }, handle)
    os.chmod(path, mode)
    return path


def fake_sign(data: bytes, private_key_pem: str) -> bytes:
    return b"signature"


class FakeEndpoint:
    """Answers token requests in order: a dict is a JSON reply, an int an HTTP error."""

    def __init__(self, *replies):
        self.replies = list(replies)
        self.requests = []

    def __call__(self, request, timeout):
        self.requests.append(request)
        reply = self.replies.pop(0)
        if isinstance(reply, tuple):
            status, body = reply
            raise urllib.error.HTTPError(request.full_url, status, "error", {}, io.BytesIO(json.dumps(body).encode()))
        return io.BytesIO(json.dumps(reply).encode())


class StopLoop(Exception):
    pass


class RefreshTest(unittest.TestCase):
    def setUp(self):
        self.script = load_script("refresh-gmail-delegated-token.py")
        self.directory = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.directory)
        self.store = write_store(self.directory, sample_document())
        self.key = write_key_file(self.directory)

    def run_script(self, endpoint, *extra, sleep=None):
        output = io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            try:
                status = self.script.main(
                    ["--service-account-key", self.key, "--sender", SENDER, "--connections-file", self.store, *extra],
                    sign=fake_sign, opener=endpoint, sleep=sleep or (lambda seconds: None),
                )
            except StopLoop:
                status = None
        return status, output.getvalue()

    def backups(self):
        return [name for name in os.listdir(self.directory) if name.startswith("connections.json.bak-")]

    def test_once_writes_the_delegated_token_without_printing_it(self):
        endpoint = FakeEndpoint({"access_token": TOKEN, "expires_in": 3599, "token_type": "Bearer"})
        before = datetime.now(timezone.utc)
        status, output = self.run_script(endpoint, "--once")
        self.assertEqual(status, 0, output)
        self.assertNotIn(TOKEN, output)

        body = urllib.parse.parse_qs(endpoint.requests[0].data.decode())
        self.assertEqual(body["grant_type"], ["urn:ietf:params:oauth:grant-type:jwt-bearer"])
        self.assertNotIn(body["assertion"][0], output)
        header, claims, signature = body["assertion"][0].split(".")
        self.assertEqual(decode_segment(header), {"alg": "RS256", "typ": "JWT", "kid": "key-id"})
        claims = decode_segment(claims)
        self.assertEqual(claims["iss"], SERVICE_ACCOUNT)
        self.assertEqual(claims["sub"], SENDER)
        self.assertEqual(claims["scope"], "https://www.googleapis.com/auth/gmail.send")
        self.assertEqual(claims["aud"], "https://oauth2.googleapis.com/token")
        self.assertEqual(claims["exp"] - claims["iat"], 3600)
        self.assertEqual(signature, base64.urlsafe_b64encode(b"signature").rstrip(b"=").decode())

        document = local_connections.load_document(self.store)
        stored = local_connections.find_connection(document, "gmail", "newsletter-sender")
        self.assertEqual(stored["credentials"], {"access_token": TOKEN, "primary_email": SENDER})
        self.assertEqual(stored["moduleVersion"], local_connections.GOOGLE_CONNECTORS["gmail"]["moduleVersion"])
        expires = local_connections.parse_timestamp(stored["credentialExpiresAt"])
        # Recorded five minutes before Google's expiry.
        self.assertLess(abs((expires - before).total_seconds() - (3599 - 300)), 30)
        self.assertEqual(local_connections.find_connection(document, "slack", "slack-workspace"), sample_document()["connections"][0])
        self.assertEqual(len(self.backups()), 1)

    def test_the_loop_backs_up_once_and_retries_after_a_failure(self):
        endpoint = FakeEndpoint(
            {"access_token": TOKEN, "expires_in": 3600},
            (503, {"error": "backendError"}),
            {"access_token": TOKEN + "-2", "expires_in": 3600},
        )
        sleeps = []

        def sleep(seconds):
            sleeps.append(seconds)
            if len(sleeps) == 3:
                raise StopLoop

        status, output = self.run_script(endpoint, sleep=sleep)
        self.assertIsNone(status, output)
        self.assertEqual(sleeps, [45 * 60, 60, 45 * 60])
        self.assertIn("backendError", output)
        self.assertNotIn(TOKEN, output)
        stored = local_connections.find_connection(local_connections.load_document(self.store), "gmail", "newsletter-sender")
        self.assertEqual(stored["credentials"]["access_token"], TOKEN + "-2")
        self.assertEqual(len(self.backups()), 1, "only the first write makes a backup")

    def test_an_unauthorized_first_attempt_explains_delegation_and_changes_nothing(self):
        with open(self.store, encoding="utf-8") as handle:
            before = handle.read()
        endpoint = FakeEndpoint((401, {"error": "unauthorized_client", "error_description": "Client is unauthorized"}))
        status, output = self.run_script(endpoint)
        self.assertEqual(status, 1)
        self.assertIn("Domain-wide delegation", output)
        with open(self.store, encoding="utf-8") as handle:
            self.assertEqual(handle.read(), before)
        self.assertEqual(self.backups(), [])

    def test_a_key_that_others_can_read_is_refused(self):
        os.chmod(self.key, 0o644)
        status, output = self.run_script(FakeEndpoint())
        self.assertEqual(status, 1)
        self.assertIn("chmod 600", output)

    def test_other_files_and_senders_are_refused(self):
        with open(self.key, "w", encoding="utf-8") as handle:
            json.dump({"type": "authorized_user", "client_email": SERVICE_ACCOUNT}, handle)
        status, output = self.run_script(FakeEndpoint())
        self.assertEqual(status, 1)
        self.assertIn("not a service account key file", output)

        self.key = write_key_file(self.directory)
        output = io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            status = self.script.main(["--service-account-key", self.key, "--sender", "not-an-address", "--connections-file", self.store])
        self.assertEqual(status, 1)


@unittest.skipUnless(shutil.which("openssl"), "needs the openssl command")
class SigningTest(unittest.TestCase):
    def test_openssl_signature_verifies_and_the_key_copy_is_removed(self):
        script = load_script("refresh-gmail-delegated-token.py")
        with tempfile.TemporaryDirectory() as directory:
            key_path = os.path.join(directory, "key.pem")
            subprocess.run(
                ["openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048", "-out", key_path],
                check=True, capture_output=True,
            )
            subprocess.run(["openssl", "pkey", "-in", key_path, "-pubout", "-out", key_path + ".pub"], check=True, capture_output=True)
            with open(key_path, encoding="utf-8") as handle:
                private_key = handle.read()
            self.assertTrue(private_key.startswith("-----BEGIN PRIVATE KEY-----"))

            scratch = os.path.join(directory, "scratch")
            os.mkdir(scratch)
            original_tempdir, tempfile.tempdir = tempfile.tempdir, scratch
            try:
                assertion = script.signed_assertion(
                    {"client_email": SERVICE_ACCOUNT, "private_key": private_key}, SENDER, datetime.now(timezone.utc)
                )
            finally:
                tempfile.tempdir = original_tempdir
            self.assertEqual(os.listdir(scratch), [], "the temporary key copy is removed")

            signing_input, signature = assertion.rsplit(".", 1)
            with open(os.path.join(directory, "signature"), "wb") as handle:
                handle.write(base64.urlsafe_b64decode(signature + "=" * (-len(signature) % 4)))
            verified = subprocess.run(
                ["openssl", "dgst", "-sha256", "-verify", key_path + ".pub", "-signature", os.path.join(directory, "signature")],
                input=signing_input.encode(), capture_output=True,
            )
            self.assertEqual(verified.returncode, 0, verified.stdout + verified.stderr)


if __name__ == "__main__":
    unittest.main()
