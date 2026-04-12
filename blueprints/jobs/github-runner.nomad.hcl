job "github-runner" {
  datacenters = ["dc1"]
  type        = "service"

  group "runner" {
    count = 1

    # The runner image is ~1GB — give extra time for the initial pull.
    update {
      healthy_deadline  = "10m"
      progress_deadline = "15m"
    }

    # The runner operates in ephemeral mode — it exits cleanly (code 0) after
    # each GitHub Actions job and re-registers on restart. Allow generous
    # restarts so Nomad doesn't put the allocation into "Recovering" state.
    restart {
      attempts = 100
      interval = "1h"
      delay    = "5s"
      mode     = "delay"
    }

    task "init-secrets" {
      driver = "raw_exec"
      lifecycle { hook = "prestart" }
      config {
        command = "bash"
        args = ["-c", <<-EOT
          set -e

          # Persist config to Consul KV so the runner task can read them
          # via Nomad template. Only write non-empty values to avoid
          # overwriting existing KV entries on re-deploys.
          TOKEN="[[.githubToken]]"
          REPO="[[.githubRepo]]"
          LABELS="[[.runnerLabels]]"

          [ -n "$TOKEN" ]  && consul kv put github-runner/access-token "$TOKEN"
          [ -n "$REPO" ]   && consul kv put github-runner/repo-url "$REPO"
          [ -n "$LABELS" ] && consul kv put github-runner/labels "$LABELS"

          # Validate GitHub access
          STORED_TOKEN=$(consul kv get github-runner/access-token 2>/dev/null || true)
          STORED_REPO=$(consul kv get github-runner/repo-url 2>/dev/null || true)

          if [ -z "$STORED_TOKEN" ]; then
            echo "ERROR: No GitHub token in Consul KV and none provided."
            exit 1
          fi

          OWNER_REPO=$(echo "$STORED_REPO" | sed -E 's|https?://github\.com/||; s|\.git$||; s|/$||')
          if [ -z "$OWNER_REPO" ] || ! echo "$OWNER_REPO" | grep -qE '^[^/]+/[^/]+$'; then
            echo "ERROR: Invalid repository URL: $STORED_REPO"
            exit 1
          fi

          HEADER=$(curl -sI -H "Authorization: token $STORED_TOKEN" \
            -H "Accept: application/vnd.github+json" \
            "https://api.github.com/repos/$OWNER_REPO" 2>/dev/null | head -1)
          HTTP_CODE=$(echo "$HEADER" | grep -oE '[0-9]{3}' | head -1)

          case "$HTTP_CODE" in
            200) echo "GitHub validation OK: $OWNER_REPO is accessible" ;;
            401) echo "ERROR: GitHub token is invalid or expired."; exit 1 ;;
            403) echo "ERROR: GitHub token lacks permissions for $OWNER_REPO."; exit 1 ;;
            404) echo "ERROR: Repository not found: $OWNER_REPO."; exit 1 ;;
            *)   echo "WARNING: GitHub API returned HTTP $HTTP_CODE (continuing)" ;;
          esac
        EOT
        ]
      }
      resources {
        cpu    = 50
        memory = 32
      }
    }

    task "runner" {
      driver = "docker"

      # Read secrets from Consul KV — written by init-secrets on deploy.
      # Job updates no longer need to carry credentials in the HCL.
      template {
        data = <<EOH
ACCESS_TOKEN={{ key "github-runner/access-token" }}
REPO_URL={{ key "github-runner/repo-url" }}
LABELS={{ keyOrDefault "github-runner/labels" "self-hosted,nomad" }}
EOH
        destination = "secrets/env"
        env         = true
      }

      config {
        image        = "myoung34/github-runner:latest"
        network_mode = "host"
        volumes = [
          "/usr/local/bin/nomad:/usr/local/bin/nomad:ro",
          "/usr/local/bin/consul:/usr/local/bin/consul:ro",
        ]
      }

      env {
        RUNNER_NAME_PREFIX  = "nomad"
        RUNNER_SCOPE        = "repo"
        EPHEMERAL           = "false"
        DISABLE_AUTO_UPDATE = "true"
      }

      resources {
        cpu    = 500
        memory = 512
      }
    }
  }
}
