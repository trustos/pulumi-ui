#!/bin/bash
set -euo pipefail

# =============================================================================
# Cloud-init for pulumi-ui managed instances.
# Rendered by Go text/template — variables use .Vars.KEY syntax,
# application conditionals use if .Apps.KEY blocks.
# =============================================================================

# --- OS detection ---
check_os() {
  local name
  name=$(grep ^NAME= /etc/os-release | sed 's/"//g')
  local clean_name=${name#*=}
  if [[ "$clean_name" == "Ubuntu" ]]; then
    operating_system="ubuntu"
  elif [[ "$clean_name" == "Oracle Linux Server" ]]; then
    operating_system="oraclelinux"
  else
    operating_system="undef"
  fi
}

check_os

# --- OS setup (always) ---
setup_os() {
  echo "[Phase 0] OS setup..."
  if [[ "${operating_system:-}" == "ubuntu" ]]; then
    if ! iptables -t nat -L DOCKER -n >/dev/null 2>&1; then
      if [ -x /usr/sbin/netfilter-persistent ]; then
        /usr/sbin/netfilter-persistent stop || true
        /usr/sbin/netfilter-persistent flush || true
        systemctl stop netfilter-persistent.service 2>/dev/null || true
        systemctl disable netfilter-persistent.service 2>/dev/null || true
      fi
    fi

    # Wait for apt locks
    local apt_locks=("/var/lib/apt/lists/lock" "/var/lib/dpkg/lock" "/var/lib/dpkg/lock-frontend")
    local wait=0
    while true; do
      local locked=0
      for lock in "${apt_locks[@]}"; do
        if fuser "$lock" >/dev/null 2>&1; then locked=1; break; fi
      done
      [ $locked -eq 0 ] && break
      echo "Waiting for apt/dpkg locks..."
      sleep 2; wait=$((wait + 2))
      [ $wait -ge 120 ] && { echo "ERROR: apt locks not released"; exit 1; }
    done

    apt-get update
    for i in {1..60}; do
      apt-get install -y software-properties-common jq python3 python3-pip inotify-tools python3-venv pipx curl openssl wget unzip && break
      echo "apt-get install failed, retrying ($i/60)..."; sleep 5
    done

    pipx ensurepath || true
    pipx install oci-cli || true
    export PATH=$PATH:/root/.local/bin
    if ! command -v oci &>/dev/null; then
      python3 -m pip install --user oci-cli || true
      export PATH=$PATH:~/.local/bin
    fi

    echo "SystemMaxUse=100M" >> /etc/systemd/journald.conf
    echo "SystemMaxFileSize=100M" >> /etc/systemd/journald.conf
    systemctl restart systemd-journald

    export PATH=$PATH:/usr/local/bin:/usr/bin:~/.local/bin
  elif [[ "${operating_system:-}" == "oraclelinux" ]]; then
    systemctl disable --now firewalld || true
    echo '(allow iptables_t cgroup_t (dir (ioctl)))' > /root/local_iptables.cil
    semodule -i /root/local_iptables.cil || true
    dnf -y update
    dnf -y install jq wget unzip curl
    local major
    major=$(grep ^VERSION_ID= /etc/os-release | sed 's/"//g' | cut -d= -f2 | cut -d. -f1)
    if [[ $major -eq 9 ]]; then
      dnf -y install oraclelinux-developer-release-el9 python39-oci-cli
    else
      dnf -y install oraclelinux-developer-release-el8
      dnf -y module enable python36:3.6
      dnf -y install python36-oci-cli
    fi
    export PATH=$PATH:/usr/local/bin:/usr/bin:~/.local/bin
  fi

  if ! command -v oci &>/dev/null; then
    echo "ERROR: OCI CLI not installed"; exit 1
  fi
  echo "OCI CLI version: $(oci --version)"

  if grep -Eq "^[# ]*StrictHostKeyChecking" /etc/ssh/ssh_config; then
    sed -i 's/^[# ]*StrictHostKeyChecking.*/    StrictHostKeyChecking accept-new/' /etc/ssh/ssh_config
  else
    echo "    StrictHostKeyChecking accept-new" >> /etc/ssh/ssh_config
  fi
}

# --- Network wait ---
wait_for_network() {
  echo "Waiting for network..."
  for i in {1..120}; do
    if curl -sf --head http://www.google.com | head -n1 | grep -qE "HTTP/[12]"; then
      echo "Network is up."; return 0
    fi
    echo "Still waiting for network... ($i/120)"; sleep 3
  done
  echo "ERROR: network did not come up"; exit 1
}

# --- Variables ---
NOMAD_VERSION="{{ .Vars.NOMAD_VERSION }}"
CONSUL_VERSION="{{ .Vars.CONSUL_VERSION }}"
DATA_DIR="/opt/nomad/data"
CONSUL_DATA_DIR="/opt/consul/data"
LOG_LEVEL="INFO"
NOMAD_CLIENT_CPU="{{ .Vars.NOMAD_CLIENT_CPU }}"
NOMAD_CLIENT_MEMORY="{{ .Vars.NOMAD_CLIENT_MEMORY }}"
NOMAD_BOOTSTRAP_EXPECT={{ .Vars.NOMAD_BOOTSTRAP_EXPECT }}
NODE_COUNT={{ .Vars.NODE_COUNT }}

# --- IMDS discovery (called after setup_os + wait_for_network to ensure jq and network) ---
# OCI IMDS v2 /vnics/ does NOT return subnetId — only vnicId, privateIp, subnetCidrBlock.
# We get compartmentId from /instance/ and vnicId from /vnics/, then use the OCI CLI
# with instance_principal auth to resolve the VNIC's subnet OCID.
discover_imds() {
  local max_retries=20
  local delay=5
  for i in $(seq 1 $max_retries); do
    COMPARTMENT_OCID=$(curl -sf --max-time 10 -H "Authorization: Bearer Oracle" \
      http://169.254.169.254/opc/v2/instance/ | jq -r '.compartmentId // empty')
    local vnic_id
    vnic_id=$(curl -sf --max-time 10 -H "Authorization: Bearer Oracle" \
      http://169.254.169.254/opc/v2/vnics/ | jq -r '.[0].vnicId // empty')

    if [ -n "$COMPARTMENT_OCID" ] && [ -n "$vnic_id" ]; then
      echo "IMDS: compartment=$COMPARTMENT_OCID vnic=$vnic_id"
      # Resolve subnet OCID from VNIC via OCI CLI (requires instance_principal)
      export OCI_CLI_AUTH=instance_principal
      SUBNET_OCID=$(oci network vnic get --vnic-id "$vnic_id" 2>/dev/null \
        | jq -r '.data["subnet-id"] // empty')
      if [ -n "$SUBNET_OCID" ]; then
        echo "IMDS: subnet=$SUBNET_OCID"
        return 0
      fi
      echo "OCI CLI could not resolve subnet for VNIC $vnic_id (attempt $i/$max_retries)"
    else
      echo "IMDS not ready (attempt $i/$max_retries): compartment=${COMPARTMENT_OCID:-empty} vnic=${vnic_id:-empty}"
    fi
    sleep $delay
  done

  echo "ERROR: Could not fetch COMPARTMENT_OCID or SUBNET_OCID after $max_retries attempts."
  exit 1
}

# --- Peer IP discovery ---
discover_node_ips() {
  export OCI_CLI_AUTH=instance_principal
  local max_retries=60 retry_interval=10 attempt=1

  while [ $attempt -le $max_retries ]; do
    echo "Attempt $attempt/$max_retries: Querying for node IPs..."
    local private_ips
    private_ips=$(oci network private-ip list --subnet-id "$SUBNET_OCID" --all \
      | jq -r '.data[] | .["ip-address"]')
    local ip_count
    ip_count=$(echo "$private_ips" | grep -c '^')
    echo "Discovered $ip_count IPs: $private_ips"

    if [ "$ip_count" -eq "$NODE_COUNT" ]; then
      echo "Node IP count matches expected ($NODE_COUNT)."
      export ALL_NODE_IPS="$(echo "$private_ips" | xargs)"
      return 0
    fi

    if [ $attempt -eq $max_retries ]; then
      echo "Max retries reached."; exit 1
    fi
    echo "Waiting $retry_interval seconds..."; sleep $retry_interval
    attempt=$((attempt + 1))
  done
}

# --- Cluster role variables (set by template, empty for single-account) ---
CLUSTER_ROLE="{{ .Vars.role }}"
CLUSTER_JOIN_IP="{{ .Vars.primaryPrivateIp }}"
GOSSIP_KEY="{{ .Vars.gossipKey }}"
SERVER_MODE="{{ .Vars.serverMode }}"
CLUSTER_BOOTSTRAP_EXPECT="{{ .Vars.bootstrapExpect }}"

# --- Peer discovery + self-identification (called after discover_imds) ---
discover_peers() {
  SELF_PRIVATE_IP=$(hostname -I | awk '{print $1}')
  if [ -z "$SELF_PRIVATE_IP" ]; then
    echo "ERROR: Could not determine self private IP."; exit 1
  fi
  echo "Self private IP: $SELF_PRIVATE_IP"

  # Multi-account cluster: role-based join
  # Override bootstrap_expect with cluster-wide value if set
  if [ -n "$CLUSTER_BOOTSTRAP_EXPECT" ] && [ "$CLUSTER_BOOTSTRAP_EXPECT" -gt 0 ] 2>/dev/null; then
    NOMAD_BOOTSTRAP_EXPECT="$CLUSTER_BOOTSTRAP_EXPECT"
  fi

  if [ "$CLUSTER_ROLE" = "primary" ]; then
    if [ "$NOMAD_BOOTSTRAP_EXPECT" -gt 1 ]; then
      echo "Cluster role: PRIMARY (multi-node, expecting $NOMAD_BOOTSTRAP_EXPECT servers)"
      discover_node_ips
      NOMAD_IPS=$(echo "$ALL_NODE_IPS" | tr ' ' '\n' | jq -R . | jq -s .)
      FIRST_NODE_IP=$(echo "$ALL_NODE_IPS" | tr ' ' '\n' | sort | head -n1)
      IS_FIRST_NODE=false
      if [ "$SELF_PRIVATE_IP" = "$FIRST_NODE_IP" ]; then
        IS_FIRST_NODE=true
        echo "This is the first/bootstrapping node."
      fi
      # Write node_index for agent bootstrap cert selection (InstancePool).
      # Sort IPs and find own position — must match the order used by the server
      # when generating per-node Nebula certs.
      local sorted_ips
      sorted_ips=$(echo "$ALL_NODE_IPS" | tr ' ' '\n' | sort)
      local node_idx=0
      for ip in $sorted_ips; do
        if [ "$ip" = "$SELF_PRIVATE_IP" ]; then break; fi
        node_idx=$((node_idx + 1))
      done
      mkdir -p /etc/pulumi-ui-agent
      echo "$node_idx" > /etc/pulumi-ui-agent/node_index
      echo "Wrote node_index=$node_idx for agent cert selection"
    else
      echo "Cluster role: PRIMARY (single-node, self-bootstrap)"
      NOMAD_IPS="[\"$SELF_PRIVATE_IP\"]"
      IS_FIRST_NODE=true
      mkdir -p /etc/pulumi-ui-agent
      echo "0" > /etc/pulumi-ui-agent/node_index
    fi
    export NOMAD_IPS IS_FIRST_NODE SELF_PRIVATE_IP NOMAD_BOOTSTRAP_EXPECT
    return 0
  fi

  if [ "$CLUSTER_ROLE" = "worker" ] && [ -n "$CLUSTER_JOIN_IP" ]; then
    echo "Cluster role: WORKER (joining primary at $CLUSTER_JOIN_IP, serverMode=$SERVER_MODE)"
    NOMAD_IPS="[\"$CLUSTER_JOIN_IP\"]"
    IS_FIRST_NODE=false
    # Use cluster-wide bootstrap_expect if set, otherwise 1 (client-only default)
    if [ -n "$CLUSTER_BOOTSTRAP_EXPECT" ] && [ "$CLUSTER_BOOTSTRAP_EXPECT" -gt 0 ] 2>/dev/null; then
      NOMAD_BOOTSTRAP_EXPECT="$CLUSTER_BOOTSTRAP_EXPECT"
    else
      NOMAD_BOOTSTRAP_EXPECT=1
    fi
    # Worker with nodeCount=1: always node-0
    mkdir -p /etc/pulumi-ui-agent
    echo "0" > /etc/pulumi-ui-agent/node_index
    export NOMAD_IPS IS_FIRST_NODE SELF_PRIVATE_IP NOMAD_BOOTSTRAP_EXPECT
    return 0
  fi

  # Single-account mode: discover all nodes in same subnet
  discover_node_ips

  if [ -z "$ALL_NODE_IPS" ]; then
    NOMAD_IPS='["127.0.0.1"]'
  else
    NOMAD_IPS=$(echo "$ALL_NODE_IPS" | tr ' ' '\n' | jq -R . | jq -s .)
  fi
  echo "Nomad peer IPs: $NOMAD_IPS"

  FIRST_NODE_IP=""
  if [ -n "$ALL_NODE_IPS" ]; then
    FIRST_NODE_IP=$(echo "$ALL_NODE_IPS" | tr ' ' '\n' | sort | head -n1)
  fi
  IS_FIRST_NODE=false
  if [ "$SELF_PRIVATE_IP" = "$FIRST_NODE_IP" ]; then
    IS_FIRST_NODE=true
    echo "This is the first/bootstrapping node."
  fi

  export NOMAD_IPS IS_FIRST_NODE SELF_PRIVATE_IP
}

# =============================================================================
# Application installers (conditionally included by Go template)
# =============================================================================

{{ if .Apps.docker }}
# --- Docker ---
install_docker() {
  echo "[Phase 1] Installing Docker..."
  if command -v docker &>/dev/null; then
    echo "Docker already installed."; return 0
  fi
  for pkg in docker.io docker-doc docker-compose docker-compose-v2 podman-docker containerd runc; do
    apt-get remove -y $pkg 2>/dev/null || true
  done
  apt-get update
  apt-get install -y ca-certificates curl
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu \
    $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}") stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null
  apt-get update
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  systemctl enable --now docker
}
{{ end }}

{{ if .Apps.consul }}
# --- Consul ---
install_consul() {
  echo "[Phase 2] Installing Consul version $CONSUL_VERSION..."
  OS=$(uname -s | tr '[:upper:]' '[:lower:]')
  ARCH=$(uname -m)
  case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
  esac

  cd /tmp
  wget -q "https://releases.hashicorp.com/consul/${CONSUL_VERSION}/consul_${CONSUL_VERSION}_${OS}_${ARCH}.zip"
  unzip -o -q "consul_${CONSUL_VERSION}_${OS}_${ARCH}.zip"
  mv consul /usr/local/bin/
  chmod +x /usr/local/bin/consul
  rm -f "consul_${CONSUL_VERSION}_${OS}_${ARCH}.zip"

  useradd --system --home /etc/consul.d --shell /bin/false consul 2>/dev/null || true
  mkdir -p /etc/consul.d "$CONSUL_DATA_DIR"
  chown -R consul:consul /etc/consul.d "$CONSUL_DATA_DIR"

  # Determine server mode: use SERVER_MODE if set, else infer from CLUSTER_ROLE
  # Options: "server" (quorum only), "client" (workloads only), "server+client" (both)
  local effective_server_mode="$SERVER_MODE"
  if [ -z "$effective_server_mode" ]; then
    if [ "$CLUSTER_ROLE" = "worker" ]; then
      effective_server_mode="client"
    else
      effective_server_mode="server+client"
    fi
  fi

  local consul_server="false"
  local consul_bootstrap=""
  if [ "$effective_server_mode" = "server" ] || [ "$effective_server_mode" = "server+client" ]; then
    consul_server="true"
    consul_bootstrap="bootstrap_expect = $NOMAD_BOOTSTRAP_EXPECT"
  fi
  local consul_encrypt=""
  if [ -n "$GOSSIP_KEY" ]; then
    consul_encrypt="encrypt = \"$GOSSIP_KEY\""
  fi

  cat > /etc/consul.d/consul.hcl <<EOF
node_name  = "$(hostname)"
server     = $consul_server
datacenter = "dc1"
data_dir   = "$CONSUL_DATA_DIR"
log_level  = "$LOG_LEVEL"
retry_join = $NOMAD_IPS
bind_addr  = "$SELF_PRIVATE_IP"
client_addr = "0.0.0.0"
$consul_bootstrap
$consul_encrypt
ui = true

limits {
  kv_max_value_size = 10485760
}
EOF
  chown consul:consul /etc/consul.d/consul.hcl

  cat > /etc/systemd/system/consul.service <<EOF
[Unit]
Description=Consul Agent
Requires=network-online.target
After=network-online.target

[Service]
User=consul
Group=consul
ExecStart=/usr/local/bin/consul agent -config-dir=/etc/consul.d/
ExecReload=/bin/kill -HUP \$MAINPID
KillMode=process
Restart=on-failure
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable consul
  systemctl start consul

  echo "Waiting for Consul to be healthy..."
  for i in {1..180}; do
    if curl -s http://127.0.0.1:8500/v1/status/leader | grep -vq 'null'; then
      echo "Consul is up."; break
    fi
    sleep 2
  done
}

configure_docker_dns() {
  echo "Configuring DNS for Consul..."
  mkdir -p /etc/systemd/resolved.conf.d
  cat > /etc/systemd/resolved.conf.d/docker.conf <<EOF
[Resolve]
DNSStubListener=yes
DNSStubListenerExtra=172.17.0.1
EOF
  cat > /etc/systemd/resolved.conf.d/consul.conf <<EOF
[Resolve]
DNS=127.0.0.1:8600
Domains=~consul
EOF
  systemctl restart systemd-resolved
  mkdir -p /etc/docker
  cat > /etc/docker/daemon.json <<EOF
{
  "dns": ["172.17.0.1"]
}
EOF
  systemctl restart docker
}
{{ end }}

{{ if .Apps.nomad }}
# --- Nomad ---
create_nomad_user() {
  if id "nomad" &>/dev/null; then return; fi
  useradd -r -s /bin/false nomad
  if ! getent group docker > /dev/null; then groupadd docker; fi
  usermod -aG docker nomad
}

install_nomad() {
  echo "[Phase 3] Installing Nomad version $NOMAD_VERSION..."
  OS=$(uname -s | tr '[:upper:]' '[:lower:]')
  ARCH=$(uname -m)
  case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
  esac

  cd /tmp
  wget -q "https://releases.hashicorp.com/nomad/${NOMAD_VERSION}/nomad_${NOMAD_VERSION}_${OS}_${ARCH}.zip"
  unzip -o -q "nomad_${NOMAD_VERSION}_${OS}_${ARCH}.zip"
  mv nomad /usr/local/bin/
  chmod +x /usr/local/bin/nomad
  rm -f "nomad_${NOMAD_VERSION}_${OS}_${ARCH}.zip"
}

configure_nomad() {
  mkdir -p "$DATA_DIR" "$DATA_DIR/alloc" /etc/nomad.d
  chown nomad:nomad "$DATA_DIR" "$DATA_DIR/alloc" 2>/dev/null || true

  # Determine server mode: use SERVER_MODE if set, else infer from CLUSTER_ROLE
  # Options: "server" (quorum only), "client" (workloads only), "server+client" (both)
  local effective_server_mode="$SERVER_MODE"
  if [ -z "$effective_server_mode" ]; then
    if [ "$CLUSTER_ROLE" = "worker" ]; then
      effective_server_mode="client"
    else
      effective_server_mode="server+client"
    fi
  fi

  local nomad_server_enabled="false"
  local nomad_server_block=""
  if [ "$effective_server_mode" = "server" ] || [ "$effective_server_mode" = "server+client" ]; then
    nomad_server_enabled="true"
    local nomad_encrypt=""
    if [ -n "$GOSSIP_KEY" ]; then
      nomad_encrypt="encrypt = \"$GOSSIP_KEY\""
    fi
    nomad_server_block="server {
  enabled = true
  bootstrap_expect = ${NOMAD_BOOTSTRAP_EXPECT}
  retry_join = ${NOMAD_IPS}
  $nomad_encrypt
}"
  else
    nomad_server_block='server {
  enabled = false
}'
  fi

  local nomad_client_enabled="false"
  if [ "$effective_server_mode" = "client" ] || [ "$effective_server_mode" = "server+client" ]; then
    nomad_client_enabled="true"
  fi

  local nomad_client_servers=""
  if [ "$CLUSTER_ROLE" != "primary" ] && [ -n "$CLUSTER_JOIN_IP" ]; then
    nomad_client_servers="servers = [\"$CLUSTER_JOIN_IP:4647\"]"
  fi

  cat > /etc/nomad.d/nomad.hcl <<EOF
log_level = "$LOG_LEVEL"
data_dir = "$DATA_DIR"

acl {
  enabled    = true
  token_ttl  = "30s"
  policy_ttl = "60s"
  role_ttl   = "60s"
}

${nomad_server_block}

bind_addr = "0.0.0.0"

client {
  enabled = ${nomad_client_enabled}
  cpu_total_compute = ${NOMAD_CLIENT_CPU}
  memory_total_mb   = ${NOMAD_CLIENT_MEMORY}
  ${nomad_client_servers}
  meta {
    cluster_role = "${CLUSTER_ROLE}"
  }
}

plugin "docker" {
  config {
    volumes { enabled = true }
  }
}

plugin "raw_exec" {
  config { enabled = true }
}

limits {
  http_max_conns_per_client = 0
}
EOF
  chown nomad:nomad /etc/nomad.d/nomad.hcl 2>/dev/null || true

  cat > /etc/systemd/system/nomad.service <<EOF
[Unit]
Description=Nomad
Documentation=https://nomadproject.io/docs/
Wants=network-online.target
After=network-online.target

[Service]
User=root
Group=root
ExecStart=/usr/local/bin/nomad agent -config=/etc/nomad.d
ExecReload=/bin/kill -HUP \$MAINPID
KillMode=process
KillSignal=SIGINT
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF

  echo "$NOMAD_IPS" > /etc/nomad.d/NOMAD_IPS
  systemctl daemon-reload
  systemctl enable nomad
  systemctl start nomad
  echo "Nomad service started."
}

