job "pgadmin" {
  datacenters = ["dc1"]
  type        = "service"

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
PGADMIN_DEFAULT_EMAIL=[[.email]]
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
        tags = []
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
