output "alb_dns_name" {
  description = "Point the store's DNS (CNAME or Route 53 alias) here."
  value       = module.alb.dns_name
}

output "alb_zone_id" { value = module.alb.zone_id }

output "media_base_url" { value = module.media.base_url }

output "ses_dns_records" {
  description = "DKIM, MAIL FROM and DMARC records to publish for the mail domain."
  value       = module.ses.dns_records
}

output "app_secret_arn" {
  description = "Fill with the third-party credentials before the first deploy."
  value       = aws_secretsmanager_secret.app.arn
}

output "culqi_webhook_url_prefix" {
  description = "Append the CULQI_WEBHOOK_SECRET value and register it in CulqiPanel."
  value       = "https://${var.domain}/v1/webhooks/culqi/"
}

# Read by infra/scripts/deploy.sh.
output "deploy" {
  value = {
    cluster        = aws_ecs_cluster.this.name
    subnets        = module.network.task_subnet_ids
    public_ip      = module.network.tasks_need_public_ip ? "ENABLED" : "DISABLED"
    security_group = aws_security_group.tasks.id
    migrate_family = module.migrate.task_definition_family
    services = {
      api            = { service = module.api.service_name, family = module.api.task_definition_family, image = "api" }
      worker         = { service = module.worker.service_name, family = module.worker.task_definition_family, image = "api" }
      "outbox-relay" = { service = module.relay.service_name, family = module.relay.task_definition_family, image = "api" }
      web            = { service = module.web.service_name, family = module.web.task_definition_family, image = "web" }
    }
  }
}
