# One Fargate service (or a run-once task definition when desired_count is
# null) running the given image and command.
#
# Deploys do not go through Terraform: infra/scripts/deploy.sh registers a new
# revision of the task definition with the new image and updates the service.
# That is why the service ignores task_definition changes.

variable "name" { type = string }
variable "cluster_arn" { type = string }
variable "image" { type = string }
variable "command" {
  type    = list(string)
  default = null
}
variable "cpu" {
  type    = number
  default = 256
}
variable "memory" {
  type    = number
  default = 512
}
variable "environment" {
  type    = map(string)
  default = {}
}
variable "secrets" {
  description = "Env var name => Secrets Manager ARN (optionally ARN:json-key::)."
  type        = map(string)
  default     = {}
}
variable "execution_role_arn" { type = string }
variable "task_role_arn" { type = string }
variable "subnet_ids" { type = list(string) }
variable "security_group_ids" { type = list(string) }
variable "container_port" {
  type    = number
  default = 8080
}
variable "desired_count" {
  description = "null: only the task definition (one-off tasks such as migrate)."
  type        = number
  default     = 1
}
variable "max_count" {
  description = "Autoscaling ceiling on CPU; equal to desired_count disables scaling."
  type        = number
  default     = 1
}
variable "target_group_arn" {
  type    = string
  default = ""
}
variable "service_connect_namespace" {
  type    = string
  default = ""
}
variable "service_connect_name" {
  description = "Publish the container port under this name (e.g. api); empty: client only."
  type        = string
  default     = ""
}
variable "log_retention_days" {
  type    = number
  default = 30
}

data "aws_region" "current" {}

resource "aws_cloudwatch_log_group" "this" {
  name              = "/ecs/${var.name}"
  retention_in_days = var.log_retention_days
}

resource "aws_ecs_task_definition" "this" {
  family                   = var.name
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = var.execution_role_arn
  task_role_arn            = var.task_role_arn
  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "X86_64"
  }
  container_definitions = jsonencode([{
    name         = "app"
    image        = var.image
    command      = var.command
    essential    = true
    portMappings = [{ name = "http", containerPort = var.container_port, protocol = "tcp" }]
    environment  = [for k in sort(keys(var.environment)) : { name = k, value = var.environment[k] }]
    secrets      = [for k in sort(keys(var.secrets)) : { name = k, valueFrom = var.secrets[k] }]
    stopTimeout  = 30
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.this.name
        awslogs-region        = data.aws_region.current.region
        awslogs-stream-prefix = "app"
      }
    }
  }])
}

resource "aws_ecs_service" "this" {
  count                              = var.desired_count == null ? 0 : 1
  name                               = var.name
  cluster                            = var.cluster_arn
  task_definition                    = aws_ecs_task_definition.this.arn
  desired_count                      = var.desired_count
  launch_type                        = "FARGATE"
  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200
  health_check_grace_period_seconds  = var.target_group_arn == "" ? null : 60
  enable_execute_command             = true
  propagate_tags                     = "SERVICE"

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  network_configuration {
    subnets          = var.subnet_ids
    security_groups  = var.security_group_ids
    assign_public_ip = false
  }

  dynamic "load_balancer" {
    for_each = var.target_group_arn == "" ? [] : [var.target_group_arn]
    content {
      target_group_arn = load_balancer.value
      container_name   = "app"
      container_port   = var.container_port
    }
  }

  dynamic "service_connect_configuration" {
    for_each = var.service_connect_namespace == "" ? [] : [1]
    content {
      enabled   = true
      namespace = var.service_connect_namespace
      dynamic "service" {
        for_each = var.service_connect_name == "" ? [] : [var.service_connect_name]
        content {
          port_name      = "http"
          discovery_name = service.value
          client_alias {
            dns_name = service.value
            port     = var.container_port
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [task_definition, desired_count]
  }
}

resource "aws_appautoscaling_target" "this" {
  count              = var.desired_count != null && var.max_count > coalesce(var.desired_count, 0) ? 1 : 0
  service_namespace  = "ecs"
  resource_id        = "service/${element(split("/", var.cluster_arn), 1)}/${aws_ecs_service.this[0].name}"
  scalable_dimension = "ecs:service:DesiredCount"
  min_capacity       = var.desired_count
  max_capacity       = var.max_count
}

resource "aws_appautoscaling_policy" "cpu" {
  count              = length(aws_appautoscaling_target.this)
  name               = "${var.name}-cpu"
  policy_type        = "TargetTrackingScaling"
  service_namespace  = "ecs"
  resource_id        = aws_appautoscaling_target.this[0].resource_id
  scalable_dimension = aws_appautoscaling_target.this[0].scalable_dimension
  target_tracking_scaling_policy_configuration {
    target_value       = 60
    scale_in_cooldown  = 300
    scale_out_cooldown = 60
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
  }
}

output "task_definition_family" { value = aws_ecs_task_definition.this.family }
output "service_name" { value = var.desired_count == null ? "" : aws_ecs_service.this[0].name }
