# Plan: Ecommerce B2B de ortodoncia — Duradent SAC

## Contexto

Duradent SAC (Perú) necesita una tienda online para vender insumos de ortodoncia. El repo actual es el starter de Astro 7 sin modificar (`src/pages/index.astro`, `Welcome.astro`, `Layout.astro`), y se convertirá en el monorepo del proyecto.

La entrevista cambió el brief original en puntos de fondo. Decisiones tomadas:

| Tema | Brief | Decisión |
|---|---|---|
| Cliente | B2C | **B2B**: odontólogos y clínicas. Se pide RUC/COP en la cuenta, no bloquea la compra |
| Pagos | Stripe | **Culqi** (Stripe no acepta comercios peruanos). Tarjeta + Yape. Además **transferencia bancaria** con confirmación manual. El puerto `PaymentGateway` queda listo para un adaptador Stripe si algún día hay entidad extranjera |
| Precios | — | **Listas de precios por grupo de cliente**, solo en **soles (PEN)** (al inicio también USD; se quitó después, ver "Cambio posterior"). Sin login se ve la lista pública |
| Brasil / LATAM | Mercado | **Solo idioma** (es, en, pt-BR). Envíos solo a Perú; tarifa por zona (Lima, provincias, recojo en tienda) |
| Facturación | SUNAT | Vía **OSE/PSE (Nubefact)**: factura (01) si hay RUC, boleta (03) si no |
| Catálogo | — | Producto + **variantes** con atributos (prescripción, slot, pieza). Datos desde cero, cargados por el **panel admin** propio en Astro |
| Auth | Google + Microsoft | Google + Microsoft + **email/contraseña** |
| Despliegue | — | **Monolito modular** en Go: 1 binario con 3 modos (`api`, `worker`, `outbox-relay`). Equipo de 1-2 personas: todo gestionado por AWS |
| Email | SES, validar WorkMail | **SES** para transaccional. WorkMail es hosting de buzones (IMAP/calendario), no un servicio de envío programático; queda para `ventas@`, `soporte@` |
| Bus | SNS+SQS o NATS | **SNS + SQS**. Para 1-2 personas, NATS exige operar un clúster (JetStream, almacenamiento, upgrades). SNS+SQS es serverless, con DLQ nativa, IAM y sin servidores. Local: LocalStack |

---

## 1. Estructura de carpetas

```
duradent/
├── apps/
│   └── web/                        # Astro 7 SSR (tienda + /admin). El Astro actual se mueve aquí
│       ├── src/
│       │   ├── pages/[locale]/     # es | en | pt-BR. Rutas: /, catalogo/, producto/[slug], carrito, checkout, cuenta/
│       │   ├── pages/admin/        # productos, variantes, listas de precios, pedidos, pagos pendientes, envíos
│       │   ├── pages/api/          # endpoints BFF mínimos (proxy a Go con cookie de sesión)
│       │   ├── components/         # .astro (estáticos) + islands/ (variant selector, cart drawer, checkout form)
│       │   ├── layouts/
│       │   ├── i18n/               # es.json, en.json, pt-BR.json + utils (t(), formatMoney, formatDate)
│       │   ├── lib/api-client.ts   # cliente tipado del API Go (OpenAPI → tipos)
│       │   └── middleware.ts       # locale, sesión, guard /admin
│       ├── astro.config.mjs        # adapter node standalone, i18n routing
│       └── Dockerfile
├── services/
│   └── api/                        # Go 1.23+, módulo único
│       ├── cmd/
│       │   └── duradent/main.go    # subcomandos: api | worker | outbox-relay | migrate
│       ├── internal/
│       │   ├── catalog/            # cada bounded context sigue el mismo layout:
│       │   │   ├── domain/         #   entidades, value objects, eventos, errores. Sin imports externos
│       │   │   ├── application/    #   casos de uso (commands/queries), orquestan dominio + puertos
│       │   │   ├── ports/          #   interfaces: repos, gateways, publishers
│       │   │   └── adapters/       #   mysql/, http/ (handlers), culqi/, nubefact/, ses/, sqs/...
│       │   ├── cart/
│       │   ├── orders/
│       │   ├── payments/
│       │   ├── inventory/
│       │   ├── identity/           # clientes, cuentas, OAuth, sesiones, roles
│       │   ├── pricing/            # listas de precios (separado de catálogo: cambia a ritmo distinto)
│       │   ├── shipping/           # zonas, tarifas, guías
│       │   ├── invoicing/          # SUNAT vía Nubefact
│       │   ├── notifications/      # plantillas + SES
│       │   └── platform/           # transversal: httpserver, mysql (tx, outbox), sns, sqs, config, log, auth middleware, idempotency
│       ├── migrations/             # SQL, golang-migrate
│       ├── openapi.yaml            # contrato; genera tipos TS para apps/web
│       ├── Dockerfile
│       └── .golangci.yml
├── infra/
│   └── terraform/
│       ├── modules/{network,ecs-service,rds-mysql,alb,messaging,s3-cdn,ses,secrets}
│       └── envs/{staging,prod}
├── docker-compose.yml              # mysql, localstack (sns/sqs/s3), mailpit, api, worker, relay, web
├── .github/workflows/{ci.yml,deploy.yml}
├── Makefile                        # make dev | test | lint | migrate | seed
├── CLAUDE.md
└── AGENTS.md (existente)
```

