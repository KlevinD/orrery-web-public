"""Check manual gate exit codes without running deployment or a Go test suite."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPTS = Path(__file__).resolve().parent
POWERSHELL = shutil.which("powershell") or shutil.which("pwsh")
BASH = os.environ.get("ORRERY_GATE_TEST_BASH")
if BASH is None and os.name != "nt":
    BASH = shutil.which("bash")


class ManualGateExitTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.native_go = None
        if os.name == "nt" and POWERSHELL:
            go = shutil.which("go")
            if go is None:
                raise unittest.SkipTest("Go is needed to build the native Windows fixture")
            cls.fixture = tempfile.TemporaryDirectory(prefix="orrery-gate-native-")
            cls.addClassCleanup(cls.fixture.cleanup)
            source = Path(cls.fixture.name) / "main.go"
            source.write_text(
                'package main\nimport ("fmt"; "os"; "strconv"; "strings")\n'
                'func main() {\n'
                'fmt.Println("GATE_ARGS:" + strings.Join(os.Args[1:], " "))\n'
                'cwd, err := os.Getwd(); if err != nil { panic(err) }; fmt.Println("GATE_CWD:" + cwd)\n'
                'code, err := strconv.Atoi(os.Getenv("ORRERY_TEST_GATE_EXIT")); if err != nil { panic(err) }; os.Exit(code)\n}\n',
                encoding="ascii", newline="\n",
            )
            cls.native_go = Path(cls.fixture.name) / "go.exe"
            subprocess.run([go, "build", "-o", str(cls.native_go), str(source)], cwd=cls.fixture.name, check=True, capture_output=True, timeout=180)

    def run_gate(self, shell, exit_code, missing_go=False):
        with tempfile.TemporaryDirectory(prefix="orrery-gate-test-") as folder:
            directory = Path(folder)
            env = dict(os.environ, ORRERY_TEST_GATE_EXIT=str(exit_code))
            # PowerShell resolves only our fixture Go tool; Bash also needs dirname.
            env["PATH"] = folder if shell == "powershell" else folder + os.pathsep + env["PATH"]
            if not missing_go:
                if shell == "powershell" and os.name == "nt":
                    shutil.copyfile(self.native_go, directory / "go.exe")
                else:
                    shim = directory / "go"
                    shim.write_text(
                        '#!/bin/sh\nprintf "GATE_ARGS:%s\\n" "$*"\nprintf "GATE_CWD:%s\\n" "$PWD"\nexit "$ORRERY_TEST_GATE_EXIT"\n',
                        encoding="ascii", newline="\n",
                    )
                    shim.chmod(0o755)
            if shell == "powershell":
                command = [POWERSHELL, "-NoProfile", "-NonInteractive", "-File", str(SCRIPTS / "check-context-graph-production-gate.ps1")]
            else:
                command = [BASH, str(SCRIPTS / "check-context-graph-production-gate.sh")]
            result = subprocess.run(command, cwd=directory, env=env, capture_output=True, text=True, timeout=30)
            if missing_go:
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertNotIn("GATE_ARGS:", result.stdout)
                return
            self.assertEqual(result.returncode, exit_code, result.stdout + result.stderr)
            self.assertIn("GATE_ARGS:test ./internal/app/ontology -run TestProductionCoverage(Gate|AuditReport|Required0618Modules|ExemptionReport) -v -count=1", result.stdout)
            cwd = next(line for line in result.stdout.splitlines() if line.startswith("GATE_CWD:"))
            self.assertTrue(cwd.replace("\\", "/").endswith("/orrery_v2"), cwd)

    @unittest.skipUnless(POWERSHELL, "PowerShell is not installed")
    def test_powershell_success(self):
        self.run_gate("powershell", 0)

    @unittest.skipUnless(POWERSHELL, "PowerShell is not installed")
    def test_powershell_preserves_failure_exit_code(self):
        self.run_gate("powershell", 7)

    @unittest.skipUnless(POWERSHELL, "PowerShell is not installed")
    def test_powershell_missing_go_fails(self):
        self.run_gate("powershell", 1, missing_go=True)

    @unittest.skipUnless(BASH, "Set ORRERY_GATE_TEST_BASH to a native Git Bash path on Windows")
    def test_bash_success(self):
        self.run_gate("bash", 0)

    @unittest.skipUnless(BASH, "Set ORRERY_GATE_TEST_BASH to a native Git Bash path on Windows")
    def test_bash_preserves_failure_exit_code(self):
        self.run_gate("bash", 7)


if __name__ == "__main__":
    unittest.main()
