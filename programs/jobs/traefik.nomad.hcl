job "traefik" {
  datacenters = ["dc1"]
  type        = "service"

  group "traefik" {
    count = 1

    network {
      port "http" {
        static = 80
      }
      port "https" {
        static = 443
      }
    }

    task "init-dirs" {
      driver = "raw_exec"
      lifecycle {
        hook = "prestart"
      }
      config {
        command = "bash"
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

ping:
  entryPoint: web

log:
  level: INFO

api:
  dashboard: true
  insecure: false

providers:
  providersThrottleDuration: 1s
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
        image        = "traefik:v3.4"
        network_mode = "host"
        ports        = ["http", "https"]
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
  }
}
