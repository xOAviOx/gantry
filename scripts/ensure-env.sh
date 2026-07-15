#!/usr/bin/env bash
# Create deploy/.env from the example on first run, with a real generated
# AES master key. Idempotent: does nothing if deploy/.env already exists.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ -f deploy/.env ]; then
  exit 0
fi

cp deploy/.env.example deploy/.env

KEY="$(node -e "console.log(require('crypto').randomBytes(32).toString('base64'))")"
# Portable in-place edit (GNU sed on Git Bash / Linux).
sed -i "s|REPLACE_WITH_BASE64_32_BYTES|${KEY}|" deploy/.env

echo "created deploy/.env with a generated GANTRY_MASTER_KEY"