Regla de dependencias (verificada en CI con `depguard`): `domain` no importa nada fuera de stdlib; `application` importa `domain` y `ports`; `adapters` importan todo; ningún contexto importa `internal/<otro>/domain` — se comunican por eventos o por `application` del otro contexto a través de un puerto.

---

## 2. Arquitectura

```
                 Internet
                    │
            ┌───────▼────────┐
            │  CloudFront    │  ← imágenes S3, assets estáticos
            └───────┬────────┘
                    │
            ┌───────▼────────┐   /api/*  ──────────────┐
            │      ALB       │                         │
            │   (multi-AZ)   │   /*      ──────┐       │
            └────────────────┘                 │       │
                                               ▼       ▼
                              ┌──────────────────┐  ┌──────────────────┐
                              │  ECS: web        │  │  ECS: api        │
                              │  Astro SSR node  │─▶│  Go  (api mode)  │
                              │  (2+ tasks)      │  │  (2+ tasks)      │
                              └──────────────────┘  └────────┬─────────┘
                                                             │ tx: estado + outbox
                                                             ▼
                                                   ┌──────────────────┐
                                                   │  RDS MySQL 8     │
                                                   │  multi-AZ        │
                                                   └────────┬─────────┘
                                                            │ poll outbox
                                                   ┌────────▼─────────┐
                                                   │ ECS: outbox-relay│  (1 task, lock por fila)
                                                   └────────┬─────────┘
                                                            │ publish
                                                   ┌────────▼─────────┐
                                                   │ SNS domain-events│
                                                   └──┬────┬────┬─────┘
                                      filter policy   │    │    │
                             ┌────────────────────────┘    │    └──────────────────────┐
                    ┌────────▼────────┐          ┌─────────▼────────┐          ┌───────▼─────────┐
                    │ SQS inventory   │          │ SQS notifications│          │ SQS invoicing   │
                    │   + DLQ         │          │   + DLQ          │          │   + DLQ         │
                    └────────┬────────┘          └─────────┬────────┘          └───────┬─────────┘
                             └─────────────────────────────┼───────────────────────────┘
                                                  ┌────────▼─────────┐
                                                  │ ECS: worker      │  consumidores idempotentes
                                                  │ Go (worker mode) │  (tabla processed_messages)
                                                  └──┬──────┬────────┘
                                                     │      │
                                               ┌─────▼┐  ┌──▼──────┐
                                               │ SES  │  │Nubefact │
                                               └──────┘  └─────────┘

 Externos síncronos desde api:  Culqi (cargo + webhook ──▶ /api/webhooks/culqi)
                                Google / Entra ID (OIDC code flow + PKCE)
 Secretos: Secrets Manager → env en task definition.  Logs: CloudWatch (JSON, slog).  Métricas: Prometheus-style /metrics + CW.
```

