#!/bin/bash
set -e

NAMESPACE="default"
VAR_PATH="nomad/jobs/pulumi-ui"

echo "=== Pulumi UI — Bootstrap ==="
echo ""
echo "This script:"
echo "  1. Generates a random encryption key"
echo "  2. Stores it in Nomad Variables at $VAR_PATH"
echo "  3. The Nomad job reads this key at startup via nomadVar template"
echo ""
echo "All other credentials (OCI, SSH, Pulumi passphrase) are"
echo "configured through the web UI after first deploy."
echo ""

# Check if the variable already exists
if nomad var get -namespace "$NAMESPACE" "$VAR_PATH" > /dev/null 2>&1; then
  echo "Nomad Variable already exists at $VAR_PATH — skipping."
else
  echo "Generating new encryption key..."
  ENCRYPTION_KEY=$(openssl rand -hex 32)

  # Write to Nomad Variables (key=value format via stdin)
  printf '%s' "encryption_key=$ENCRYPTION_KEY" \
    | nomad var put -namespace "$NAMESPACE" -in=env "$VAR_PATH" -

  echo "Encryption key stored in Nomad Variable: $VAR_PATH"
  echo ""
  echo "IMPORTANT: Back up this key. If lost, credentials stored in"
  echo "the SQLite database cannot be recovered."
  echo ""
  echo "To read it back:"
  echo "  nomad var get -namespace $NAMESPACE $VAR_PATH"
fi

echo "Bootstrap complete. Deploy with:"
echo "  nomad job run deploy/nomad/pulumi-ui.nomad.hcl"
echo ""
echo "Then open the UI and go to Settings to configure OCI credentials."
