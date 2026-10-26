# Duradent — tienda B2B de ortodoncia

Monorepo de la tienda online de Duradent SAC (Perú). Clientes: odontólogos y clínicas.
Plan completo y modelo de datos: ver `docs/plan.md`.

@AGENTS.md

## Estructura

```
apps/web/          Astro 7 SSR (adapter node standalone): tienda + /admin
services/api/      Go: un binario `duradent` con modos api | worker | outbox-relay | migrate
infra/local/       Init de LocalStack (SNS, SQS+DLQ, S3) para docker compose
infra/terraform/   AWS (Fase 6)
Taskfile.yml       Todos los comandos
```

## Comandos

Requisitos: Go 1.27, Node 24, Docker Desktop, `task` y `golangci-lint`
(`go install github.com/go-task/task/v3/cmd/task@latest`,
`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`).

| Qué | Comando |
|---|---|
| Levantar todo | `task up` (o `docker compose up -d --build`) |
| Estado / logs | `task ps`, `task logs -- api` |
| Parar / borrar datos | `task down`, `task reset` |
| Lint completo | `task lint` |
| Tests completos (necesita Docker) | `task test` |
| Tests unitarios rápidos | `task api:test:unit` |
| Un test Go | `cd services/api && go test ./internal/platform/mysql -run TestMigrate` |
| Migrar BD local | `task migrate` |
| Regenerar tipos TS del API | `task web:gen-api` (después de editar `services/api/openapi.yaml`) |

URLs locales: web http://localhost:4321 · API http://localhost:8080 (`/healthz`, `/readyz`, `/metrics`) ·
Mailpit http://localhost:8025 · LocalStack http://localhost:4566 · MySQL `localhost:3307` (duradent/duradent; cambia con `MYSQL_HOST_PORT`).

El servidor de desarrollo de Astro fuera de Docker: `astro dev --background` (ver AGENTS.md).

## Arquitectura del backend (hexagonal)

Cada bounded context vive en `services/api/internal/<context>/` con cuatro capas:

- `domain/` — entidades, value objects, eventos, errores. **Solo stdlib** (y `internal/kernel`).
- `application/` — casos de uso. Usa `domain` y `ports`. Nunca `adapters` ni `internal/platform`.
- `ports/` — interfaces (repositorios, gateways, publicadores). Usa `domain`.
- `adapters/` — implementaciones: `mysql/`, `http/`, `culqi/`, `nubefact/`, `ses/`, `sqs/`…

Ningún contexto importa `domain`, `application` o `adapters` de otro: se comunican por **eventos**
o a través de los `ports` del otro. `internal/platform/` es infraestructura compartida
(config, logging, httpserver, mysql, outbox). `internal/kernel/` es para tipos de dominio
compartidos sin infraestructura (ids, money).

Estas reglas las verifica `internal/archtest` en cada `go test`. Si falla, corrige la dependencia;
no relajes la regla sin registrar la decisión abajo.

Contextos previstos: catalog, pricing, cart, orders, payments, inventory, identity, shipping,
invoicing, notifications.

## Convenciones

- **Go**: `log/slog` JSON con `request_id`; errores con `%w`; `context.Context` como primer
  argumento; sin ORM (SQL explícito en adapters). Formato con `task api:fmt`.
- **Tests**: unitarios en `domain`/`application` con fakes de los ports. Integración en adapters
  con `internal/platform/mysqltest.Start(t)` (testcontainers, MySQL 8.4). `go test -short`
  salta los de integración.
- **SQL**: migraciones en `services/api/migrations/NNNN_nombre.{up,down}.sql`, embebidas en el
  binario. IDs `BINARY(16)` UUIDv7, fechas `DATETIME(6)` UTC, dinero `DECIMAL(12,2)` + `currency CHAR(3)`.
- **API**: el contrato es `services/api/openapi.yaml`. Errores en formato RFC 9457 (`Problem`).
  CI falla si `apps/web/src/lib/api/schema.d.ts` no está regenerado.
- **Web**: textos siempre en archivos de traducción (es, en, pt-BR), nunca en el markup.
  Islas (Preact) solo donde haya interacción.
- **Config**: solo variables de entorno (`internal/platform/config`). En AWS los secretos llegan
  desde Secrets Manager al entorno de la tarea ECS. Nunca credenciales en el repo.
- **Commits**: una fase o un cambio coherente por commit; ramas `fase-N-…`.

## Decisiones de arquitectura (ADR)

1. **Pagos con Culqi, no Stripe.** Stripe no acepta comercios constituidos en Perú. Culqi cubre
   tarjetas y Yape en PEN/USD. El puerto `PaymentGateway` permite añadir Stripe si existe una
   entidad extranjera. Además, transferencia bancaria con confirmación manual en el admin.
2. **B2B con listas de precios por cliente.** Se pide RUC/COP en la cuenta, no bloquea la compra.
   Factura (01) con RUC, boleta (03) sin él.
3. **Monolito modular.** Un binario y una imagen; ECS ejecuta `api`, `worker` y `outbox-relay`
   como servicios separados. Partir en microservicios solo si un contexto necesita escalar o
   desplegarse aparte.
4. **SNS + SQS como bus, no NATS.** Equipo de 1-2 personas: servicio gestionado, DLQ nativa,
   IAM, sin clúster que operar. Un topic `duradent-domain-events`, una cola + DLQ por contexto
   consumidor (`maxReceiveCount=5`). Local: LocalStack.
5. **Transactional outbox en MySQL.** El cambio de estado y el evento se escriben en la misma
   transacción (`outbox`); `outbox-relay` los publica. Consumidores idempotentes vía
   `processed_messages (consumer, message_id)`.
6. **SES para correo transaccional.** WorkMail es hosting de buzones (IMAP/calendario), no un
   servicio de envío programático; queda para `ventas@`, `soporte@`. Local: Mailpit.
7. **SUNAT vía OSE/PSE (Nubefact)**, no integración directa con SUNAT.
8. **Brasil y LATAM: solo idioma** en la primera versión. Envíos solo a Perú por zonas.
9. **Taskfile en lugar de Makefile**: el equipo desarrolla en Windows.
10. **`openapi-typescript` por `npx` con versión fija**: declara peer TypeScript 5 y la web usa 6;
    es solo una herramienta de generación, no una dependencia del bundle.

## Estado por fases

- [x] Fase 1 — monorepo, Compose, CI
- [ ] Fase 2 — catálogo, pricing, frontend i18n, admin de catálogo
- [ ] Fase 3 — identidad (Google, Microsoft, email)
- [ ] Fase 4 — carrito, envíos, pedidos, pagos Culqi + transferencia
- [ ] Fase 5 — outbox relay, worker, inventario, emails, facturación
- [ ] Fase 6 — Terraform, ALB, ECS, despliegue
