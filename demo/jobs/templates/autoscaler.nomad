job "autoscaler" {
  datacenters = ["platform"]

  group "autoscaler" {
    network {
      port "autoscaler" {}
      port "promtail" {}
    }

    task "autoscaler" {
      driver = "docker"

      artifact {
        source      = "https://github.com/Aiven-Open/nomad-droplets-autoscaler/releases/download/v0.0.25/nomad-droplets-autoscaler_Linux_x86_64.tar.gz"
        destination = "local/plugins/"
      }

      config {
        image   = "hashicorp/nomad-autoscaler:0.4.6"
        command = "nomad-autoscaler"

        args = [
          "agent",
          "-config", "local/config.hcl",
          "-plugin-dir", "local/plugins/"
        ]
        ports   = ["autoscaler"]
      }

      template {
        data = <<EOF
http {
  bind_address = "0.0.0.0"
  bind_port    = {{ env "NOMAD_PORT_autoscaler" }}
}

policy {
  dir = "local/policies"
}

nomad {
  address = "http://{{env "attr.unique.network.ip-address" }}:4646"
}

apm "prometheus" {
  driver = "prometheus"
  config = {
    address = "http://{{ range service "prometheus" }}{{ .Address }}:{{ .Port }}{{ end }}"
  }
}

strategy "pass-through" {
  driver = "pass-through"
}

target "do-droplets" {
  driver = "do-droplets"
  config = {
    token = "${token}"
    ssh_keys = "${ssh_key}"
    vpc_uuid = "${vpc_uuid}"
  }
}
EOF

        change_mode   = "signal"
        change_signal = "SIGHUP"
        destination   = "local/config.hcl"
      }

      template {
        data = <<EOF
scaling "batch" {
  enabled = true
  min     = 0
  max     = 5

  policy {
    cooldown            = "1m"
    evaluation_interval = "10s"

    check "batch_jobs_in_progess" {
      source = "prometheus"
      query  = "sum(nomad_nomad_job_summary_queued{exported_job=~\"batch/.*\"} + nomad_nomad_job_summary_running{exported_job=~\"batch/.*\"}) OR on() vector(0)"

      strategy "pass-through" {}
    }

    target "do-droplets" {
      name = "hashi-batch"
      region = "${region}"
      size = "s-1vcpu-1gb"
      snapshot_id = ${snapshot_id}
      user_data = "local/batch-startup.sh"
      tags = "hashi-stack"

      datacenter             = "batch_workers"
      node_drain_deadline    = "1h"
      node_selector_strategy = "empty_ignore_system"

      reserve_ipv4_addresses = "false"
      reserve_ipv6_addresses = "false"
      ipv6 = "true"
      create_reserved_addresses = "false"
    }
  }
}
EOF

        change_mode   = "signal"
        change_signal = "SIGHUP"
        destination   = "local/policies/batch.hcl"
      }

      template {
        destination = "local/batch-startup.sh"
        data = <<EOF
#!/bin/bash
/ops/scripts/client.sh "batch_workers" "hashi-server" "${token}"
EOF
      }

      service {
        name = "autoscaler"
        port = "autoscaler"

        check {
          type     = "http"
          path     = "/v1/health"
          interval = "10s"
          timeout  = "2s"
        }
      }
    }

    task "promtail" {
      driver = "docker"

      lifecycle {
        hook    = "prestart"
        sidecar = true
      }

      config {
        image = "grafana/promtail:1.5.0"
        ports = ["promtail"]

        args = [
          "-config.file",
          "local/promtail.yaml",
        ]
      }

      template {
        data = <<EOH
server:
  http_listen_port: {{ env "NOMAD_PORT_promtail" }}
  grpc_listen_port: 0

positions:
  filename: /tmp/positions.yaml

client:
  url: http://{{ range $i, $s := service "loki" }}{{ if eq $i 0 }}{{.Address}}:{{.Port}}{{end}}{{end}}/api/prom/push

scrape_configs:
- job_name: system
  entry_parser: raw
  static_configs:
  - targets:
      - localhost
    labels:
      task: autoscaler
      __path__: {{ env "NOMAD_ALLOC_DIR" }}/logs/autoscaler*
  pipeline_stages:
  - match:
      selector: '{task="autoscaler"}'
      stages:
      - regex:
          expression: '.*policy_id=(?P<policy_id>[a-zA-Z0-9_-]+).*reason="(?P<reason>.+)"'
      - labels:
          policy_id:
          reason:
EOH

        destination = "local/promtail.yaml"
      }

      resources {
        cpu    = 50
        memory = 32
      }

      service {
        name = "promtail"
        port = "promtail"

        check {
          type     = "http"
          path     = "/ready"
          interval = "10s"
          timeout  = "2s"
        }
      }
    }
  }
}
