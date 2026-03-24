#!/bin/bash
set -euo pipefail
# PULUMI_UI_AGENT_BOOTSTRAP
# Standalone Nebula mesh + pulumi-ui agent installer.
# Injected automatically by the engine into every compute instance whose
# program implements ApplicationProvider or AgentAccessProvider.
# Uses @@PLACEHOLDER@@ markers replaced at injection time (not Go templates).

# --- OS detection ---
check_os_agent() {
  local name
  name=$(grep ^NAME= /etc/os-release | sed 's/"//g')
  local clean_name=${name#*=}
  if [[ "$clean_name" == "Ubuntu" ]]; then
    agent_os="ubuntu"
  elif [[ "$clean_name" == "Oracle Linux Server" ]]; then
    agent_os="oraclelinux"
  else
    agent_os="unknown"
  fi
}
check_os_agent

# --- Architecture detection ---
ARCH=$(uname -m)
case "$ARCH" in
  aarch64) AGENT_ARCH="arm64" ;;
  x86_64)  AGENT_ARCH="amd64" ;;
  *) echo "Unsupported arch: $ARCH"; exit 1 ;;
esac

# --- Nebula mesh ---
install_nebula() {
  echo "[agent-bootstrap] Installing Nebula mesh..."
  mkdir -p /etc/nebula

  NEBULA_VERSION="@@AGENT_VERSION@@"
  if [ "$NEBULA_VERSION" = "latest" ]; then
    NEBULA_VERSION="v1.10.3"
  fi

  echo "[agent-bootstrap] Downloading Nebula ${NEBULA_VERSION} for ${AGENT_ARCH}..."
  curl -fsSL "https://github.com/slackhq/nebula/releases/download/${NEBULA_VERSION}/nebula-linux-${AGENT_ARCH}.tar.gz" \
    | tar xz -C /usr/local/bin nebula nebula-cert
  chmod +x /usr/local/bin/nebula /usr/local/bin/nebula-cert

  cat > /etc/nebula/ca.crt <<'NEBULA_CA'
@@NEBULA_CA_CERT@@
NEBULA_CA

  cat > /etc/nebula/host.crt <<'NEBULA_HOST_CERT'
@@NEBULA_HOST_CERT@@
NEBULA_HOST_CERT

  cat > /etc/nebula/host.key <<'NEBULA_HOST_KEY'
@@NEBULA_HOST_KEY@@
NEBULA_HOST_KEY

  chmod 600 /etc/nebula/host.key

  cat > /etc/nebula/config.yml <<EOF
pki:
  ca: /etc/nebula/ca.crt
  cert: /etc/nebula/host.crt
  key: /etc/nebula/host.key

static_host_map: {}

lighthouse:
  am_lighthouse: false
  hosts: []

listen:
  host: 0.0.0.0
  port: 41820

punchy:
  punch: true

tun:
  disabled: false
  dev: nebula1

firewall:
  outbound:
    - port: any
      proto: any
      host: any
  inbound:
    - port: 41820
      proto: tcp
      group: server
    - port: any
      proto: icmp
      host: any
EOF

  cat > /etc/systemd/system/nebula.service <<EOF
[Unit]
Description=Nebula Mesh Network
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/nebula -config /etc/nebula/config.yml
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable nebula
  systemctl start nebula
  echo "[agent-bootstrap] Nebula mesh started."
}

# --- pulumi-ui agent ---
install_agent() {
  echo "[agent-bootstrap] Installing pulumi-ui agent..."

  AGENT_URL="@@AGENT_DOWNLOAD_URL@@"
  if [ -z "$AGENT_URL" ]; then
    AGENT_VERSION="@@AGENT_VERSION@@"
    if [ "$AGENT_VERSION" = "latest" ]; then
      AGENT_VERSION="v1.10.3"
    fi
    AGENT_URL="https://github.com/trustos/pulumi-ui/releases/download/${AGENT_VERSION}/agent_linux_${AGENT_ARCH}"
  fi

  curl -fsSL "$AGENT_URL" -o /usr/local/bin/pulumi-ui-agent
  chmod +x /usr/local/bin/pulumi-ui-agent

  mkdir -p /etc/pulumi-ui-agent
  echo '@@AGENT_TOKEN@@' > /etc/pulumi-ui-agent/token
  chmod 600 /etc/pulumi-ui-agent/token

  cat > /etc/systemd/system/pulumi-ui-agent.service <<EOF
[Unit]
Description=pulumi-ui Agent
After=network-online.target nebula.service
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/pulumi-ui-agent --listen :41820 --token-file /etc/pulumi-ui-agent/token
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable pulumi-ui-agent
  systemctl start pulumi-ui-agent
  echo "[agent-bootstrap] pulumi-ui agent started."
}

install_nebula
install_agent
echo "[agent-bootstrap] Complete."
