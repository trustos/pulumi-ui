job "postgres-backup" {
  datacenters = ["dc1"]
  type        = "batch"

  periodic {
    crons            = ["[[.backupSchedule]]"]
    prohibit_overlap = true
    time_zone        = "UTC"
  }

  group "backup" {
    count = 1

    task "init-dirs" {
      driver = "raw_exec"
      lifecycle {
        hook = "prestart"
      }
      config {
        command = "bash"
        args    = ["-c", "mkdir -p /opt/postgres/backups && chown -R 999:999 /opt/postgres/backups"]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "pgdump" {
      driver = "docker"

      config {
        image   = "postgres:16"
        command = "bash"
        args    = ["local/backup.sh"]
        mounts = [
          { type = "bind", source = "/opt/postgres/backups", target = "/backups", readonly = false },
        ]
      }

      template {
        data = <<-EOH
          #!/bin/bash
          set -euo pipefail

          # Clean up old local backups (keep last 24h)
          find /backups -name "*.sql.gz" -mtime +0 -delete 2>/dev/null || true

          for DB in $(psql -h postgres.service.consul -U "$PGUSER" -d postgres -t -c "SELECT datname FROM pg_database WHERE datistemplate = false AND datname NOT IN ('postgres')"); do
            DB=$(echo "$DB" | xargs)
            if [ -n "$DB" ]; then
              OUTFILE="/backups/${DB}-$(date +%F-%H%M%S).sql.gz"
              echo "Dumping $DB → $OUTFILE"
              pg_dump -h postgres.service.consul -U "$PGUSER" "$DB" | gzip > "$OUTFILE"
            fi
          done
          echo "Backup complete."
        EOH
        destination = "local/backup.sh"
        perms       = "755"
      }

      template {
        data = <<-EOH
          PGUSER={{ key "postgres/adminuser" }}
          PGPASSWORD={{ key "postgres/adminpassword" }}
        EOH
        destination = "secrets/env"
        env         = true
      }

      resources {
        cpu    = 500
        memory = 512
      }
    }

    task "upload-to-oci" {
      driver = "raw_exec"
      user   = "root"

      lifecycle {
        hook = "poststart"
      }

      template {
        data = <<-EOH
          #!/bin/bash
          set -euo pipefail

          export PATH=$PATH:/root/.local/bin
          export OCI_CLI_AUTH=instance_principal

          NAMESPACE=$(oci os ns get --query "data" --raw-output)
          COMPARTMENT_ID=$(curl -sL http://169.254.169.254/opc/v2/instance/compartmentId -H "Authorization: Bearer Oracle")
          BUCKET="[[.bucketName]]"

          # Create bucket if needed
          if ! oci os bucket get -bn "$BUCKET" --namespace-name "$NAMESPACE" >/dev/null 2>&1; then
            echo "Creating bucket $BUCKET..."
            oci os bucket create --compartment-id "$COMPARTMENT_ID" --namespace-name "$NAMESPACE" --name "$BUCKET"
          fi

          BACKUP_DIR="/opt/postgres/backups"
          UPLOADED=0

          for f in "$BACKUP_DIR"/*.sql.gz; do
            [ -f "$f" ] || continue
            # Only upload files created in the last 10 minutes
            if [ $(find "$f" -newermt "10 minutes ago" 2>/dev/null | wc -l) -gt 0 ]; then
              echo "Uploading $(basename "$f")..."
              oci os object put -bn "$BUCKET" --file "$f" --name "$(basename "$f")" --force
              UPLOADED=$((UPLOADED + 1))
            fi
          done

          echo "Uploaded $UPLOADED file(s)."

          # Retention: keep last N backups per database
          RETENTION=[[.retentionCount]]
          ALL_OBJECTS=$(oci os object list -bn "$BUCKET" --query "data[].name" --raw-output 2>/dev/null | jq -r '.[]' 2>/dev/null || echo "")
          if [ -n "$ALL_OBJECTS" ]; then
            DB_NAMES=$(echo "$ALL_OBJECTS" | grep -E '.*-[0-9]{4}-[0-9]{2}-[0-9]{2}.*\.sql\.gz$' | sed -E 's/-[0-9]{4}-[0-9]{2}-[0-9]{2}.*$//' | sort -u)
            for DB in $DB_NAMES; do
              COUNT=$(echo "$ALL_OBJECTS" | grep "^${DB}-" | wc -l | tr -d ' ')
              if [ "$COUNT" -gt "$RETENTION" ]; then
                echo "Pruning $DB: keeping $RETENTION of $COUNT backups"
                echo "$ALL_OBJECTS" | grep "^${DB}-" | sort -r | tail -n +$((RETENTION + 1)) | while read -r old; do
                  oci os object delete -bn "$BUCKET" --object-name "$old" --force
                done
              fi
            done
          fi

          echo "Backup + upload complete."
        EOH
        destination = "local/upload.sh"
        perms       = "755"
      }

      config {
        command = "bash"
        args    = ["local/upload.sh"]
      }

      resources {
        cpu    = 200
        memory = 128
      }
    }
  }
}
