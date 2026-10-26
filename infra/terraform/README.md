# Infraestructura AWS (Fase 6)

Terraform para staging y producción: VPC multi-AZ, ALB, ECS Fargate (web, api, worker,
outbox-relay), RDS MySQL 8 multi-AZ, SNS/SQS + DLQ, S3 + CloudFront, SES, Secrets Manager
y alarmas de CloudWatch. Estructura prevista:

```
modules/{network,alb,ecs-service,rds-mysql,messaging,s3-cdn,ses,secrets}
envs/{staging,prod}
```

Los recursos de mensajería replican lo que `infra/local/localstack/init-aws.sh` crea en local.
