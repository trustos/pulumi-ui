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

    task "init-secrets" {
      driver = "raw_exec"
      lifecycle {
        hook = "prestart"
      }
      config {
        command = "bash"
        args = ["-c", <<-EOT
          set -e
          if ! consul kv get nocobase/db_user > /dev/null 2>&1; then
            consul kv put nocobase/db_user "[[.dbUser]]"
          fi
          if ! consul kv get nocobase/db_password > /dev/null 2>&1; then
            USER_PW="[[.dbPassword]]"
            if [ -z "$USER_PW" ]; then
              USER_PW=$(openssl rand -base64 24)
              echo "Generated nocobase DB password (stored in Consul KV)"
            else
              echo "Using provided nocobase DB password (stored in Consul KV)"
            fi
            consul kv put nocobase/db_password "$USER_PW"
          fi
          if ! consul kv get nocobase/db_name > /dev/null 2>&1; then
            consul kv put nocobase/db_name "[[.dbName]]"
          fi
        EOT
        ]
      }
      resources {
        cpu    = 100
        memory = 64
      }
    }

    task "init-db" {
      driver = "docker"
      lifecycle {
        hook = "prestart"
      }
      template {
        data = <<EOH
PGHOST=postgres.service.consul
PGPORT=5432
PGUSER={{ key "postgres/adminuser" }}
PGPASSWORD={{ key "postgres/adminpassword" }}
DB_NAME={{ key "nocobase/db_name" }}
DB_APP_USER={{ key "nocobase/db_user" }}
DB_APP_PASSWORD={{ key "nocobase/db_password" }}
EOH
        destination = "secrets/env"
        env         = true
      }
      config {
        image        = "postgres:16"
        network_mode = "host"
        command      = "bash"
        args = ["-c", <<-EOT
          set -e
          for i in $(seq 1 30); do
            if pg_isready -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" > /dev/null 2>&1; then
              break
            fi
            echo "Waiting for postgres ($i/30)..."
            sleep 2
          done

          # Create database if it doesn't exist
          existing=$(psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -tAc "SELECT 1 FROM pg_database WHERE datname='$DB_NAME'" 2>/dev/null || true)
          if [ "$existing" != "1" ]; then
            createdb -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" "$DB_NAME"
            echo "Created database: $DB_NAME"
          else
            echo "Database $DB_NAME already exists"
          fi

          # Create dedicated user if it doesn't exist
          user_exists=$(psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -tAc "SELECT 1 FROM pg_roles WHERE rolname='$DB_APP_USER'" 2>/dev/null || true)
          if [ "$user_exists" != "1" ]; then
            psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -c "CREATE USER $DB_APP_USER WITH PASSWORD '$DB_APP_PASSWORD'"
            echo "Created user: $DB_APP_USER"
          else
            psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -c "ALTER USER $DB_APP_USER WITH PASSWORD '$DB_APP_PASSWORD'" 2>/dev/null || true
            echo "User $DB_APP_USER already exists (password synced)"
          fi

          # Grant privileges
          psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$DB_NAME" -c "
            GRANT ALL PRIVILEGES ON DATABASE $DB_NAME TO $DB_APP_USER;
            GRANT ALL ON SCHEMA public TO $DB_APP_USER;
            GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO $DB_APP_USER;
            GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO $DB_APP_USER;
            ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO $DB_APP_USER;
            ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO $DB_APP_USER;
          "
          echo "Database $DB_NAME ready for user $DB_APP_USER."
        EOT
        ]
      }
      resources {
        cpu    = 200
        memory = 128
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
        DB_TYPE = "postgres"
        TZ      = "UTC"
      }

      template {
        data = <<EOH
APP_ENV=production
APP_PORT=13000
APP_KEY=[[.appKey]]
DB_DIALECT=postgres
DB_HOST=postgres.service.consul
DB_PORT=5432
DB_DATABASE={{ key "nocobase/db_name" }}
DB_USER={{ key "nocobase/db_user" }}
DB_PASSWORD={{ key "nocobase/db_password" }}
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
          path     = "/"
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
