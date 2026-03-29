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

      env {
        APP_ENV  = "production"
        APP_PORT = "13000"
        APP_KEY  = "[[.appKey]]"
        DB_DIALECT  = "postgres"
        DB_HOST     = "postgres.service.consul"
        DB_PORT     = "5432"
        DB_DATABASE = "[[.dbName]]"
        DB_USER     = "[[.dbUser]]"
        DB_PASSWORD = "[[.dbPassword]]"
      }

      service {
        name = "nocobase"
        port = "http"
        tags = [[if .domain]][
          "traefik.enable=true",
          "traefik.http.routers.nocobase.rule=Host(`[[.domain]]`)",
          "traefik.http.routers.nocobase.entrypoints=websecure",
          "traefik.http.routers.nocobase.tls=true",
          "traefik.http.routers.nocobase.tls.certresolver=letsencrypt",
        ][[else]][][[end]]
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
