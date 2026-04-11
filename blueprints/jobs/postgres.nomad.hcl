job "postgres" {
  datacenters = ["dc1"]
  type        = "service"

  group "postgres" {
    count = 1

    volume "postgres-data" {
      type   = "host"
      source = "postgres-data"
    }

    network {
      port "db" {
        static = 5432
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
            USER_PW="[[.dbPassword]]"
            if [ -z "$USER_PW" ]; then
              USER_PW=$(openssl rand -base64 24)
              echo "Generated postgres password (stored in Consul KV)"
            else
              echo "Using provided postgres password (stored in Consul KV)"
            fi
            consul kv put postgres/adminpassword "$USER_PW"
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

      volume_mount {
        volume      = "postgres-data"
        destination = "/var/lib/postgresql/data"
      }

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
}
