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

    task "validate-github" {
      driver = "raw_exec"
      lifecycle {
        hook = "prestart"
      }
      config {
        command = "bash"
        args = ["-c", <<-EOT
          set -e
          TOKEN="[[.githubToken]]"
          REPO_URL="[[.githubRepo]]"

          if [ -z "$TOKEN" ]; then
            echo "ERROR: GitHub token is empty. Provide a Personal Access Token with 'repo' scope."
            exit 1
          fi

          # Extract owner/repo from URL
          OWNER_REPO=$(echo "$REPO_URL" | sed -E 's|https?://github\.com/||; s|\.git$||; s|/$||')
          if [ -z "$OWNER_REPO" ] || ! echo "$OWNER_REPO" | grep -qE '^[^/]+/[^/]+$'; then
            echo "ERROR: Invalid repository URL: $REPO_URL"
            echo "Expected format: https://github.com/owner/repo"
            exit 1
          fi

          # Check repo access — parse HTTP status from response header
          # (avoid curl -w with percent-brace format which HCL heredocs reject)
          HEADER=$(curl -sI -H "Authorization: token $TOKEN" \
            -H "Accept: application/vnd.github+json" \
            "https://api.github.com/repos/$OWNER_REPO" 2>/dev/null | head -1)
          HTTP_CODE=$(echo "$HEADER" | grep -oE '[0-9]{3}' | head -1)

          case "$HTTP_CODE" in
            200) echo "GitHub validation OK: $OWNER_REPO is accessible" ;;
            401) echo "ERROR: GitHub token is invalid or expired. Create a new token at https://github.com/settings/tokens with 'repo' scope."; exit 1 ;;
            403) echo "ERROR: GitHub token lacks permissions for $OWNER_REPO. Need 'repo' scope (Classic PAT) or 'Administration: Read & Write' (Fine-grained PAT)."; exit 1 ;;
            404) echo "ERROR: Repository not found: $OWNER_REPO. Check URL and token access."; exit 1 ;;
            *)   echo "WARNING: GitHub API returned HTTP $HTTP_CODE for $OWNER_REPO (continuing)" ;;
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

      config {
        image        = "myoung34/github-runner:latest"
        network_mode = "host"
        volumes = [
          "/usr/local/bin/nomad:/usr/local/bin/nomad:ro",
          "/usr/local/bin/consul:/usr/local/bin/consul:ro",
        ]
      }

      env {
        ACCESS_TOKEN        = "[[.githubToken]]"
        REPO_URL            = "[[.githubRepo]]"
        RUNNER_NAME_PREFIX  = "nomad"
        RUNNER_SCOPE        = "repo"
        LABELS              = "[[.runnerLabels]]"
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
