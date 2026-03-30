job "github-runner" {
  datacenters = ["dc1"]
  type        = "service"

  group "runner" {
    count = 1

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