**Flujo de pedido con pago online:**
`POST /checkout` → application `PlaceOrder`: valida carrito, resuelve lista de precios del cliente, calcula envío por zona, reserva stock, crea `Order(PendingPayment)` + `Payment(Created)` + evento `OrderPlaced` en **una transacción** (outbox). Responde con `culqi_charge_token` del lado cliente → Culqi → webhook `charge.succeeded` → verifica firma + `Idempotency-Key` → `ConfirmPayment` → `Order(Paid)` + `PaymentConfirmed` (outbox). Worker: `inventory` confirma reserva, `invoicing` emite en Nubefact, `notifications` envía correo.

**Transferencia bancaria:** mismo flujo hasta `PendingPayment`; el pedido queda esperando. Admin marca "depósito recibido" → `ConfirmPayment` por la misma ruta. Reserva de stock expira a las 48h (`ReservationExpired`) si no se confirma.

**Autenticación:** el API Go hace el code flow OIDC (`coreos/go-oidc`), crea sesión opaca en MySQL (`sessions`), cookie `HttpOnly; Secure; SameSite=Lax`. Astro middleware reenvía la cookie al API; `/admin` exige rol `admin`. Email/contraseña con Argon2id.

---

## 3. Modelo de datos (MySQL 8, InnoDB, utf8mb4)

Monedas como `DECIMAL(12,2)` + `currency CHAR(3)`. Todas las tablas: `id BINARY(16)` (UUIDv7), `created_at`, `updated_at`.

**identity**
- `accounts` — email (unique), password_hash (nullable), display_name, role (`customer|admin`), status
- `oauth_identities` — account_id, provider (`google|microsoft`), subject (unique con provider), email
- `sessions` — id (token hash), account_id, expires_at, ip, user_agent
- `customers` — account_id (1:1), customer_type (`persona|empresa`), document_type (`DNI|RUC|CE`), document_number, business_name, cop_number (nullable), price_list_id (FK pricing), phone
- `addresses` — customer_id, label, line1, district, province, department, ubigeo (6 dígitos, lo exige SUNAT/Nubefact), reference, is_default

**catalog**
- `categories` — slug (unique), parent_id, sort_order
- `category_translations` — category_id, locale, name, description
- `products` — slug (unique), brand, status (`draft|active|archived`), category_id
- `product_translations` — product_id, locale, name, description, seo_title
- `attributes` — code (`prescription|slot|tooth|size|material`) ; `attribute_values` — attribute_id, code, label por locale
- `variants` — product_id, sku (unique), barcode, weight_g, is_default
- `variant_attribute_values` — variant_id, attribute_value_id
- `media` — product_id, s3_key, alt (json por locale), sort_order

**pricing**
- `price_lists` — code (`public|clinica|distribuidor`), name, is_default
- `prices` — price_list_id, variant_id, currency, amount, min_qty (default 1) — unique (price_list_id, variant_id, currency, min_qty)

**inventory**
- `stock_items` — variant_id (unique), on_hand, reserved, reorder_point
- `stock_reservations` — order_id, variant_id, qty, status (`active|confirmed|released|expired`), expires_at
- `stock_movements` — variant_id, qty_delta, reason (`purchase|sale|adjustment|return`), reference_id

**cart**
- `carts` — customer_id (nullable, carrito anónimo), currency, expires_at
- `cart_items` — cart_id, variant_id, qty — unique (cart_id, variant_id)

