# RDS MySQL 8.4 in the private subnets. The application user's password is
# generated here and the full DSN (with tls=rds) is stored in Secrets Manager,
# which ECS injects as MYSQL_DSN.

variable "name" { type = string }
variable "vpc_id" { type = string }
variable "subnet_ids" { type = list(string) }
variable "client_security_group_ids" {
  description = "Security groups allowed to connect (ECS tasks)."
  type        = list(string)
}
variable "instance_class" {
  type    = string
  default = "db.t4g.small"
}
variable "allocated_storage" {
  type    = number
  default = 20
}
variable "multi_az" {
  type    = bool
  default = true
}
variable "deletion_protection" {
  type    = bool
  default = true
}
variable "backup_retention_days" {
  type    = number
  default = 7
}
variable "alarm_topic_arn" {
  type    = string
  default = ""
}

resource "aws_db_subnet_group" "this" {
  name       = var.name
  subnet_ids = var.subnet_ids
}

resource "aws_security_group" "db" {
  name        = "${var.name}-db"
  description = "MySQL from the application tasks"
  vpc_id      = var.vpc_id
}

resource "aws_vpc_security_group_ingress_rule" "mysql" {
  for_each                     = toset(var.client_security_group_ids)
  security_group_id            = aws_security_group.db.id
  referenced_security_group_id = each.value
  ip_protocol                  = "tcp"
  from_port                    = 3306
  to_port                      = 3306
}

resource "aws_db_parameter_group" "this" {
  name   = var.name
  family = "mysql8.4"
  parameter {
    name  = "character_set_server"
    value = "utf8mb4"
  }
  parameter {
    name  = "collation_server"
    value = "utf8mb4_0900_ai_ci"
  }
  parameter {
    name  = "require_secure_transport"
    value = "1"
  }
  parameter {
    name  = "time_zone"
    value = "UTC"
  }
}

resource "random_password" "db" {
  length  = 32
  special = false # avoids escaping inside the DSN
}

resource "aws_db_instance" "this" {
  identifier                      = var.name
  engine                          = "mysql"
  engine_version                  = "8.4"
  instance_class                  = var.instance_class
  allocated_storage               = var.allocated_storage
  max_allocated_storage           = var.allocated_storage * 5
  storage_type                    = "gp3"
  storage_encrypted               = true
  db_name                         = "duradent"
  username                        = "duradent"
  password                        = random_password.db.result
  db_subnet_group_name            = aws_db_subnet_group.this.name
  vpc_security_group_ids          = [aws_security_group.db.id]
  parameter_group_name            = aws_db_parameter_group.this.name
  multi_az                        = var.multi_az
  publicly_accessible             = false
  backup_retention_period         = var.backup_retention_days
  backup_window                   = "08:00-09:00" # 03:00-04:00 in Lima
  maintenance_window              = "sun:09:00-sun:10:00"
  auto_minor_version_upgrade      = true
  deletion_protection             = var.deletion_protection
  skip_final_snapshot             = false
  final_snapshot_identifier       = "${var.name}-final"
  copy_tags_to_snapshot           = true
  performance_insights_enabled    = true
  enabled_cloudwatch_logs_exports = ["error", "slowquery"]
}

resource "aws_secretsmanager_secret" "dsn" {
  name                    = "${var.name}/mysql-dsn"
  description             = "MYSQL_DSN for the duradent containers"
  recovery_window_in_days = 7
}

resource "aws_secretsmanager_secret_version" "dsn" {
  secret_id = aws_secretsmanager_secret.dsn.id
  secret_string = format("%s:%s@tcp(%s)/%s?parseTime=true&multiStatements=true&tls=rds",
  aws_db_instance.this.username, random_password.db.result, aws_db_instance.this.endpoint, aws_db_instance.this.db_name)
}

resource "aws_cloudwatch_metric_alarm" "cpu" {
  alarm_name          = "${var.name}-cpu-high"
  namespace           = "AWS/RDS"
  metric_name         = "CPUUtilization"
  dimensions          = { DBInstanceIdentifier = aws_db_instance.this.identifier }
  statistic           = "Average"
  period              = 300
  evaluation_periods  = 3
  threshold           = 80
  comparison_operator = "GreaterThanThreshold"
  alarm_actions       = var.alarm_topic_arn == "" ? [] : [var.alarm_topic_arn]
}

resource "aws_cloudwatch_metric_alarm" "storage" {
  alarm_name          = "${var.name}-free-storage-low"
  namespace           = "AWS/RDS"
  metric_name         = "FreeStorageSpace"
  dimensions          = { DBInstanceIdentifier = aws_db_instance.this.identifier }
  statistic           = "Minimum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 2 * 1024 * 1024 * 1024
  comparison_operator = "LessThanThreshold"
  alarm_actions       = var.alarm_topic_arn == "" ? [] : [var.alarm_topic_arn]
}

output "dsn_secret_arn" { value = aws_secretsmanager_secret.dsn.arn }
output "endpoint" { value = aws_db_instance.this.endpoint }
