# Infraestructura AWS

```
shared/              ECR (duradent/api, duradent/web) y roles de GitHub Actions (OIDC)
envs/staging/        entorno pequeño (1 NAT, RDS single-AZ, 1 tarea por servicio)
envs/prod/           producción (NAT por AZ, RDS Multi-AZ, autoescalado)
modules/stack/       un entorno completo; compone los módulos siguientes
modules/network      VPC en 2 AZ, subredes públicas/privadas, NAT, endpoint de S3
modules/alb          ALB HTTPS (TLS 1.2+), redirección 80→443, webhook de Culqi al API
modules/ecs-service  servicio Fargate (o tarea única) con logs, Service Connect y autoescalado
modules/rds-mysql    RDS MySQL 8.4 cifrado, TLS obligatorio, DSN en Secrets Manager, alarmas
modules/messaging    SNS + SQS + DLQ con filter policy por consumidor y alarma de DLQ
modules/s3-cdn       bucket privado de imágenes + CloudFront con OAC
modules/ses          identidad de dominio con DKIM, MAIL FROM y configuration set
```

Pasos, despliegue, rollback y operación: sección **Runbook (AWS)** de `CLAUDE.md`.
Validar sin credenciales: `task tf:validate`.