**orders**
- `orders` — number (secuencia legible `DD-000123`), customer_id, status (`pending_payment|paid|preparing|shipped|delivered|cancelled`), currency, subtotal, shipping_amount, tax_amount (IGV 18%), total, shipping_address (json snapshot), billing (json snapshot: doc type/number/razón social), notes
- `order_items` — order_id, variant_id, sku, name (snapshot), unit_price, qty, line_total
- `order_status_history` — order_id, from, to, actor, at

**payments**
- `payments` — order_id, method (`culqi_card|culqi_yape|bank_transfer`), status (`created|authorized|confirmed|failed|refunded`), amount, currency, provider_ref (charge id), confirmed_by (account_id admin, para transferencia), confirmed_at
- `payment_webhook_events` — provider, event_id (unique), payload json, processed_at — idempotencia de webhooks

**shipping**
- `shipping_zones` — code (`lima|provincias|pickup`), name ; `shipping_zone_ubigeos` — zone_id, ubigeo_prefix
- `shipping_rates` — zone_id, max_weight_g, amount, currency
- `shipments` — order_id, carrier, tracking_number, status, shipped_at, delivered_at

**invoicing**
- `invoices` — order_id, type (`01 factura|03 boleta`), series, number, status (`pending|accepted|rejected`), provider_ref, sunat_hash, pdf_url, xml_url, response json

**notifications**
- `email_log` — template, to, order_id, ses_message_id, status

**platform**
- `outbox` — id, aggregate_type, aggregate_id, event_type, payload json, occurred_at, published_at (null), attempts, locked_until. Índice (published_at, occurred_at)
- `processed_messages` — consumer, message_id — PK compuesta (idempotencia de consumidores)
- `idempotency_keys` — key, request_hash, response json, expires_at (para POST de checkout)

---

## 4. Catálogo de eventos de dominio

Envelope común: `{id, type, version, occurred_at, aggregate_type, aggregate_id, correlation_id, payload}`. Publicados a SNS con `MessageAttributes.type` para filter policies.

| Evento | Productor | Payload (resumen) | Consumidores |
|---|---|---|---|
| `OrderPlaced` | orders | order_id, customer_id, items[{variant_id, qty}], total, currency, payment_method | inventory (reserva ya hecha en tx; confirma consistencia), notifications (correo "pedido recibido / instrucciones de transferencia") |
| `PaymentConfirmed` | payments | order_id, payment_id, method, amount | orders (→ `paid`), inventory (reserva → confirmada, on_hand −qty), invoicing (emitir), notifications (correo de pago) |
| `PaymentFailed` | payments | order_id, payment_id, reason | orders (sigue pending, registra), notifications |
| `StockReserved` | inventory | order_id, reservations[] | — (auditoría) |
| `StockReservationExpired` | inventory (job) | order_id, variant_ids | orders (→ `cancelled`), notifications |
| `StockReleased` | inventory | order_id | — |
| `StockLow` | inventory | variant_id, on_hand, reorder_point | notifications (correo a admin) |
| `OrderCancelled` | orders | order_id, reason | inventory (liberar), payments (reembolso si pagado), notifications |
| `OrderShipped` | shipping | order_id, carrier, tracking_number | orders (→ `shipped`), notifications |
| `OrderDelivered` | shipping | order_id | orders (→ `delivered`) |
| `InvoiceIssued` | invoicing | order_id, invoice_id, type, series-number, pdf_url | notifications (adjunta PDF) |
| `InvoiceRejected` | invoicing | order_id, error | notifications (admin) |
| `CustomerRegistered` | identity | customer_id, email, locale | notifications (bienvenida) |
| `PriceListAssigned` | pricing (admin) | customer_id, price_list_id | notifications (opcional) |

Reglas: consumidores idempotentes por `(consumer, message_id)`; reintentos con backoff de SQS (visibility timeout) y `maxReceiveCount=5` → DLQ; alarma CloudWatch si DLQ > 0.

---

## 5. Plan por fases

