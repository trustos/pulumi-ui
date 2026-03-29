job "nocobase" {
  datacenters = ["dc1"]
  type        = "service"

  group "nocobase" {
    count = 1

    network {
      port "http" { to = 13000 }
    }

    task "init-dirs" {
      driver = "raw_exec"
      lifecycle {
        hook = "prestart"
      }
      config {
        command = "mkdir"
        args    = ["-p", "/opt/nocobase/storage"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "nocobase" {
      driver = "docker"

      config {
        image = "nocobase/nocobase:latest"
        ports = ["http"]
        mounts = [
          { type = "bind", source = "/opt/nocobase/storage", target = "/app/nocobase/storage", readonly = false },
        ]
      }

      template {
        data = <<EOH
APP_ENV=production
APP_PORT=13000
APP_KEY=[[.appKey]]
DB_DIALECT=postgres
DB_HOST=postgres.service.consul
DB_PORT=5432
DB_DATABASE=[[.dbName]]
DB_USER={{ key "postgres/adminuser" }}
DB_PASSWORD={{ key "postgres/adminpassword" }}
EOH
        destination = "secrets/env"
        env         = true
      }

      service {
        name = "nocobase"
        port = "http"
        tags = [
          "traefik.enable=true",
          "traefik.http.routers.nocobase.rule=HostRegexp(`.*`)",
          "traefik.http.routers.nocobase.entrypoints=web",
          "traefik.http.routers.nocobase.priority=1",
        ]
        check {
          type     = "http"
          path     = "/api/__health"
          interval = "15s"
          timeout  = "5s"
        }
      }

      resources {
        cpu    = 1000
        memory = 1024
      }
    }
  }
}
