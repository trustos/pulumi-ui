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

          # Validate token format
          if [ -z "$TOKEN" ]; then
            echo "ERROR: GitHub token is empty. Provide a Personal Access Token with 'repo' scope."
            exit 1
          fi

          # Extract owner/repo from URL
          # Accepts: https://github.com/owner/repo or https://github.com/owner/repo.git
          OWNER_REPO=$(echo "$REPO_URL" | sed -E 's|https?://github\.com/||; s|\.git$||; s|/$||')
          if [ -z "$OWNER_REPO" ] || ! echo "$OWNER_REPO" | grep -qE '^[^/]+/[^/]+$'; then
            echo "ERROR: Invalid repository URL: $REPO_URL"
            echo "Expected format: https://github.com/owner/repo"
            exit 1
          fi

          # Check repo access with the token
          HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
            -H "Authorization: token $TOKEN" \
            -H "Accept: application/vnd.github+json" \
            "https://api.github.com/repos/$OWNER_REPO")

          case "$HTTP_CODE" in
            200)
              echo "GitHub validation OK: $OWNER_REPO is accessible"
              ;;
            401)
              echo "ERROR: GitHub token is invalid or expired."
              echo "Create a new token at https://github.com/settings/tokens"
              echo "Required scope: 'repo' (for repository runners)"
              exit 1
              ;;
            403)
              echo "ERROR: GitHub token lacks permissions for $OWNER_REPO."
              echo "Required scope: 'repo' (Classic PAT) or 'Administration: Read & Write' (Fine-grained PAT)"
              exit 1
              ;;
            404)
              echo "ERROR: Repository not found: $OWNER_REPO"
              echo "Check that the URL is correct and the token has access to this repository."
              echo "For private repos, the token needs 'repo' scope."
              exit 1
              ;;
            *)
              echo "WARNING: GitHub API returned HTTP $HTTP_CODE for $OWNER_REPO (continuing anyway)"
              ;;
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
        image = "myoung34/github-runner:latest"
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