Cada fase termina con: compila, `golangci-lint` + `astro check` limpios, tests verdes, `docker compose up` funcional, CLAUDE.md actualizado.

### Fase 1 — Esqueleto del monorepo, Compose, CI
- Mover el Astro actual a `apps/web`; crear `services/api` (Go), `infra/terraform` (vacío con README), `Makefile`.
- Go: `cmd/duradent` con subcomandos (cobra), `internal/platform/{config,log(slog),httpserver,mysql,health}`, `GET /healthz`, `/readyz`, `/metrics`.
- Migraciones con golang-migrate; migración 0001 crea `outbox`, `processed_messages`, `idempotency_keys`.
- `docker-compose.yml`: mysql 8, localstack (sns, sqs, s3), mailpit, api, web (hot reload con `air` y `astro dev`).
- `openapi.yaml` inicial + generación de tipos TS (`openapi-typescript`).
- CI (`ci.yml`): lint + test Go (con testcontainers MySQL), `astro check` + build, build de imágenes Docker.
- `CLAUDE.md` con comandos, convenciones hexagonales, regla de dependencias, ADRs iniciales (Culqi, SNS+SQS, monolito modular, SES).

### Fase 2 — Catálogo + frontend base con i18n + admin de catálogo
- Contextos `catalog` y `pricing` (dominio, casos de uso, repos MySQL, handlers HTTP). Tests unitarios de dominio, integración con testcontainers.
- Media: subida a S3 (LocalStack en local) con URLs firmadas; CloudFront en Fase 6.
- Astro: adapter node standalone, i18n routing `es` (default) / `en` / `pt-BR`, archivos de traducción, `formatMoney(amount, currency, locale)`, layout base con Tailwind, diseño claro/clínico, WCAG AA (contraste, focus, landmarks).
- Páginas: home, listado por categoría, ficha de producto con selector de variantes (island), precios de la lista `public` en PEN/USD (toggle de moneda en cookie).
- `/admin` (aún sin auth real; guard por variable de entorno en local): CRUD productos, variantes, atributos, listas de precios, carga de imágenes.
- Seed con ~20 productos reales de ortodoncia para desarrollo.

### Fase 3 — Identidad: Google, Microsoft, email/contraseña, cuentas de cliente
- Contexto `identity`: OIDC code flow + PKCE con `coreos/go-oidc` (Google, Entra ID multi-tenant `common`), registro con email + Argon2id, sesiones en MySQL, cookies seguras, CSRF en formularios.
- Perfil de cliente: tipo persona/empresa, DNI/RUC, razón social, COP, direcciones con ubigeo (tabla de ubigeos cargada por seed).
- Roles `customer|admin`; `/admin` protegido de verdad; asignación de lista de precios a cliente desde admin → `PriceListAssigned`.
- Precios en la tienda según lista del cliente logueado.
- Rate limiting en login/registro (`platform/ratelimit`, en memoria por task + cabeceras ALB).

### Fase 4 — Carrito, envíos, pedidos y pagos (Culqi + transferencia)
- `cart` (anónimo y por cliente, merge al loguear), `shipping` (zonas, tarifas por peso, recojo en tienda), `orders` (`PlaceOrder` transaccional con outbox e `Idempotency-Key`), `inventory` (reserva en la misma tx).
- `payments`: puerto `PaymentGateway`; adaptador Culqi (cargo con token, Yape), webhook con verificación de firma + tabla `payment_webhook_events`; método `bank_transfer` con confirmación en admin.
- Checkout en Astro (island): resumen, dirección, método de pago, Culqi Checkout JS.
- Admin: pedidos, cambio de estado, confirmar transferencia, registrar guía de envío.
- Tests: casos de uso de pedido con fakes de puertos; integración de webhook Culqi con payloads grabados.

