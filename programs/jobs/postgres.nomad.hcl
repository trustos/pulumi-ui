job "postgres" {
  datacenters = ["dc1"]
  type        = "service"

  group "postgres" {
    count = 1

    network {
      port "db" {
        static = 5432
      }
    }

    task "init-dirs" {
      driver = "raw_exec"
      lifecycle {
        hook = "prestart"
      }
      config {
        command = "bash"
        args    = ["-c", "mkdir -p /opt/postgres/data && chown -R 999:999 /opt/postgres/data"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "init-secrets" {
      driver = "raw_exec"
      lifecycle {
        hook = "prestart"
      }
      config {
        command = "bash"
        args = ["-c", <<-EOT
          set -e
          if ! consul kv get postgres/adminuser > /dev/null 2>&1; then
            consul kv put postgres/adminuser "[[.dbUser]]"
          fi
          if ! consul kv get postgres/adminpassword > /dev/null 2>&1; then
            pw=$(openssl rand -base64 24)
            consul kv put postgres/adminpassword "$pw"
            echo "Generated postgres password (stored in Consul KV: postgres/adminpassword)"
          fi
        EOT
        ]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "postgres" {
      driver = "docker"

      template {
        data = <<EOH
POSTGRES_USER={{ key "postgres/adminuser" }}
POSTGRES_PASSWORD={{ key "postgres/adminpassword" }}
EOH
        destination = "secrets/env"
        env         = true
      }

      config {
        image = "postgres:16"
        ports = ["db"]
        mounts = [
          { type = "bind", source = "/opt/postgres/data", target = "/var/lib/postgresql/data", readonly = false },
        ]
      }

      service {
        name = "postgres"
        port = "db"
        check {
          type     = "tcp"
          interval = "10s"
          timeout  = "2s"
        }
      }

      resources {
        cpu    = 1000
        memory = 1024
      }
    }
  }

  group "pgadmin" {
    count = 1

    network {
      port "http" { to = 80 }
    }

    task "init-dirs" {
      driver = "raw_exec"
      lifecycle {
        hook = "prestart"
      }
      config {
        command = "bash"
        args    = ["-c", "mkdir -p /opt/postgres/pgadmin && chown -R 5050:5050 /opt/postgres/pgadmin"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "pgadmin" {
      driver = "docker"

      template {
        data = <<EOH
PGADMIN_DEFAULT_EMAIL=[[.pgadminEmail]]
PGADMIN_DEFAULT_PASSWORD={{ key "postgres/adminpassword" }}
EOH
        destination = "secrets/env"
        env         = true
      }

      template {
        data = <<EOH
{
  "Servers": {
    "1": {
      "Name": "Postgres",
      "Group": "Servers",
      "Host": "postgres.service.consul",
      "Port": 5432,
      "MaintenanceDB": "postgres",
      "Username": "{{ key "postgres/adminuser" }}",
      "SSLMode": "prefer"
    }
  }
}
EOH
        destination = "local/servers.json"
        perms       = "0644"
      }

      config {
        image = "dpage/pgadmin4"
        ports = ["http"]
        mounts = [
          { type = "bind", source = "/opt/postgres/pgadmin",  target = "/var/lib/pgadmin",     readonly = false },
          { type = "bind", source = "local/servers.json",     target = "/pgadmin4/servers.json", readonly = true },
        ]
      }

      env {
        PGADMIN_CONFIG_SERVER_MODE        = "True"
        PGADMIN_REPLACE_SERVERS_ON_STARTUP = "True"
      }

      service {
        name = "pgadmin"
        port = "http"
        tags = [
          "traefik.enable=true",
          "traefik.http.routers.pgadmin.rule=Host(`[[.pgadminDomain]]`)",
          "traefik.http.routers.pgadmin.entrypoints=websecure",
          "traefik.http.routers.pgadmin.tls=true",
          "traefik.http.routers.pgadmin.tls.certresolver=letsencrypt",
        ]
        check {
          type     = "http"
          path     = "/misc/ping"
          interval = "15s"
          timeout  = "3s"
        }
      }

      resources {
        cpu    = 500
        memory = 512
      }
    }
  }
}
