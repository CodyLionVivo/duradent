# Duradent — tienda B2B de ortodoncia

Monorepo de la tienda online de Duradent SAC (Perú). Clientes: odontólogos y clínicas.
Plan completo y modelo de datos: ver `docs/plan.md`.

@AGENTS.md

## Estructura

```
apps/web/          Astro 7 SSR (adapter node standalone): tienda + /admin
services/api/      Go: un binario `duradent` con modos api | worker | outbox-relay | migrate | seed
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
| Cargar catálogo demo | `task seed` (20 productos, 53 variantes, 3 listas; se salta si ya hay datos) |
| Recargar demo desde cero | `task reset && task up` |
| Tests web (Vitest) | `task web:test` |
| Regenerar tipos TS del API | `task web:gen-api` (después de editar `services/api/openapi.yaml`) |

URLs locales: web http://localhost:4321 · API http://localhost:8080 (`/healthz`, `/readyz`, `/metrics`) ·
Mailpit http://localhost:8025 · LocalStack http://localhost:4566 · MySQL `localhost:3307` (duradent/duradent; cambia con `MYSQL_HOST_PORT`).

El servidor de desarrollo de Astro fuera de Docker: `astro dev --background` (ver AGENTS.md).

**Tienda**: `/{es|en|pt-br}`, `/…/catalog`, `/…/catalog/{categoría}`, `/…/product/{slug}`,
`/…/account` (login, registro, perfil). `/` redirige según `Accept-Language`.

**Admin**: http://localhost:4321/admin → pide iniciar sesión. En local usa el **acceso de desarrollo**
de la página de login con `admin@duradent.local` (está en `ADMIN_EMAILS` del compose). Para scripts,
las rutas `/v1/admin/*` del API también aceptan `Authorization: Bearer local-dev-admin-token`.

**Google / Microsoft en local**: exporta `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET` (o los de
Microsoft) antes de `task up`; redirect URI a registrar: `http://localhost:4321/auth/<google|microsoft>/callback`.

## Arquitectura del backend (hexagonal)

Cada bounded context vive en `services/api/internal/<context>/` con cuatro capas:

- `domain/` — entidades, value objects, eventos, errores. **Solo stdlib** (y `internal/kernel`).
- `application/` — casos de uso. Usa `domain` y `ports`. Nunca `adapters` ni `internal/platform`.
- `ports/` — interfaces (repositorios, gateways, publicadores). Usa `domain`.
- `adapters/` — implementaciones: `mysql/`, `http/`, `culqi/`, `nubefact/`, `ses/`, `sqs/`…

Ningún contexto importa `domain`, `application` o `adapters` de otro: se comunican por **eventos**
o a través de los `ports` del otro. `internal/platform/` es infraestructura compartida
(config, logging, httpserver, mysql, outbox). `internal/kernel/` es para tipos de dominio
compartidos sin infraestructura: `id` (UUIDv7), `money` (céntimos `int64`, nunca float),
`locale` (es, en, pt-br) y `apperr` (errores tipados Invalid/NotFound/Conflict con código estable).

Estas reglas las verifica `internal/archtest` en cada `go test`. Si falla, corrige la dependencia;
no relajes la regla sin registrar la decisión abajo.

Contextos implementados: **catalog** (productos, variantes, categorías, atributos, imágenes en S3),
**pricing** (listas de precios con tramos por volumen) e **identity** (cuentas, OIDC Google/Microsoft,
contraseña Argon2id, sesiones, perfil de facturación RUC/DNI/CE, direcciones, roles). Previstos:
cart, orders, payments, inventory, shipping, invoicing, notifications.

**Autenticación**: `internal/platform/auth` resuelve `Authorization: Session <token>` en cada request y
deja un `auth.Principal` (cuenta, rol, lista de precios) en el contexto. `auth.RequireUser` y
`auth.RequireAdmin(ADMIN_TOKEN)` protegen rutas. Los contextos leen el principal del contexto, nunca
importan identity. La web guarda el token en la cookie httpOnly `dd_session` y lo reenvía desde
`Astro.locals.api` (cliente por request creado en `middleware.ts`), junto con la IP del cliente.

`internal/apitest` prueba la API completa por HTTP contra MySQL real (solo S3 simulado).

## Convenciones

- **Go**: `log/slog` JSON con `request_id`; errores con `%w`; `context.Context` como primer
  argumento; sin ORM (SQL explícito en adapters). Formato con `task api:fmt`.
- **Tests**: unitarios en `domain`/`application` con fakes de los ports. Integración en adapters
  con `internal/platform/mysqltest.Start(t)` (testcontainers, MySQL 8.4). `go test -short`
  salta los de integración.
- **SQL**: migraciones en `services/api/migrations/NNNN_nombre.{up,down}.sql`, embebidas en el
  binario. IDs `BINARY(16)` UUIDv7, fechas `DATETIME(6)` UTC, dinero `DECIMAL(12,2)` + `currency CHAR(3)`.
- **API**: el contrato es `services/api/openapi.yaml`. Errores en formato RFC 9457 (`Problem`)
  con `code` estable (`catalog.sku_taken`). Dinero en JSON como string decimal (`"85.00"`).
  CI falla si `apps/web/src/lib/api/schema.d.ts` no está regenerado.
- **Errores de dominio**: devuelve `apperr.Invalid/NotFound/Conflict(code, field, msg)`; el HTTP los
  mapea a 422/404/409. Al añadir un código nuevo, tradúcelo: errores que ve el cliente en
  `messages/{es,en,pt-br}.json` → `codes.<contexto>.<código>`; errores del panel en `es.json` →
  `admin.errors`. La web nunca muestra el mensaje en inglés del API.
- **Web**: textos siempre en `src/i18n/messages/{es,en,pt-br}.json`, nunca en el markup. `es` es la
  referencia; un test falla si en/pt-br no traducen todas las claves de la tienda (el admin
  solo existe en español). Islas (Preact) solo donde haya interacción; reciben los textos ya
  traducidos por props. Las páginas llaman al API solo desde el servidor (`src/lib/api/client.ts`);
  el navegador nunca ve el token de admin. Variables del servidor vía `astro:env/server`.
- **Formularios del admin**: HTML normal con Post/Redirect/Get (`intent` para varias acciones en una
  página); funcionan sin JS. Astro rechaza POST de formularios de otro origen (checkOrigin).
- **Accesibilidad**: contraste AA en los tokens de `global.css`, foco visible, `label` en cada
  campo, `aria-live` en resultados y precios, `fieldset/legend` por idioma.
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
11. **Variantes archivadas, nunca borradas**: precios, stock y pedidos las referencian.
12. **Las listas de precios de cliente son confidenciales**: `/v1/pricing/quotes` solo usa la lista
    predeterminada; desde la fase 3 la lista sale de la sesión, nunca de un parámetro.
13. **Precios sin IGV**: se muestran y guardan sin IGV; el pedido (fase 4) suma el 18 %.
    (Supuesto razonable para B2B en Perú; confirmar con Duradent.)
14. **Imágenes directas a S3** con URL prefirmada (PUT); el API nunca recibe los bytes.
15. **i18n manual** (`/[locale]/…` + middleware) en lugar del routing i18n de Astro, para que
    `/admin` y `/api` queden fuera del prefijo de idioma.
16. **Admin por rol de sesión** (fase 3). `ADMIN_TOKEN` queda solo para automatización.
17. **Sesiones opacas en MySQL**, no JWT: se pueden revocar (logout, recuperación de cuenta) y solo se
    guarda su SHA-256. 30 días.
18. **Admins por `ADMIN_EMAILS` con correo verificado**: un registro con contraseña (correo sin
    verificar) nunca da admin. Si alguien verifica un correo por Google/Microsoft y ya existía una
    cuenta sin verificar con ese correo, se le quita la contraseña y se cierran sus sesiones
    (evita que quien registró el correo ajeno conserve acceso).
19. **Microsoft**: su `email` no viene verificado salvo cuentas personales (tenant de consumidores) o
    el claim opcional `xms_edov`; el resto se trata como no verificado.
20. **`DEV_LOGIN` solo con `APP_ENV=local`** (la config falla si se activa en otro entorno).
21. **Rate limiting en memoria** por IP (20/min en endpoints de login) y por correo (5/min en login).
    Con N tareas el límite efectivo es N veces mayor; aceptable a esta escala.
22. **La redirect URI de OIDC sale de `WEB_BASE_URL`**, nunca del request; el `state` es de un solo uso
    (fila borrada en la misma transacción) y además va atado al navegador con la cookie `dd_oauth_state`.

## Estado por fases

- [x] Fase 1 — monorepo, Compose, CI
- [x] Fase 2 — catálogo, pricing, frontend i18n, admin de catálogo
- [x] Fase 3 — identidad (Google, Microsoft, email)
- [ ] Fase 4 — carrito, envíos, pedidos, pagos Culqi + transferencia
- [ ] Fase 5 — outbox relay, worker, inventario, emails, facturación
- [ ] Fase 6 — Terraform, ALB, ECS, despliegue