### Fase 5 — Eventos, inventario, notificaciones, facturación SUNAT
- `outbox-relay` (polling con `SELECT … FOR UPDATE SKIP LOCKED`, publica a SNS, marca `published_at`).
- `worker`: consumidores SQS idempotentes, backoff, DLQ. Un consumer group por contexto.
- `inventory`: confirmación/liberación por eventos, job de expiración de reservas, `StockLow`.
- `notifications`: plantillas HTML por locale (es/en/pt-BR), adaptador SES (Mailpit en local), `email_log`.
- `invoicing`: adaptador Nubefact (factura/boleta, IGV, ubigeo, CDR, PDF/XML a S3), reintentos, `InvoiceIssued/Rejected`.
- Cancelación de pedido y reembolso Culqi.

### Fase 6 — Terraform, ALB, ECS Fargate, despliegue
- Módulos: VPC multi-AZ (2 públicas, 2 privadas, NAT), ALB con reglas `/api/*` → api, resto → web, ACM + HTTPS, RDS MySQL 8 multi-AZ, ECS Fargate (web, api, worker, relay) con autoscaling por CPU, SNS/SQS/DLQ, S3 + CloudFront (OAC), SES (dominio verificado, DKIM), Secrets Manager, CloudWatch logs/alarms (5xx ALB, DLQ, CPU RDS).
- `deploy.yml`: build + push a ECR, `terraform plan` en PR, `apply` manual a staging/prod, migraciones como task one-off antes del rollout.
- Runbook en CLAUDE.md: despliegue, rollback, rotación de secretos, ver DLQ.

---

## 6. Librerías y herramientas

**Go:** stdlib `net/http` (router 1.22+), `sqlx` + `golang-migrate`, `slog`, `aws-sdk-go-v2` (sns, sqs, s3, ses, secretsmanager), `coreos/go-oidc` + `x/oauth2`, `alexedwards/argon2id`, `google/uuid` (v7), `testcontainers-go`, `golangci-lint` (+ `depguard`), `air` para hot reload. Culqi y Nubefact: clientes HTTP propios en `adapters/` (SDKs oficiales inmaduros).

**Web:** Astro 7 + `@astrojs/node`, Tailwind, islands en Preact (ligero) solo para selector de variantes, carrito y checkout; `openapi-typescript` para el cliente tipado. Verificar nombres exactos de adaptador e i18n de Astro 7 con `ctx7` al iniciar la Fase 1.

---

## 7. Verificación

- **Por fase:** `make lint test` (Go + web) y `docker compose up` → smoke manual en el navegador integrado: navegar catálogo en 3 locales, login con cada proveedor, pedido completo con Culqi sandbox y con transferencia, correo recibido en Mailpit, factura de prueba en Nubefact demo.
- **Eventos:** test de integración que inserta en `outbox`, corre relay contra LocalStack y comprueba que el consumer procesa una vez aunque el mensaje llegue dos veces.
- **Seguridad:** test de webhook con firma inválida → 400; replay de `event_id` → 200 sin efectos; `Idempotency-Key` repetida en checkout → mismo pedido.
- **Fase 6:** `terraform plan` sin drift, healthchecks verdes en ALB, pedido real en staging con tarjeta de prueba Culqi.

## Pendientes a confirmar durante la implementación (no bloquean el plan)
- Cuentas que Duradent debe abrir: Culqi (sandbox hoy, producción al lanzar), Nubefact, AWS, Google Cloud (OAuth client), Entra ID (app registration).
- Series de facturación (F001/B001) y si usan tienda física que ya emite comprobantes.
- Si DIGEMID exige alguna advertencia o restricción de venta para algún producto del catálogo.

---

## Cambios durante la implementación

- **Fase 1**: subcomandos con `switch` en `main.go` en lugar de cobra (no hacía falta la dependencia).
  MySQL local en el puerto **3307** (el 3306 suele estar ocupado). Taskfile en lugar de Makefile.
