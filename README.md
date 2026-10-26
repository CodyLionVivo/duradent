# Duradent — tienda online

Tienda B2B de productos de ortodoncia de Duradent SAC (Perú).

- `apps/web` — Astro 7 SSR (tienda y panel admin), i18n es / en / pt-BR
- `services/api` — backend Go, arquitectura hexagonal, eventos con outbox + SNS/SQS
- `infra/` — LocalStack para desarrollo y Terraform para AWS

## Empezar

```bash
docker compose up -d --build
```

Web en http://localhost:4321, API en http://localhost:8080/healthz, correos en http://localhost:8025.

Comandos, convenciones y decisiones de arquitectura: [CLAUDE.md](CLAUDE.md). Plan del proyecto: [docs/plan.md](docs/plan.md).