wait_for_nomad() {
  echo "Waiting for Nomad API..."
  for i in {1..36}; do
    if curl -s http://127.0.0.1:4646/v1/status/leader >/dev/null; then
      echo "Nomad API up."; return 0
    fi
    sleep 2
  done
  echo "Nomad API did not come up."; exit 1
}

wait_for_nomad_leader() {
  echo "Waiting for Nomad leader election..."
  for i in {1..60}; do
    leader=$(curl -s http://127.0.0.1:4646/v1/status/leader)
    if [[ -n "$leader" && "$leader" != "null" && "$leader" != "No cluster leader" ]]; then
      echo "Leader is: $leader"; return 0
    fi
    sleep 5
  done
  echo "Nomad leader election timed out."; return 1
}

setup_nomad_acl() {
  echo "Setting up Nomad ACLs..."
  wait_for_nomad

  # Wait for leader — retry with increasing patience. Without a leader,
  # bootstrap will fail. On multi-node clusters, quorum takes longer.
  local leader_ok=false
  for attempt in 1 2 3; do
    if wait_for_nomad_leader; then
      leader_ok=true
      break
    fi
    echo "Leader not elected yet (attempt $attempt/3), waiting 30s..."
    sleep 30
  done
  if [ "$leader_ok" != "true" ]; then
    echo "ERROR: Nomad leader never elected — skipping ACL bootstrap"
    echo "Run 'nomad acl bootstrap' manually once the cluster is stable"
    return 0
  fi

  if [ -f /etc/nomad.d/nomad-bootstrap-token ]; then
    # Already bootstrapped — verify token is valid
    local existing_token
    existing_token=$(jq -r .SecretID /etc/nomad.d/nomad-bootstrap-token 2>/dev/null || true)
    if [ -n "$existing_token" ] && [ "$existing_token" != "null" ]; then
      echo "Nomad ACL already bootstrapped."
      # Ensure it's in Consul KV
      if command -v consul &>/dev/null; then
        consul kv put nomad/bootstrap-token "$existing_token" 2>/dev/null || true
      fi
      return 0
    fi
    # File exists but is corrupt — remove and re-bootstrap
    rm -f /etc/nomad.d/nomad-bootstrap-token
  fi

  # Bootstrap with retries — the API may need a moment after leader election
  local bootstrap_ok=false
  for i in 1 2 3 4 5; do
    if nomad acl bootstrap -json > /etc/nomad.d/nomad-bootstrap-token.tmp 2>/dev/null; then
      mv /etc/nomad.d/nomad-bootstrap-token.tmp /etc/nomad.d/nomad-bootstrap-token
      chmod 600 /etc/nomad.d/nomad-bootstrap-token
      bootstrap_ok=true
      break
    fi
    echo "ACL bootstrap attempt $i/5 failed, retrying in 10s..."
    rm -f /etc/nomad.d/nomad-bootstrap-token.tmp
    sleep 10
  done

  if [ "$bootstrap_ok" != "true" ]; then
    echo "ERROR: Nomad ACL bootstrap failed after 5 attempts"
    echo "Run 'nomad acl bootstrap -json > /etc/nomad.d/nomad-bootstrap-token' manually"
    return 0
  fi

  # Verify token file is valid JSON with a SecretID
  local TOKEN
  TOKEN=$(jq -r .SecretID /etc/nomad.d/nomad-bootstrap-token 2>/dev/null || true)
  if [ -z "$TOKEN" ] || [ "$TOKEN" = "null" ]; then
    echo "ERROR: Bootstrap token file is invalid"
    rm -f /etc/nomad.d/nomad-bootstrap-token
    return 0
  fi

  # Store in Consul KV — retry to handle Consul leadership transitions
  if command -v consul &>/dev/null; then
    for i in 1 2 3; do
      if consul kv put nomad/bootstrap-token "$TOKEN" 2>/dev/null; then
        echo "Nomad bootstrap token stored in Consul KV"
        break
      fi
      echo "Consul KV write attempt $i/3 failed, retrying..."
      sleep 5
    done
  fi
  echo "Nomad ACL bootstrap complete."
}
{{ end }}

# =============================================================================
# Main execution
# =============================================================================

setup_os
wait_for_network
discover_imds
discover_peers

{{ if .Apps.docker }}
install_docker
{{ end }}

{{ if .Apps.consul }}
install_consul
configure_docker_dns
{{ end }}

{{ if .Apps.nomad }}
create_nomad_user
install_nomad
configure_nomad
{{ end }}

{{ if .Apps.nomad }}
if [ "$IS_FIRST_NODE" = true ]; then
  setup_nomad_acl
fi
{{ end }}

echo "Cloud-init complete."
exit 0