- **Fase 2**:
  - Rutas de la tienda en inglés neutro: `/{locale}/catalog`, `/{locale}/catalog/{categoría}`,
    `/{locale}/product/{slug}` (el plan decía `catalogo/producto`).
  - Variantes archivadas en vez de borradas (`variants.archived_at`).
  - Precios: `prices` con PK `(price_list_id, variant_id, currency, min_qty)`; sin FK a `variants`
    para no acoplar contextos.
  - Atributos guardan nombres/etiquetas como JSON por idioma (`attributes.names`, `attribute_values.labels`).
  - Precios mostrados **sin IGV** (pendiente de confirmar con Duradent).
  - El endpoint público de precios solo expone la lista predeterminada; las listas de cliente
    llegan por sesión en la fase 3.
  - Admin protegido con token temporal hasta la fase 3.
- **Fase 3**:
  - Acceso de desarrollo (`DEV_LOGIN`, solo local) para probar sin credenciales de Google.
  - `customers.price_list_code` guarda el código de la lista (sin FK a pricing).
  - Direcciones con código de departamento INEI y ubigeo opcional; el catálogo completo de ubigeos
    se cargará si la fase 4 lo necesita para las zonas de envío.
  - El evento `CustomerRegistered` se publicará en la fase 5 (outbox).
  - Verificación de correo para cuentas con contraseña: fase 5 (necesita SES).
- **Fase 4**:
  - `payments` vive dentro del contexto `orders` (no como contexto aparte).
  - Culqi no firma webhooks: se verifica releyendo el cargo en su API (ver ADR 25 en CLAUDE.md).
  - El stock se confirma/libera de forma síncrona desde `orders` (por ports) en vez de por eventos;
    los eventos se escriben igual en el outbox para notificaciones y facturación (fase 5).
  - El job de expiración corre en el proceso `worker`.
  - Tarifas de envío sembradas en la migración 0006 (editables por SQL; falta pantalla de admin).
  - Cancelación de pedidos pagados y reembolsos Culqi: fase 5.
- **Fase 5**:
  - Solo `notifications` e `invoicing` consumen eventos (colas `duradent-notifications` y
    `duradent-invoicing` con filter policy); inventario se confirma/libera/repone de forma síncrona.
  - Reembolsos en el contexto `orders` (`Refund`), con reposición de stock (`Restock`).
  - Tablas nuevas (migración 0008): `email_log`, `invoices`, `invoice_series`, `email_verifications`;
    `orders.refunded_at/refund_ref`, estado de pago `refunded`, reserva `returned`.
  - PDF/XML de comprobantes: se enlazan los de Nubefact (no se copian a S3 todavía).
  - El correo de stock bajo muestra el id de la variante (el evento `StockLow` aún no lleva SKU).
  - Tipo de cambio USD por configuración (`USD_EXCHANGE_RATE`), no por API de la SBS.
- **Fase 6**:
  - Región `us-east-1`; dominio supuesto `duradent.pe` (configurable).
  - Las versiones no se despliegan con `terraform apply` sino con `infra/scripts/deploy.sh`
    (Terraform crea la infraestructura y la primera revisión de cada tarea).
  - El ALB solo enruta `/v1/webhooks/*` al API; la web llama al API por ECS Service Connect.
  - El plan de los PRs usa un rol de solo lectura y los tfvars guardados junto al estado en S3.
  - Sin aplicar todavía: falta la cuenta AWS de Duradent (validado con `terraform validate`).

## Cambio posterior: solo soles

Duradent decidió vender solo en soles. Se quitó el dólar en todas las capas: el selector PEN/USD y la
cookie de moneda de la tienda, las columnas USD del admin de precios, los precios USD del seed, la
tarifa de envío en USD (migración 0009, que además borra los precios en USD), el tipo de cambio de la
facturación (`USD_EXCHANGE_RATE`, también en Terraform) y el formato en dólares de los correos. La
moneda sigue en el modelo de datos y en el API (siempre `PEN`) por si algún día vuelve. Ver ADR 46 en
`CLAUDE.md`.
