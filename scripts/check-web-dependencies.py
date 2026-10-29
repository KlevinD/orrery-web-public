#!/usr/bin/env python3
"""Check the actual Go server dependency graph for retired runtime dependencies."""
from pathlib import Path
import json
import subprocess

ROOT = Path(__file__).resolve().parent.parent
deps = subprocess.check_output(['go', 'list', '-deps', './cmd/server'], cwd=ROOT/'orrery_v2', text=True).splitlines()
retired = ('github.com/cloudwego/eino', 'github.com/eino-contrib/', 'github.com/redis/', 'github.com/iOfficeAI/AionCore', 'github.com/multica-ai/')
found = [name for name in deps if any(name == prefix or name.startswith(prefix.rstrip('/') + '/') for prefix in retired)]
print(json.dumps({'serverPackages': len(deps), 'retiredDependencies': found}, indent=2))
if found:
    raise SystemExit(1)
