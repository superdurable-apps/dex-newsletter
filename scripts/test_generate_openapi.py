import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("generate-openapi.sh")


class GenerateOpenAPITest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        for directory in (
            "scripts",
            "tools/openapi",
            "openapi",
            "web/src/api/generated",
            "internal/api/generated",
            "fake-bin",
        ):
            (self.root / directory).mkdir(parents=True, exist_ok=True)
        shutil.copy2(SCRIPT, self.root / "scripts/generate-openapi.sh")
        (self.root / "scripts/generate-openapi.sh").chmod(0o755)
        (self.root / "openapi/openapi.yaml").write_text("openapi: 3.0.3\n")
        (self.root / "internal/api/generated/old.go").write_text("old go\n")
        (self.root / "web/src/api/generated/old.ts").write_text("old ts\n")
        self._write_executable(
            "go",
            """#!/usr/bin/env bash
set -euo pipefail
if [[ "${FAIL_GO:-}" == "true" ]]; then exit 19; fi
while (( $# > 0 )); do
  if [[ "$1" == "--target" ]]; then target="$2"; break; fi
  shift
done
mkdir -p "$target"
printf 'new go\n' > "$target/generated.go"
""",
        )
        self._write_executable(
            "npm",
            """#!/usr/bin/env bash
set -euo pipefail
if [[ "${FAIL_NPM:-}" == "true" ]]; then exit 23; fi
mkdir -p "$OPENAPI_OUTPUT"
printf 'new ts\n' > "$OPENAPI_OUTPUT/generated.ts"
""",
        )

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_replaces_both_outputs_after_success(self) -> None:
        self._run()
        self.assertEqual(
            (self.root / "internal/api/generated/generated.go").read_text(),
            "new go\n",
        )
        self.assertEqual(
            (self.root / "web/src/api/generated/generated.ts").read_text(),
            "new ts\n",
        )
        self.assertFalse((self.root / "internal/api/generated/old.go").exists())
        self.assertFalse((self.root / "web/src/api/generated/old.ts").exists())

    def test_preserves_both_outputs_when_go_generation_fails(self) -> None:
        with self.assertRaises(subprocess.CalledProcessError):
            self._run(FAIL_GO="true")
        self._assert_old_outputs()

    def test_preserves_both_outputs_when_typescript_generation_fails(self) -> None:
        with self.assertRaises(subprocess.CalledProcessError):
            self._run(FAIL_NPM="true")
        self._assert_old_outputs()

    def _write_executable(self, name: str, contents: str) -> None:
        path = self.root / "fake-bin" / name
        path.write_text(contents)
        path.chmod(0o755)

    def _run(self, **overrides: str) -> None:
        environment = os.environ.copy()
        environment.update(overrides)
        environment["PATH"] = f"{self.root / 'fake-bin'}:{environment['PATH']}"
        subprocess.run(
            [str(self.root / "scripts/generate-openapi.sh")],
            cwd=self.root,
            env=environment,
            check=True,
            capture_output=True,
            text=True,
        )

    def _assert_old_outputs(self) -> None:
        self.assertEqual(
            (self.root / "internal/api/generated/old.go").read_text(),
            "old go\n",
        )
        self.assertEqual(
            (self.root / "web/src/api/generated/old.ts").read_text(),
            "old ts\n",
        )


if __name__ == "__main__":
    unittest.main()
