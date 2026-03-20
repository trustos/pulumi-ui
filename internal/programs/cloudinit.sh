#!/bin/bash
#cloud-config

set -euo pipefail

# --- Check for failed Pulumi substitutions ---
check_substitutions() {
  echo "Checking for unsubstituted placeholders..."

  # Exclude validation lines with the marker before searching for placeholders
  local unsubstituted
  unsubstituted=$(grep -v 'PLACEHOLDER-CHECK' "$0" | grep -o '@@[^@]*@@' 2>/dev/null || true)
  if [ -n "$unsubstituted" ]; then
    echo "ERROR: Unsubstituted placeholder(s) detected in cloud-init script!"
    echo "The following placeholders were not replaced:"
    echo "$unsubstituted" | sort | uniq
    exit 1
  fi

  echo "All placeholders appear to be substituted correctly."
}

# Run the check early so substitution issues fail fast.
check_substitutions

# --- Consolidated OS detection, firewall, OCI CLI, and jq installation logic ---

check_os() {
  local name version clean_name clean_version major minor
  name=$(grep ^NAME= /etc/os-release | sed 's/"//g')
  clean_name=${name#*=}

  version=$(grep ^VERSION_ID= /etc/os-release | sed 's/"//g')
  clean_version=${version#*=}
  major=${clean_version%.*}
  minor=${clean_version#*.}

  if [[ "$clean_name" == "Ubuntu" ]]; then
    operating_system="ubuntu"
  elif [[ "$clean_name" == "Oracle Linux Server" ]]; then
    operating_system="oraclelinux"
  else
    operating_system="undef"
  fi
}

check_os

if [[ "${operating_system:-}" == "ubuntu" ]]; then
  echo "Canonical Ubuntu"
  if ! iptables -t nat -L DOCKER -n >/dev/null 2>&1; then
    echo "DOCKER chain not found, safe to flush netfilter-persistent."
    if [ -x /usr/sbin/netfilter-persistent ]; then
      /usr/sbin/netfilter-persistent stop
      /usr/sbin/netfilter-persistent flush
      systemctl stop netfilter-persistent.service
      systemctl disable netfilter-persistent.service
    fi
  else
    echo "DOCKER chain found, skipping netfilter-persistent flush to avoid breaking Docker networking."
  fi

  apt_locks=("/var/lib/apt/lists/lock" "/var/lib/dpkg/lock" "/var/lib/dpkg/lock-frontend")
  apt_lock_wait=0
  apt_lock_max_wait=120
  while true; do
    locked=0
    for lock in "${apt_locks[@]}"; do
      if fuser "$lock" >/dev/null 2>&1; then
        locked=1
        echo "Lock $lock held by:"
        fuser -v "$lock"
        break
      fi
    done
    if [ $locked -eq 0 ]; then
      break
    fi
    echo "Waiting for apt/dpkg locks to be released..."
    sleep 2
    apt_lock_wait=$((apt_lock_wait + 2))
    if [ $apt_lock_wait -ge $apt_lock_max_wait ]; then
      echo "ERROR: apt/dpkg locks not released after $apt_lock_max_wait seconds."
      exit 1
    fi
  done

  for i in {1..60}; do
    apt-get install -y software-properties-common jq python3 python3-pip inotify-tools python3-venv pipx curl openssl && break
    echo "apt-get install failed, retrying ($i/60)..."
    sleep 5
  done

  pipx ensurepath || true
  pipx install oci-cli || true
  export PATH=$PATH:/root/.local/bin

  if ! command -v oci &> /dev/null; then
    python3 -m pip install --user oci-cli
    export PATH=$PATH:~/.local/bin
  fi

  echo "SystemMaxUse=100M" >> /etc/systemd/journald.conf
  echo "SystemMaxFileSize=100M" >> /etc/systemd/journald.conf
  systemctl restart systemd-journald

  export PATH=$PATH:/usr/local/bin:/usr/bin:~/.local/bin
elif [[ "${operating_system:-}" == "oraclelinux" ]]; then
  echo "Oracle Linux"
  systemctl disable --now firewalld

  echo '(allow iptables_t cgroup_t (dir (ioctl)))' > /root/local_iptables.cil
  semodule -i /root/local_iptables.cil

  dnf -y update
  dnf -y install jq
  if [[ $major -eq 9 ]]; then
    dnf -y install oraclelinux-developer-release-el9
    dnf -y install python39-oci-cli curl
  else
    dnf -y install oraclelinux-developer-release-el8
    dnf -y module enable python36:3.6
    dnf -y install python36-oci-cli curl
  fi
  export PATH=$PATH:/usr/local/bin:/usr/bin:~/.local/bin
fi

if ! command -v oci &> /dev/null; then
  echo "ERROR: OCI CLI is not installed or not in PATH!"
  exit 1
fi

echo "OCI CLI version: $(oci --version)"

ensure_git() {
  if ! command -v git &>/dev/null; then
    echo "git not found. Installing git..."
    if command -v apt-get &>/dev/null; then
      apt-get update -y && apt-get install -y git
    elif command -v yum &>/dev/null; then
      yum install -y git
    elif command -v dnf &>/dev/null; then
      dnf install -y git
    else
      echo "Unsupported Linux distribution. Please install git manually."
      exit 1
    fi
  fi
}

# --- Static variables ---
NOMAD_VERSION="@@NOMAD_VERSION@@"
DATA_DIR="/opt/nomad/data"
CONSUL_DATA_DIR="/opt/consul/data"
LOG_LEVEL="INFO"

# Pulumi-substituted values
NOMAD_CLIENT_CPU="@@NOMAD_CLIENT_CPU@@"
NOMAD_CLIENT_MEMORY="@@NOMAD_CLIENT_MEMORY@@"
NOMAD_BOOTSTRAP_EXPECT=@@NOMAD_BOOTSTRAP_EXPECT@@
COMPARTMENT_OCID="@@COMPARTMENT_OCID@@"
SUBNET_OCID="@@SUBNET_OCID@@"

for var_name in "NOMAD_CLIENT_CPU" "NOMAD_CLIENT_MEMORY" "NOMAD_BOOTSTRAP_EXPECT" "COMPARTMENT_OCID" "SUBNET_OCID"; do
  var_value=$(eval echo $$var_name)
  if [[ "$var_value" =~ @@.+@@ ]]; then  # PLACEHOLDER-CHECK
    echo "ERROR: Variable substitution failed for $var_name: $var_value"
    exit 1
  fi
done
echo "Variable substitutions validated successfully."


# Run IP discovery


# --- Discover private IPs in subnet using OCI CLI ---
discover_node_ips() {
  export OCI_CLI_AUTH=instance_principal

  local compartment_ocid subnet_ocid bootstrap_expect
  compartment_ocid="$COMPARTMENT_OCID"
  subnet_ocid="$SUBNET_OCID"
  bootstrap_expect="$NOMAD_BOOTSTRAP_EXPECT"

  echo "Discovering node IPs in subnet $subnet_ocid with bootstrap expect $bootstrap_expect..."

  local max_retries=60
  local retry_interval=10  # seconds
  local attempt=1

  while [ $attempt -le $max_retries ]; do
    echo "Attempt $attempt/$max_retries: Querying for node IPs..."

    # Query OCI CLI for private IPs in the subnet with the required tag
    local private_ips
    private_ips=$(oci network private-ip list --subnet-id "$subnet_ocid" --all \
      | jq -r '.data[]
          | select(.["freeform-tags"] and .["freeform-tags"]["oci:compute:instanceconfiguration"])
          | .["ip-address"]')

    local ip_count
    ip_count=$(echo "$private_ips" | grep -c '^')

    echo "Discovered $ip_count IPs: $private_ips"

    if [ "$ip_count" -eq "$bootstrap_expect" ]; then
      echo "Bootstrap IP count matches expected ($bootstrap_expect). Proceeding..."
      # Export for later use
      export ALL_NODE_IPS="$(echo "$private_ips" | xargs)"
      return 0
    else
      echo "Expected $bootstrap_expect bootstrap IPs, but found $ip_count."

      if [ $attempt -eq $max_retries ]; then
        echo "Maximum retry attempts ($max_retries) reached. Exiting..."
        exit 1
      else
        echo "Waiting $retry_interval seconds before retry $((attempt + 1))..."
        sleep $retry_interval
      fi
    fi

    attempt=$((attempt + 1))
  done
}

discover_node_ips

# --- Metadata & discovery ---
# Use ALL_NODE_IPS provided by Pulumi for Nomad peer discovery

if [ -z "$ALL_NODE_IPS" ]; then
  echo "ERROR: ALL_NODE_IPS is empty!"
  NOMAD_IPS='["127.0.0.1"]'
else
  NOMAD_IPS=$(echo "$ALL_NODE_IPS" | tr ' ' '\n' | jq -R . | jq -s .)
fi

echo "Nomad peer IPs: $NOMAD_IPS"

# Optionally, get self private IP for logging or service config
SELF_PRIVATE_IP=$(hostname -I | awk '{print $1}')
if [ -z "$SELF_PRIVATE_IP" ] || [ "$SELF_PRIVATE_IP" = "null" ]; then
  echo "ERROR: Could not determine self private IP."
  exit 1
fi
echo "Self private IP: $SELF_PRIVATE_IP"

# Optionally, determine if this is the first node (by IP sort order)
FIRST_NODE_IP=""
if [ -n "$ALL_NODE_IPS" ]; then
  FIRST_NODE_IP=$(echo "$ALL_NODE_IPS" | tr ' ' '\n' | sort | head -n1)
fi
IS_FIRST_NODE=false
if [ -n "$SELF_PRIVATE_IP" ] && [ -n "$FIRST_NODE_IP" ] && [[ "$SELF_PRIVATE_IP" == "$FIRST_NODE_IP" ]]; then
  IS_FIRST_NODE=true
  echo "This is the first/bootstrapping node."
else
  echo "Not first node."
fi

export NOMAD_IPS
export IS_FIRST_NODE
export SELF_PRIVATE_IP

# --- Installer functions ---
install_nomad() {
  echo "Installing Nomad version $NOMAD_VERSION..."
  if command -v apt-get &>/dev/null; then
    apt-get update -y && apt-get install -y wget unzip
  elif command -v yum &>/dev/null; then
    yum install -y wget unzip
  elif command -v dnf &>/dev/null; then
    dnf install -y wget unzip
  else
    echo "Unsupported distribution for nomad prerequisites."
    exit 1
  fi

  OS=$(uname -s | tr '[:upper:]' '[:lower:]')
  ARCH=$(uname -m)
  case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64 | arm64) ARCH="arm64" ;;
    armv7l | armv6l | arm) ARCH="arm" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
  esac

  NOMAD_ZIP="nomad_${NOMAD_VERSION}_${OS}_${ARCH}.zip"
  NOMAD_URL="https://releases.hashicorp.com/nomad/${NOMAD_VERSION}/${NOMAD_ZIP}"

  cd /tmp
  echo "Downloading $NOMAD_URL"
  wget -q "$NOMAD_URL"
  unzip -o -q "$NOMAD_ZIP"
  mv nomad /usr/local/bin/
  chmod +x /usr/local/bin/nomad
  echo "Nomad installation complete."
}

install_docker() {
  for pkg in docker.io docker-doc docker-compose docker-compose-v2 podman-docker containerd runc; do
    apt-get remove -y $pkg || true
  done
  apt-get update
  apt-get install -y ca-certificates curl
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc

  echo \
    "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu \
    $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}") stable" | \
    tee /etc/apt/sources.list.d/docker.list > /dev/null
  apt-get update
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  systemctl enable --now docker
}

configure_nomad() {
  echo "Configuring Nomad..."
  mkdir -p "$DATA_DIR"
  mkdir -p "$DATA_DIR/alloc"
  chown nomad:nomad "$DATA_DIR" || true
  chown nomad:nomad "$DATA_DIR/alloc" || true

  cat > /etc/nomad.d/nomad.hcl <<EOF
log_level = "$LOG_LEVEL"
data_dir = "$DATA_DIR"

acl {
  enabled    = true
  token_ttl  = "30s"
  policy_ttl = "60s"
  role_ttl   = "60s"
}

server {
  enabled = true
  bootstrap_expect = ${NOMAD_BOOTSTRAP_EXPECT}
  retry_join = ${NOMAD_IPS}
}

bind_addr = "0.0.0.0"

client {
  enabled = true
  cpu_total_compute = ${NOMAD_CLIENT_CPU}
  memory_total_mb   = ${NOMAD_CLIENT_MEMORY}

  host_volume "shared-data" {
    path      = "/mnt/glusterfs"
    read_only = false
  }
}

plugin "docker" {
  config {
    volumes {
      enabled = true
    }
  }
}

plugin "raw_exec" {
  config {
    enabled = true
  }
}
EOF

  chown nomad:nomad /etc/nomad.d/nomad.hcl || true
  echo "Nomad configuration complete."
}

create_nomad_user() {
  echo "Creating nomad user..."
  if id "nomad" &>/dev/null; then
    echo "User 'nomad' already exists."
  else
    useradd -r -s /bin/false nomad
  fi

  if ! getent group docker > /dev/null; then
    groupadd docker
    if systemctl is-active --quiet docker; then
      systemctl restart docker
    fi
  fi

  usermod -aG docker nomad
  echo "Nomad user configured."
}

wait_for_nomad() {
  echo "Waiting for Nomad API..."
  for i in {1..36}; do
    if curl -s http://127.0.0.1:4646/v1/status/leader >/dev/null; then
      echo "Nomad API up."
      return 0
    fi
    sleep 2
  done
  echo "Nomad API did not come up."
  exit 1
}

wait_for_nomad_leader() {
  echo "Waiting for Nomad leader election..."
  for i in {1..60}; do
    leader=$(curl -s http://127.0.0.1:4646/v1/status/leader)
    if [[ -n "$leader" && "$leader" != "null" && "$leader" != "No cluster leader" ]]; then
      echo "Leader is: $leader"
      return 0
    fi
    sleep 5
  done
  echo "Nomad cluster leader election timed out."
  return 1
}

setup_glusterfs_peers_and_volume() {
  local GLUSTER_MOUNT_BRICK="$GLUSTER_MOUNT/data"
  local VOLUME_NAME="gv0"

  # Install glusterfs if missing
  if ! command -v gluster &>/dev/null; then
    if [[ "${operating_system}" == "ubuntu" ]]; then
      apt-get install -y glusterfs-server glusterfs-client
    elif [[ "${operating_system}" == "oraclelinux" ]]; then
      dnf install -y glusterfs-server glusterfs
    fi
  fi

  mkdir -p "$GLUSTER_MOUNT_BRICK"
  chown -R gluster:gluster "$(dirname "$GLUSTER_MOUNT_BRICK")"

  systemctl enable --now glusterd

  # Ensure glusterd starts before network mounts
  systemctl add-wants multi-user.target glusterd.service

  echo "Waiting for glusterd to be active..."
  for i in {1..60}; do
    if systemctl is-active --quiet glusterd; then
      break
    fi
    sleep 2
  done
  if ! systemctl is-active --quiet glusterd; then
    echo "ERROR: glusterd failed to become active."
    exit 1
  fi

  echo "Probing GlusterFS peers (excluding self: $SELF_PRIVATE_IP)..."
  readarray -t ips < <(echo "$NOMAD_IPS" | jq -r '.[]')
  for ip in "${ips[@]}"; do
    if [[ "$ip" == "$SELF_PRIVATE_IP" ]]; then
      continue
    fi
    if ! gluster peer status | grep -q "$ip"; then
      echo "Probing peer $ip..."
      for attempt in {1..60}; do
        gluster peer probe "$ip" && break
        echo "Peer probe to $ip failed (attempt $attempt), retrying..."
        sleep 5
      done
    else
      echo "Already aware of peer $ip."
    fi
  done

  total_nodes=${#ips[@]}
  expected_other_peers=$(( total_nodes - 1 ))
  echo "Expecting $expected_other_peers other connected peers."

  local connected_peers=0
  for i in {1..60}; do
    connected_peers=$(gluster peer status | grep -c "Peer in Cluster (Connected)")
    if [ "$connected_peers" -ge "$expected_other_peers" ]; then
      echo "All peers connected ($connected_peers/$expected_other_peers)."
      break
    fi
    echo "Waiting for peers to connect ($connected_peers/$expected_other_peers)..."
    sleep 5
  done

  if [ "$connected_peers" -lt "$expected_other_peers" ]; then
    echo "ERROR: Timeout waiting for GlusterFS peer mesh."
    gluster peer status
    exit 1
  fi

  if [ "$IS_FIRST_NODE" = true ]; then
    echo "Bootstrap node: attempting volume creation."

    exec 200>/var/lock/gluster_volume_creation.lock
    flock -n 200 || {
      echo "Another creator in progress, waiting for lock..."
      flock 200
    }

    if ! gluster volume info "$VOLUME_NAME" >/dev/null 2>&1; then
      replica_count=${NOMAD_BOOTSTRAP_EXPECT}
      if (( replica_count > total_nodes )); then
        echo "Adjusting replica count from $replica_count to $total_nodes."
        replica_count=$total_nodes
      fi

      BRICKS=()
      readarray -t ips < <(echo "$NOMAD_IPS" | jq -r '.[]')
      for ip in "${ips[@]}"; do
        BRICKS+=("${ip}:${GLUSTER_MOUNT_BRICK}")
      done

      echo "Creating GlusterFS volume $VOLUME_NAME (replica $replica_count) with bricks: ${BRICKS[*]}"
      gluster volume create "$VOLUME_NAME" replica "$replica_count" "${BRICKS[@]}" force
      if [ $? -ne 0 ]; then
        echo "ERROR: Volume creation failed."
        exit 1
      fi

      gluster volume start "$VOLUME_NAME"
      if [ $? -ne 0 ]; then
        echo "ERROR: Failed to start volume."
        exit 1
      fi
    else
      echo "Volume $VOLUME_NAME already exists; ensuring it's started."
      if ! gluster volume info "$VOLUME_NAME" | grep -q "Status: Started"; then
        gluster volume start "$VOLUME_NAME" || true
      fi
    fi
  else
    echo "Non-bootstrap node: skipping volume creation."
  fi
}

mount_glusterfs_with_retry() {
  mkdir -p /mnt/glusterfs
  local FIRST_GLUSTER_IP
  FIRST_GLUSTER_IP=$(echo "$NOMAD_IPS" | jq -r '.[0]')
  local VOLUME_NAME="gv0"

  if [ -z "$FIRST_GLUSTER_IP" ] || [ "$FIRST_GLUSTER_IP" == "null" ]; then
    echo "ERROR: Could not determine first Gluster IP; skipping mount."
    return 1
  fi

  echo "Waiting for GlusterFS volume '$VOLUME_NAME' to report 'Started'..."
  for ((i = 1; i <= 36; i++)); do
    if gluster volume info "$VOLUME_NAME" 2>&1 | grep -q "Status: Started"; then
      echo "Volume is started."
      break
    fi
    echo "Attempt $i waiting for volume to start..."
    sleep 10
  done

  if ! gluster volume info "$VOLUME_NAME" 2>&1 | grep -q "Status: Started"; then
    echo "ERROR: Volume did not start in time."
    exit 1
  fi

  local BACKUP_SERVERS
  BACKUP_SERVERS=$(echo "$NOMAD_IPS" | jq -r '. | join(":")')
  local GLUSTERFS_MOUNT_LINE="${FIRST_GLUSTER_IP}:/${VOLUME_NAME} /mnt/glusterfs glusterfs defaults,_netdev,x-systemd.automount,backup-volfile-servers=${BACKUP_SERVERS},nofail 0 0"

  if ! grep -q "/${VOLUME_NAME} /mnt/glusterfs glusterfs" /etc/fstab; then
    echo "Adding GlusterFS mount to /etc/fstab..."
    echo "$GLUSTERFS_MOUNT_LINE" >> /etc/fstab
  fi

  if mountpoint -q /mnt/glusterfs && [ -d /mnt/glusterfs ] && [ "$(ls -A /mnt/glusterfs 2>/dev/null)" ]; then
    echo "Already mounted and functional."
    return 0
  fi

  if mountpoint -q /mnt/glusterfs || grep -q "/mnt/glusterfs" /proc/mounts; then
    echo "Stale mount, unmounting..."
    umount -l /mnt/glusterfs 2>/dev/null || true
    sleep 2
  fi

  for ((i = 1; i <= 12; i++)); do
    if mount /mnt/glusterfs; then
      echo "Mounted GlusterFS."
      if systemctl is-active --quiet nomad || systemctl list-unit-files nomad.service &>/dev/null; then
        systemctl restart nomad || true
      fi
      return 0
    fi
    echo "Mount attempt $i failed; retrying..."
    sleep 10
  done

  echo "ERROR: Failed to mount GlusterFS after retries."
  exit 1
}

configure_docker_dns() {
  echo "Configuring systemd-resolved and Docker for Consul DNS..."
  mkdir -p /etc/systemd/resolved.conf.d

  cat >/etc/systemd/resolved.conf.d/docker.conf <<EOF
[Resolve]
DNSStubListener=yes
DNSStubListenerExtra=172.17.0.1
EOF

  cat >/etc/systemd/resolved.conf.d/consul.conf <<EOF
[Resolve]
DNS=127.0.0.1:8600
Domains=~consul
EOF

  systemctl restart systemd-resolved

  mkdir -p /etc/docker
  cat >/etc/docker/daemon.json <<EOF
{
  "dns": ["172.17.0.1"]
}
EOF

  systemctl restart docker
}

setup_nomad_acl() {
  echo "Setting up Nomad ACLs and tokens for nomad-ops..."
  if ! command -v jq &>/dev/null; then
    if command -v apt-get &>/dev/null; then
      apt-get update -y && apt-get install -y jq
    elif command -v yum &>/dev/null; then
      yum install -y jq
    elif command -v dnf &>/dev/null; then
      dnf install -y jq
    else
      echo "Please install jq manually."
      exit 1
    fi
  fi

  wait_for_nomad
  wait_for_nomad_leader

  if [ ! -f /etc/nomad.d/nomad-bootstrap-token ]; then
    nomad acl bootstrap -json > /etc/nomad.d/nomad-bootstrap-token
    mkdir -p /mnt/glusterfs/nomad/
    cp /etc/nomad.d/nomad-bootstrap-token /mnt/glusterfs/nomad/nomad-bootstrap-token

    if ! consul kv get nomad/bootstrap-token >/dev/null 2>&1; then
      TOKEN=$(jq -r .SecretID /mnt/glusterfs/nomad/nomad-bootstrap-token)
      consul kv put nomad/bootstrap-token "$TOKEN"
    fi
  fi

  MGMT_TOKEN=$(jq -r .SecretID /etc/nomad.d/nomad-bootstrap-token)

  POLICY_PATH="/opt/nomad-server/nomad-ops/acl/acl.hcl"
  NOMAD_TOKEN=$MGMT_TOKEN nomad acl policy apply nomad-ops-superuser "$POLICY_PATH"

  NOMAD_TOKEN=$MGMT_TOKEN nomad acl token create -name="nomad-ops" -policy="nomad-ops-superuser" -json > /etc/nomad.d/nomad-ops-token
  NOMAD_OPS_TOKEN=$(jq -r .SecretID /etc/nomad.d/nomad-ops-token)

  echo -n "$NOMAD_OPS_TOKEN" > /etc/nomad.d/nomad_token
  chmod 600 /etc/nomad.d/nomad_token
  chown nomad:nomad /etc/nomad.d/nomad_token || true

  echo "Nomad ACL setup complete."
}

install_consul() {
    echo "Installing Consul..."
    if command -v apt-get &>/dev/null; then
      apt-get update -y && apt-get install -y wget unzip
    elif command -v yum &>/dev/null; then
      yum install -y wget unzip
    elif command -v dnf &>/dev/null; then
      dnf install -y wget unzip
    else
      echo "Unsupported distribution for nomad prerequisites."
      exit 1
    fi
  CONSUL_VERSION="@@CONSUL_VERSION@@"
  CONSUL_DATACENTER="${CONSUL_DATACENTER:-dc1}"
  CONSUL_SERVER_ENABLED="${CONSUL_SERVER_ENABLED:-true}"
  CONSUL_BOOTSTRAP_EXPECT="${NOMAD_BOOTSTRAP_EXPECT:-3}"
  CONSUL_RETRY_JOIN="${NOMAD_IPS:-[\"127.0.0.1\"]}"
  CONSUL_NODE_NAME="$(hostname)"
  CONSUL_LOG_LEVEL="${LOG_LEVEL:-INFO}"

  OS=$(uname -s | tr '[:upper:]' '[:lower:]')
  ARCH=$(uname -m)
  case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64 | arm64) ARCH="arm64" ;;
    armv7l | armv6l | arm) ARCH="arm" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
  esac

  CONSUL_ZIP="consul_${CONSUL_VERSION}_${OS}_${ARCH}.zip"
  CONSUL_URL="https://releases.hashicorp.com/consul/${CONSUL_VERSION}/${CONSUL_ZIP}"

  cd /tmp
  echo "Downloading Consul from $CONSUL_URL"
  wget -q "$CONSUL_URL"
  unzip -o -q "$CONSUL_ZIP"
  mv consul /usr/local/bin/
  chmod +x /usr/local/bin/consul
  rm "$CONSUL_ZIP"

  useradd --system --home /etc/consul.d --shell /bin/false consul || true
  mkdir -p /etc/consul.d "$CONSUL_DATA_DIR"
  chown -R consul:consul /etc/consul.d "$CONSUL_DATA_DIR"

  cat <<EOF > /etc/consul.d/consul.hcl
node_name  = "${CONSUL_NODE_NAME}"
server     = ${CONSUL_SERVER_ENABLED}
datacenter = "${CONSUL_DATACENTER}"
data_dir   = "${CONSUL_DATA_DIR}"
log_level  = "${CONSUL_LOG_LEVEL}"
retry_join = ${CONSUL_RETRY_JOIN}
bind_addr  = "${SELF_PRIVATE_IP}"
client_addr = "0.0.0.0"
bootstrap_expect = ${CONSUL_BOOTSTRAP_EXPECT}
ui = true
EOF
  chown consul:consul /etc/consul.d/consul.hcl || true

  cat <<EOF > /etc/systemd/system/consul.service
[Unit]
Description=Consul Agent
Requires=network-online.target
After=network-online.target

[Service]
User=consul
Group=consul
ExecStart=/usr/local/bin/consul agent -config-dir=/etc/consul.d/
ExecReload=/bin/kill -HUP $MAINPID
KillMode=process
Restart=on-failure
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable consul
  systemctl start consul
}

install_nomad_ops() {
  echo "Installing nomad-ops from source..."
  wait_for_nomad
  export NOMAD_TOKEN
  NOMAD_TOKEN=$(cat /etc/nomad.d/nomad_token)

  if ! NOMAD_TOKEN=$MGMT_TOKEN nomad namespace status nomad-ops &>/dev/null; then
    NOMAD_TOKEN=$MGMT_TOKEN nomad namespace apply nomad-ops
  fi

  nomad job run /opt/nomad-server/nomad-ops/job/nomad-ops.nomad.hcl
  echo "nomad-ops job started."
}

install_zerotier() {
  echo "Installing ZeroTier One on node..."

  if ! command -v zerotier-one >/dev/null 2>&1; then
    echo "Installing ZeroTier..."
    curl -s https://install.zerotier.com | bash
  else
    echo "ZeroTier already installed"
  fi

  NETWORK_ID="35c192ce9b9bd219"
  if command -v zerotier-cli >/dev/null 2>&1; then
    if ! zerotier-cli listnetworks | grep -q "$NETWORK_ID"; then
      echo "Joining ZeroTier network $NETWORK_ID"
      zerotier-cli join "$NETWORK_ID"
      echo "ZeroTier network joined. Manual approval may be required in the ZeroTier admin console."
    else
      echo "Already joined to ZeroTier network $NETWORK_ID"
    fi
  else
    echo "zerotier-cli not found, install may have failed."
    exit 1
  fi

  sysctl -w net.ipv4.ip_forward=1
  grep -q "net.ipv4.ip_forward=1" /etc/sysctl.conf || echo "net.ipv4.ip_forward=1" >> /etc/sysctl.conf

  echo "ZeroTier installation complete."
}

setup_traefik_acl() {
  echo "Setting up Nomad ACLs and tokens for Traefik..."
  MGMT_TOKEN=$(jq -r .SecretID /etc/nomad.d/nomad-bootstrap-token)
  POLICY_PATH="/opt/nomad-server/traefik/acl/acl.hcl"

  NOMAD_TOKEN=$MGMT_TOKEN nomad acl policy apply traefik-read-policy "$POLICY_PATH"
  NOMAD_TOKEN=$MGMT_TOKEN nomad acl token create -name="traefik" -policy="traefik-read-policy" -json > /etc/nomad.d/traefik-token
  TRAEFIK_TOKEN=$(jq -r .SecretID /etc/nomad.d/traefik-token)

  if ! consul kv get nomad/traefik-token >/dev/null 2>&1; then
    consul kv put nomad/traefik-token "$TRAEFIK_TOKEN"
  fi

  echo "Traefik ACL setup complete."
}

setup_traefik() {
  export MGMT_TOKEN=$(jq -r .SecretID /etc/nomad.d/nomad-bootstrap-token)
  export TRAEFIK_TOKEN=$(jq -r .SecretID /etc/nomad.d/traefik-token)
  NAMESPACE="traefik"
  if ! NOMAD_TOKEN=$MGMT_TOKEN nomad namespace status -namespace=$NAMESPACE &>/dev/null; then
    NOMAD_TOKEN=$MGMT_TOKEN nomad namespace apply -description "Traefik reverse proxy namespace." $NAMESPACE
  fi

  VOLUME_PATH="/mnt/glusterfs/traefik"
  mkdir -p "$VOLUME_PATH"
  cp -r /opt/nomad-server/traefik/configs/. "$VOLUME_PATH"

  NOMAD_TOKEN=$MGMT_TOKEN nomad job run -namespace=traefik /opt/nomad-server/traefik/job/traefik.nomad.hcl
  NOMAD_TOKEN=$MGMT_TOKEN nomad job restart -on-error=fail -namespace=traefik traefik
  echo "Traefik job started."
}

setup_zerotier_bridge() {
  export MGMT_TOKEN=$(jq -r .SecretID /etc/nomad.d/nomad-bootstrap-token)
  echo "Setting up ZeroTier NAT bridge job..."
  NOMAD_TOKEN=$MGMT_TOKEN nomad job run /opt/nomad-server/zerotier-bridge/job/zerotier-bridge.nomad.hcl
  echo "ZeroTier bridge job started."
}

setup_postgres() {
  echo "Setting up PostgreSQL stack..."
  wait_for_nomad
  wait_for_nomad_leader

  export MGMT_TOKEN=$(jq -r .SecretID /etc/nomad.d/nomad-bootstrap-token)

  NAMESPACE="postgres"
  if ! NOMAD_TOKEN=$MGMT_TOKEN nomad namespace status $NAMESPACE &>/dev/null; then
    NOMAD_TOKEN=$MGMT_TOKEN nomad namespace apply -description "PostgreSQL database namespace." $NAMESPACE
  fi

  NOMAD_TOKEN=$MGMT_TOKEN nomad job run -namespace=postgres /opt/nomad-server/postgres/job/postgres.nomad.hcl
  echo "PostgreSQL job started."

  NOMAD_TOKEN=$MGMT_TOKEN nomad job run -namespace=postgres /opt/nomad-server/postgres/job/backup/postgres-backup.nomad.hcl
  echo "PostgreSQL backup job started."
}

# --- Bootstrap execution ---

echo "Waiting for network..."
for i in {1..120}; do
  if curl -s --head http://www.google.com | head -n1 | grep -E "HTTP/1\.[01] [23].." >/dev/null; then
    echo "Network is up."
    break
  fi
  echo "Still waiting for network... ($i/120)"
  sleep 3
done

if ! command -v docker &>/dev/null; then
  echo "Docker missing; installing."
  install_docker
else
  echo "Docker already installed."
fi

if grep -Eq "^[# ]*StrictHostKeyChecking" /etc/ssh/ssh_config; then
    sed -i 's/^[# ]*StrictHostKeyChecking.*/    StrictHostKeyChecking accept-new/' /etc/ssh/ssh_config
else
    echo "    StrictHostKeyChecking accept-new" >> /etc/ssh/ssh_config
fi

GLUSTER_MOUNT="/mnt/gluster-brick"
GLUSTER_DEV=""
timeout=86400
elapsed=0

echo "Listing block devices for GlusterFS detection:"
lsblk

while [ $elapsed -lt $timeout ]; do
  for dev in /dev/sd[b-z] /dev/vd[b-z] /dev/oracleoci/oraclevdb; do
    if [ -b "$dev" ]; then
      GLUSTER_DEV="$dev"
      break 2
    fi
  done
  echo "Waiting for GlusterFS block device..."
  sleep 10
  elapsed=$((elapsed + 10))
done

if [ -z "$GLUSTER_DEV" ]; then
  echo "ERROR: No GlusterFS block device found."
  lsblk
  exit 1
fi

echo "Found block device: $GLUSTER_DEV"

if ! blkid | grep -q "$GLUSTER_DEV"; then
  echo "Formatting $GLUSTER_DEV as ext4..."
  mkfs.ext4 "$GLUSTER_DEV"
fi

mkdir -p "$GLUSTER_MOUNT"
if ! mount | grep -q "$GLUSTER_MOUNT"; then
  mount "$GLUSTER_DEV" "$GLUSTER_MOUNT"
fi

if ! grep -q "$GLUSTER_DEV" /etc/fstab; then
  echo "$GLUSTER_DEV $GLUSTER_MOUNT ext4 defaults,nofail 0 2" >> /etc/fstab
fi

ensure_git

echo "Starting Consul setup..."
install_consul
configure_docker_dns

for i in {1..180}; do
  if curl -s http://127.0.0.1:8500/v1/status/leader | grep -vq 'null'; then
    echo "Consul is up."
    break
  fi
  echo "Waiting for Consul to be healthy..."
  sleep 2
done

echo "Starting Nomad setup..."

create_nomad_user
install_nomad

install_zerotier

if [ ! -d "/opt/nomad-server" ]; then
  git clone https://github.com/trustos/nomad-server.git /opt/nomad-server
fi

setup_glusterfs_peers_and_volume

mount_glusterfs_with_retry

mkdir -p /etc/nomad.d
configure_nomad

cat > /etc/systemd/system/nomad.service <<EOF
[Unit]
Description=Nomad
Documentation=https://nomadproject.io/docs/
Wants=network-online.target glusterd.service
After=network-online.target glusterd.service
RequiresMountsFor=/mnt/glusterfs

[Service]
User=root
Group=root
ExecStart=/usr/local/bin/nomad agent -config=/etc/nomad.d
ExecReload=/bin/kill -HUP $MAINPID
KillMode=process
KillSignal=SIGINT
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF

export NOMAD_IPS

echo "$NOMAD_IPS" > /etc/nomad.d/NOMAD_IPS

cat > /etc/systemd/system/glusterfs-mount-check.service <<EOF
[Unit]
Description=Ensure GlusterFS mount is available
After=glusterd.service mnt-glusterfs.mount network-online.target
Wants=mnt-glusterfs.mount
Before=nomad.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'if mountpoint -q /mnt/glusterfs && timeout 10 ls /mnt/glusterfs >/dev/null 2>&1; then echo "GlusterFS mount is healthy"; else echo "GlusterFS mount not ready"; exit 1; fi'

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable glusterfs-mount-check.service
systemctl enable nomad
systemctl start nomad

echo "Nomad service started."

if [ "$IS_FIRST_NODE" = true ]; then
  setup_nomad_acl
  setup_traefik_acl
  setup_traefik
  setup_zerotier_bridge
  setup_postgres
  install_nomad_ops
fi

echo "Setup complete."
exit 0