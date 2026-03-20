job "pulumi-ui" {
  namespace   = "default"
  datacenters = ["dc1"]
  type        = "service"

  group "app" {
    count = 1

    # Ensure local data directory exists on the host before the container starts
    task "init-dir" {
      driver = "raw_exec"
      lifecycle { hook = "prestart" }
      config {
        command = "mkdir"
        args    = ["-p", "/opt/pulumi-ui/state"]
      }
      resources { cpu = 50; memory = 32 }
    }

    network {
      port "http" { to = 8080 }
    }

    service {
      name = "pulumi-ui"
      port = "http"
      tags = [
        "traefik.enable=true",
        "traefik.http.routers.pulumi-ui.rule=Host(`pulumi.<your-domain>`)",
        "traefik.http.routers.pulumi-ui.entrypoints=websecure",
        "traefik.http.routers.pulumi-ui.tls=true",
      ]
      check {
        type     = "http"
        path     = "/api/settings/health"
        interval = "15s"
        timeout  = "3s"
      }
    }

    task "server" {
      driver = "docker"

      # Read encryption key from Nomad Variables (no Consul dependency)
      template {
        data        = <<EOH
{{ with nomadVar "nomad/jobs/pulumi-ui" -}}
PULUMI_UI_ENCRYPTION_KEY={{ .encryption_key }}
{{- end }}
EOH
        destination = "secrets/env"
        env         = true
      }

      config {
        image = "<your-registry>/pulumi-ui:latest"
        ports = ["http"]
        mounts = [
          {
            type     = "bind"
            source   = "/opt/pulumi-ui"
            target   = "/data"
            readonly = false
          }
        ]
      }

      env {
        PULUMI_UI_DATA_DIR  = "/data"
        PULUMI_UI_STATE_DIR = "/data/state"
        PULUMI_UI_ADDR      = ":8080"
      }

      resources {
        cpu    = 500
        memory = 512
      }
    }
  }
}
