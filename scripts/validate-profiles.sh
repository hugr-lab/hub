#!/usr/bin/env bash
# Validate profiles.json — run from admin workspace before saving changes.
# Usage: bash /home/jovyan/hub-config/scripts/validate-profiles.sh

set -euo pipefail

PROFILES="${1:-/home/jovyan/hub-config/profiles.json}"

if [ ! -f "$PROFILES" ]; then
    echo "ERROR: File not found: $PROFILES"
    exit 1
fi

# JSON syntax check
if ! python3 -c "import json; json.load(open('$PROFILES'))" 2>/dev/null; then
    echo "ERROR: Invalid JSON syntax"
    python3 -c "import json; json.load(open('$PROFILES'))"
    exit 1
fi

# Schema validation
python3 -c "
import json, sys
sys.path.insert(0, '/srv/jupyterhub')
from hub_profiles.schema import validate

with open('$PROFILES') as f:
    config = json.load(f)

errors = validate(config)
if errors:
    print('VALIDATION ERRORS:')
    for e in errors:
        print(f'  - {e}')
    sys.exit(1)
else:
    profiles = list(config.get('profiles', {}).keys())
    print(f'OK: {len(profiles)} profiles ({", ".join(profiles)})')
    print(f'Default: {config.get(\"default_profile\")}')
    role_map = config.get('role_map', {})
    if role_map:
        print(f'Role map: {role_map}')
"
