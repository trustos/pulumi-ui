job "traefik" {
  datacenters = ["dc1"]
  type        = "service"

[[- if gt (atoi (or .instances "1")) 1 ]]

  constraint {
    operator = "distinct_hosts"
    value    = "true"
  }

  # ═══ LEADER: handles ACME challenges, syncs certs to Consul KV ═══
  group "traefik-leader" {
    count = 1

    network {
      port "http"  { static = 80 }
      port "https" { static = 443 }
      port "api"   { static = 8080 }
    }

    task "init-dirs" {
      driver = "raw_exec"
      lifecycle { hook = "prestart" }
      config {
        command = "/bin/bash"
        args    = ["-c", "mkdir -p /opt/traefik/acme /opt/traefik/dynamic && chmod 700 /opt/traefik/acme"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "init-acme-redirect" {
      driver = "raw_exec"
      lifecycle { hook = "prestart" }
      config {
        command = "/bin/bash"
        args = ["-c", <<-SCRIPT
cat > /opt/traefik/dynamic/acme-redirect.yaml <<'REDIR'
http:
  routers:
    acme-challenge-redirect:
      rule: "PathPrefix(`/.well-known/acme-challenge/`)"
      entryPoints: [web]
      priority: 10000
      service: acme-leader-forward
  services:
    acme-leader-forward:
      loadBalancer:
        servers:
          - url: "http://traefik-leader.service.consul:80"
REDIR
SCRIPT
        ]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "traefik" {
      driver = "docker"

      template {
        data = <<EOH
TRAEFIK_TOKEN={{ key "nomad/bootstrap-token" }}
EOH
        destination = "secrets/env"
        env         = true
      }

      template {
        data = <<EOF
entryPoints:
  web:
    address: ":80"
  websecure:
    address: ":443"
  traefik:
    address: ":8080"

ping:
  entryPoint: web

log:
  level: INFO

api:
  dashboard: true
  insecure: true

providers:
  providersThrottleDuration: 1s
  file:
    directory: "/etc/traefik/dynamic"
    watch: true
  nomad:
    endpoint:
      address: "http://localhost:4646"
      token: {{ key "nomad/bootstrap-token" }}
    watch: true
    namespaces:
      - "default"
  consulCatalog:
    endpoint:
      address: "localhost:8500"
    watch: true

certificatesResolvers:
  letsencrypt:
    acme:
      email: [[.acmeEmail]]
      storage: /etc/traefik/acme/acme.json
      httpChallenge:
        entryPoint: web
EOF
        destination = "local/traefik.yaml"
        change_mode = "restart"
      }

      config {
        image        = "traefik:v3.6.13"
        network_mode = "host"
        ports        = ["http", "https", "api"]
        mounts = [
          { type = "bind", source = "local/traefik.yaml", target = "/etc/traefik/traefik.yaml", readonly = true },
          { type = "bind", source = "/opt/traefik/acme",    target = "/etc/traefik/acme",         readonly = false },
          { type = "bind", source = "/opt/traefik/dynamic", target = "/etc/traefik/dynamic",      readonly = false },
        ]
      }

      service {
        name = "traefik-leader"
        port = "http"
        check {
          type     = "http"
          path     = "/ping"
          interval = "10s"
          timeout  = "2s"
        }
      }

      resources {
        cpu    = 500
        memory = 256
      }
    }

    task "acme-to-consul" {
      driver = "raw_exec"

      lifecycle {
        hook    = "poststart"
        sidecar = true
      }

      config {
        command = "/bin/bash"
        args = ["-c", <<-SCRIPT
export PATH="/usr/local/bin:/usr/bin:/bin:$PATH"
LAST_HASH=""
while true; do
  if [ -f /opt/traefik/acme/acme.json ]; then
    NEW_HASH=$(md5sum /opt/traefik/acme/acme.json | awk '{print $1}')
    if [ "$LAST_HASH" != "$NEW_HASH" ]; then
      consul kv put traefik/acme-json @/opt/traefik/acme/acme.json
      LAST_HASH="$NEW_HASH"
      echo "Synced acme.json to Consul KV (hash: $NEW_HASH)"
    fi
  fi
  sleep 10
done
SCRIPT
        ]
      }

      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "kv-dynamic-config" {
      driver = "raw_exec"

      lifecycle {
        hook    = "poststart"
        sidecar = true
      }

      template {
        data        = "{{ key \"traefik/dynamic-config\" }}"
        destination = "/opt/traefik/dynamic/kv-dynamic.yaml"
        change_mode = "noop"
      }

      config {
        command = "/bin/bash"
        args    = ["-c", "sleep infinity"]
      }

      resources {
        cpu    = 50
        memory = 32
      }
    }
  }

  # ═══ FOLLOWERS: read certs from Consul KV, redirect ACME to leader ═══
  group "traefik-follower" {
    count = [[ sub (atoi .instances) 1 ]]

    network {
      port "http"  { static = 80 }
      port "https" { static = 443 }
      port "api"   { static = 8080 }
    }

    task "init-dirs" {
      driver = "raw_exec"
      lifecycle { hook = "prestart" }
      config {
        command = "/bin/bash"
        args    = ["-c", "mkdir -p /opt/traefik/acme /opt/traefik/dynamic && chmod 600 /opt/traefik/acme"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "init-acme-redirect" {
      driver = "raw_exec"
      lifecycle { hook = "prestart" }
      config {
        command = "/bin/bash"
        args = ["-c", <<-SCRIPT
cat > /opt/traefik/dynamic/acme-redirect.yaml <<'REDIR'
http:
  routers:
    acme-challenge-redirect:
      rule: "PathPrefix(`/.well-known/acme-challenge/`)"
      entryPoints: [web]
      priority: 10000
      service: acme-leader-forward
  services:
    acme-leader-forward:
      loadBalancer:
        servers:
          - url: "http://traefik-leader.service.consul:80"
REDIR
SCRIPT
        ]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "consul-to-acme" {
      driver = "raw_exec"

      lifecycle {
        hook    = "prestart"
        sidecar = true
      }

      config {
        command = "/bin/bash"
        args = ["-c", <<-SCRIPT
export PATH="/usr/local/bin:/usr/bin:/bin:$PATH"
# Initial sync: fetch cert if it already exists in Consul KV
consul kv get traefik/acme-json > /opt/traefik/acme/acme.json 2>/dev/null || echo '{}' > /opt/traefik/acme/acme.json
chmod 600 /opt/traefik/acme/acme.json
echo "Watching Consul KV for ACME cert updates..."
LAST_HASH=""
while true; do
  CERT=$(consul kv get traefik/acme-json 2>/dev/null || true)
  if [ -n "$CERT" ]; then
    NEW_HASH=$(echo "$CERT" | md5sum | awk '{print $1}')
    if [ "$LAST_HASH" != "$NEW_HASH" ]; then
      echo "$CERT" > /opt/traefik/acme/acme.json
      chmod 600 /opt/traefik/acme/acme.json
      LAST_HASH="$NEW_HASH"
      echo "Cert synced from Consul KV (hash: $NEW_HASH)"
    fi
  fi
  sleep 30
done
SCRIPT
        ]
      }

      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "traefik" {
      driver = "docker"

      template {
        data = <<EOH
TRAEFIK_TOKEN={{ key "nomad/bootstrap-token" }}
EOH
        destination = "secrets/env"
        env         = true
      }

      template {
        data = <<EOF
entryPoints:
  web:
    address: ":80"
  websecure:
    address: ":443"
  traefik:
    address: ":8080"

ping:
  entryPoint: web

log:
  level: INFO

api:
  dashboard: true
  insecure: true

providers:
  providersThrottleDuration: 1s
  file:
    directory: "/etc/traefik/dynamic"
    watch: true
  nomad:
    endpoint:
      address: "http://localhost:4646"
      token: {{ key "nomad/bootstrap-token" }}
    watch: true
    namespaces:
      - "default"
  consulCatalog:
    endpoint:
      address: "localhost:8500"
    watch: true

certificatesResolvers:
  letsencrypt:
    acme:
      email: [[.acmeEmail]]
      storage: /etc/traefik/acme/acme.json
      httpChallenge:
        entryPoint: web
EOF
        destination = "local/traefik.yaml"
        change_mode = "restart"
      }

      config {
        image        = "traefik:v3.6.13"
        network_mode = "host"
        ports        = ["http", "https", "api"]
        mounts = [
          { type = "bind", source = "local/traefik.yaml", target = "/etc/traefik/traefik.yaml", readonly = true },
          { type = "bind", source = "/opt/traefik/acme",    target = "/etc/traefik/acme",         readonly = false },
          { type = "bind", source = "/opt/traefik/dynamic", target = "/etc/traefik/dynamic",      readonly = false },
        ]
      }

      service {
        name = "traefik-follower"
        port = "http"
        check {
          type     = "http"
          path     = "/ping"
          interval = "10s"
          timeout  = "2s"
        }
      }

      resources {
        cpu    = 500
        memory = 256
      }
    }

    task "kv-dynamic-config" {
      driver = "raw_exec"

      lifecycle {
        hook    = "poststart"
        sidecar = true
      }

      template {
        data        = "{{ key \"traefik/dynamic-config\" }}"
        destination = "/opt/traefik/dynamic/kv-dynamic.yaml"
        change_mode = "noop"
      }

      config {
        command = "/bin/bash"
        args    = ["-c", "sleep infinity"]
      }

      resources {
        cpu    = 50
        memory = 32
      }
    }
  }

[[- else ]]

  # ═══ SINGLE INSTANCE (default) ═══
  group "traefik" {
    count = 1

    network {
      port "http"  { static = 80 }
      port "https" { static = 443 }
      port "api"   { static = 8080 }
    }

    task "init-dirs" {
      driver = "raw_exec"
      lifecycle { hook = "prestart" }
      config {
        command = "/bin/bash"
        args    = ["-c", "mkdir -p /opt/traefik/acme /opt/traefik/dynamic && chmod 600 /opt/traefik/acme"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "traefik" {
      driver = "docker"

      template {
        data = <<EOH
TRAEFIK_TOKEN={{ key "nomad/bootstrap-token" }}
EOH
        destination = "secrets/env"
        env         = true
      }

      template {
        data = <<EOF
entryPoints:
  web:
    address: ":80"
  websecure:
    address: ":443"
  traefik:
    address: ":8080"

ping:
  entryPoint: web

log:
  level: INFO

api:
  dashboard: true
  insecure: true

providers:
  providersThrottleDuration: 1s
  file:
    directory: "/etc/traefik/dynamic"
    watch: true
  nomad:
    endpoint:
      address: "http://localhost:4646"
      token: {{ key "nomad/bootstrap-token" }}
    watch: true
    namespaces:
      - "default"
  consulCatalog:
    endpoint:
      address: "localhost:8500"
    watch: true

certificatesResolvers:
  letsencrypt:
    acme:
      email: [[.acmeEmail]]
      storage: /etc/traefik/acme/acme.json
      httpChallenge:
        entryPoint: web
EOF
        destination = "local/traefik.yaml"
        change_mode = "restart"
      }

      config {
        image        = "traefik:v3.6.13"
        network_mode = "host"
        ports        = ["http", "https", "api"]
        mounts = [
          { type = "bind", source = "local/traefik.yaml", target = "/etc/traefik/traefik.yaml", readonly = true },
          { type = "bind", source = "/opt/traefik/acme",    target = "/etc/traefik/acme",         readonly = false },
          { type = "bind", source = "/opt/traefik/dynamic", target = "/etc/traefik/dynamic",      readonly = false },
        ]
      }

      service {
        name = "traefik"
        port = "http"
        check {
          type     = "http"
          path     = "/ping"
          interval = "10s"
          timeout  = "2s"
        }
      }

      resources {
        cpu    = 500
        memory = 256
      }
    }

    task "kv-dynamic-config" {
      driver = "raw_exec"

      lifecycle {
        hook    = "poststart"
        sidecar = true
      }

      template {
        data        = "{{ key \"traefik/dynamic-config\" }}"
        destination = "/opt/traefik/dynamic/kv-dynamic.yaml"
        change_mode = "noop"
      }

      config {
        command = "/bin/bash"
        args    = ["-c", "sleep infinity"]
      }

      resources {
        cpu    = 50
        memory = 32
      }
    }
  }

[[- end ]]
}
