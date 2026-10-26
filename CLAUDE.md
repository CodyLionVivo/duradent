# Duradent — tienda B2B de ortodoncia

Monorepo de la tienda online de Duradent SAC (Perú). Clientes: odontólogos y clínicas.
Plan completo y modelo de datos: ver `docs/plan.md`.

@AGENTS.md

## Estructura

```
apps/web/          Astro 7 SSR (adapter node standalone): tienda + /admin
services/api/      Go: un binario `duradent` con modos api | worker | outbox-relay | migrate | seed
                   (internal/wiring arma la app; internal/apitest la prueba end-to-end)
infra/local/       Init de LocalStack (SNS, SQS+DLQ, S3) para docker compose
infra/terraform/   AWS: shared (ECR, roles de GitHub), envs/{staging,prod}, modules/*
infra/scripts/     deploy.sh (migración + rollout de una imagen)
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
| Validar Terraform (sin AWS) | `task tf:validate` · formatear: `task tf:fmt` |
| Plan de un entorno | `task tf:plan -- staging` (necesita credenciales y `backend.hcl`) |
| Desplegar una imagen | `task deploy -- staging <sha>` (normalmente lo hace GitHub Actions) |

URLs locales: web http://localhost:4321 · API http://localhost:8080 (`/healthz`, `/readyz`, `/metrics`) ·
Mailpit http://localhost:8025 · LocalStack http://localhost:4566 · MySQL `localhost:3307` (duradent/duradent; cambia con `MYSQL_HOST_PORT`).

El servidor de desarrollo de Astro fuera de Docker: `astro dev --background` (ver AGENTS.md).

**Tienda**: `/{es|en|pt-br}`, `/…/catalog`, `/…/catalog/{categoría}`, `/…/product/{slug}`,
`/…/cart`, `/…/checkout`, `/…/orders`, `/…/account` (login, registro, perfil), `/…/complaints-book`
(Libro de Reclamaciones; aviso en el pie de todas las páginas). `/` redirige según `Accept-Language`.
Admin del libro: `/admin/complaints`.

**Pagos en local**: sin `CULQI_SECRET_KEY` el API usa una pasarela simulada y la página del pedido
muestra botones "Pago aprobado / Tarjeta rechazada / Yape". Con llaves sandbox de Culqi
(`CULQI_PUBLIC_KEY=pk_test_…`, `CULQI_SECRET_KEY=sk_test_…` antes de `task up`) se abre el Culqi
Checkout real. Webhook a configurar en CulqiPanel → Eventos → Webhooks:
`https://<api>/v1/webhooks/culqi/<CULQI_WEBHOOK_SECRET>` (evento de cargos).

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
**pricing** (listas de precios con tramos por volumen), **identity** (cuentas, OIDC Google/Microsoft,
contraseña Argon2id, sesiones, perfil de facturación RUC/DNI/CE, direcciones, roles), **inventory**
(stock, reservas por pedido, movimientos), **shipping** (zonas Lima/Provincias/Recojo y tarifas por
peso), **orders** (carrito, pedidos, pagos Culqi y transferencia, reembolsos),
**notifications** (correos transaccionales es/en/pt-br por SMTP/Mailpit o SES, `email_log`),
**invoicing** (facturas, boletas y notas de crédito SUNAT vía Nubefact, series correlativas) y
**complaints** (Libro de Reclamaciones virtual: hojas numeradas por año, respuesta en 15 días hábiles).

**Composición**: `internal/wiring` construye todos los contextos y monta las rutas; lo usan
`cmd/duradent` y `internal/apitest`, así los tests ejercitan exactamente el mismo cableado. Los
*bridges* de `wiring/bridges.go` y `wiring/event_bridges.go` implementan los ports de `orders`,
`notifications`, `invoicing` e `identity` sobre los servicios de los otros contextos: es el único lugar
donde los contextos se tocan.

**Eventos**: `outbox-relay` publica las filas del outbox en SNS (`platform/outbox.Relay`, lotes de 50
con `FOR UPDATE SKIP LOCKED`, backoff exponencial hasta 10 min por fila fallida). El `worker` consume
una cola SQS por contexto (`platform/messaging.Consumer`: long polling, idempotencia por
`processed_messages`, error → reintento y DLQ tras 5 recepciones). Cada contexto declara los tipos que
consume (`notifications/application.Events`, `invoicing/application.Events`), que deben coincidir con las
filter policies de `infra/local/localstack/init-aws.sh` (y de Terraform; `wiring/events_test.go` lo
comprueba). En una LocalStack ya creada, una policy nueva no se aplica sola: `task reset && task up`
o vuelve a correr el script dentro del contenedor. El worker además corre cada
minuto la expiración de pedidos impagos y el reintento de comprobantes pendientes.

**Ver correos y eventos en local**: Mailpit http://localhost:8025 recibe todos los correos. Comprobantes
simulados salvo que exportes `NUBEFACT_URL`/`NUBEFACT_TOKEN` (cuenta demo de Nubefact). Clientes con
"rechazo" en la razón social simulan un rechazo del OSE. Colas:
`docker compose exec localstack awslocal sqs get-queue-attributes --queue-url http://localhost:4566/000000000000/duradent-notifications-dlq --attribute-names ApproximateNumberOfMessages`.

**Unidad de trabajo**: `platform/mysql.UnitOfWork.InTx` deja la transacción en el `context`; los
repositorios usan `mysql.Conn(ctx, db)`, así un pedido, su reserva de stock y su evento en el outbox
se confirman juntos sin que un contexto conozca al otro. Eventos: `platform/outbox.Writer.Record`.

**Autenticación**: `internal/platform/auth` resuelve `Authorization: Session <token>` en cada request y
deja un `auth.Principal` (cuenta, rol, lista de precios) en el contexto. `auth.RequireUser` y
`auth.RequireAdmin(ADMIN_TOKEN)` protegen rutas. Los contextos leen el principal del contexto, nunca
importan identity. La web guarda el token en la cookie httpOnly `dd_session` y lo reenvía desde
`Astro.locals.api` (cliente por request creado en `middleware.ts`), junto con la IP del cliente.

`internal/apitest` prueba la API completa por HTTP contra MySQL real (S3, Culqi, correo y Nubefact
simulados); `drain` hace de relay + worker para probar los consumidores. `platform/messaging` tiene un
test con LocalStack real (outbox → SNS con filtro → SQS → consumidor, reintento y duplicados).

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
- **Textos legales**: Markdown por idioma en `apps/web/src/content/legal/<locale>/{terms,privacy,returns}.md` (colección `legal`, ruta `/{locale}/legal/{slug}`). El español es la versión que prevalece; al cambiar uno, actualiza los tres idiomas y la fecha `updated`. Teléfono de la tienda en `src/lib/legal.ts`.
- **Fechas**: la web las muestra en hora de Lima (`TIME_ZONE` en `i18n/locales.ts`) aunque el servidor
  esté en UTC; usa `formatDate`/`formatDay`, nunca `Intl.DateTimeFormat` sin `timeZone`.
- **Imágenes propias**: en `apps/web/src/assets/` (las optimiza `astro:assets`, p. ej. la foto de la tienda del hero). El logotipo y los iconos están en `public/brand/`.
- **Efectos visuales**: solo animar `transform`/`opacity`; nada de `filter: blur` ni `backdrop-filter` sobre elementos animados (hacían lento el scroll del inicio). Las animaciones del hero se pausan fuera de pantalla y durante el scroll.
- **Accesibilidad**: contraste AA en los tokens de `global.css`, foco visible, `label` en cada
  campo, `aria-live` en resultados y precios, `fieldset/legend` por idioma.
- **Config**: solo variables de entorno (`internal/platform/config`). En AWS los secretos llegan
  desde Secrets Manager al entorno de la tarea ECS. Nunca credenciales en el repo.
- **Commits**: una fase o un cambio coherente por commit; ramas `fase-N-…`.

## Decisiones de arquitectura (ADR)

1. **Pagos con Culqi, no Stripe.** Stripe no acepta comercios constituidos en Perú. Culqi cubre
   tarjetas y Yape en soles (ver ADR 46). El puerto `PaymentGateway` permite añadir Stripe si existe una
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
23. **Pagos dentro del contexto `orders`**: el ciclo de vida del pago está atado al pedido; Culqi queda
    detrás del port `PaymentGateway` (adaptadores `culqi` y `fakegateway`).
24. **Cobro en tres pasos**: transacción corta que marca el cargo "en vuelo" (`charging_until`, 90 s),
    llamada a Culqi fuera de transacción, y transacción que confirma o registra el rechazo. Si la
    respuesta de Culqi se pierde, el pedido queda en "verificando" y el webhook lo concilia; el
    cliente no puede cobrar dos veces mientras tanto.
25. **Webhook de Culqi sin firma**: Culqi no firma sus webhooks. La URL lleva un secreto y el API
    vuelve a leer el cargo en Culqi (`GET /v2/charges/{id}`) antes de cambiar nada. Un cargo
    confirmado se registra una sola vez (`payments.confirmed_ref` único), así las repeticiones no dañan.
26. **3-D Secure**: un cargo que Culqi devuelve con `action_code: REVIEW` se trata como no pagado
    (`3ds_required`). Integrar Culqi 3DS queda pendiente si los rechazos lo justifican.
27. **Stock reservado al crear el pedido** (misma transacción) y vendido al pagar. Pedidos sin pagar
    se cancelan solos: 2 h con Culqi, 48 h con transferencia (job del `worker` cada minuto). Las filas
    de stock se bloquean en orden de `variant_id` para evitar deadlocks.
28. **IGV 18 % sobre productos + envío**, redondeo al céntimo hacia arriba en la mitad. Los precios se
    guardan y muestran sin IGV.
29. **Idempotency-Key en `POST /v1/orders`**: la web genera una por carga del checkout; reintentos o
    doble clic devuelven el mismo pedido.
30. **Pedidos pagados se cancelan con reembolso** (admin, `POST /v1/admin/orders/{id}/refund`): pagados o
    en preparación, nunca enviados. Culqi: `POST /v2/refunds` fuera de la transacción (un segundo intento
    lo rechaza Culqi). Transferencia: devolución manual y el admin anota el n.º de operación. El stock
    vuelve al inventario (reservas `returned`, movimiento `return`) y `OrderRefunded` dispara la nota de
    crédito y el correo. Un pago que llegó tras la expiración del pedido también se puede reembolsar.
31. **Yape**: solo PEN y hasta S/ 2 000 por pago (límite de Culqi); se valida antes de cobrar.
32. **Solo consumen eventos `notifications` e `invoicing`**. Inventario y pagos se coordinan de forma
    síncrona dentro de la transacción del pedido (ports); las colas `orders`, `inventory` y `payments`
    del plan no se crean hasta que un consumidor las necesite.
33. **Entrega al menos una vez**: el relay puede publicar dos veces (si muere entre publicar y marcar) y
    el consumidor registra el mensaje después de su efecto. Por eso cada efecto externo es idempotente
    por su cuenta: `email_log` único por (evento, tipo, destinatario) y comprobantes únicos por
    (pedido, tipo).
34. **Comprobantes: número antes de llamar al OSE.** La serie se incrementa en la misma transacción que
    crea la fila `pending`; un reintento envía el mismo número. Si Nubefact responde "ya existe"
    (código 23) se consulta con `consultar_comprobante`. Errores de datos → `rejected` + alerta a
    `OPS_EMAILS` + botón "Reintentar" en el admin; errores de red → reintento (SQS y job cada minuto,
    máx. 10 intentos).
35. **Factura si el cliente pidió factura con RUC; si no, boleta.** Series por defecto F001/B001 y notas
    de crédito FC01/BC01 (`INVOICE_SERIES_*`). El IGV se calcula por línea y la diferencia de redondeo
    va a la línea mayor, así las líneas suman exactamente el IGV cobrado. El envío va como línea de
    servicio (`ZZ`). Todos los comprobantes van en soles (`moneda` 1), sin tipo de cambio.
36. **El enlace de verificación de correo no viaja por el bus**: identity lo envía en el momento a través
    del mailer de notifications (bridge). Token aleatorio de 32 bytes, solo se guarda su SHA-256, vale
    24 h, un uso, máx. 3 envíos por hora. La página `/…/account/verify` confirma con POST (los escáneres
    de enlaces del correo no lo consumen). Verificar un correo listado en `ADMIN_EMAILS` da admin.
37. **`CustomerRegistered` en la misma transacción que la cuenta** (`Accounts.Create` se une a la unidad
    de trabajo).
38. **Terraform para la infraestructura, `deploy.sh` para las versiones.** Los servicios ECS ignoran
    cambios de `task_definition`; el script copia la última revisión de cada familia con la imagen
    nueva, corre `migrate` como tarea única y solo si sale bien actualiza api → worker → relay → web.
    El circuit breaker de ECS revierte un rollout que no arranca. Imágenes con tag inmutable (SHA).
39. **Solo el webhook de Culqi entra por el ALB al API** (`POST /v1/webhooks/*`); todo lo demás va a la
    web, que llama al API dentro de la VPC por ECS Service Connect (`http://api:8080`). El navegador
    nunca habla con el API.
40. **IP del visitante detrás del ALB**: la web toma la **última** entrada de `X-Forwarded-For` (la que
    añade el ALB) con `TRUST_PROXY=true` y se la pasa al API, que confía en ella
    (`TRUST_PROXY_HEADERS`) porque solo la web y el webhook llegan a él.
41. **TLS a RDS verificado**: `require_secure_transport=1`, la imagen incluye el bundle de CA de RDS y el
    DSN usa `tls=rds` (`MYSQL_CA_FILE`). La contraseña la genera Terraform y el DSN completo vive en
    Secrets Manager (`duradent-<env>/mysql-dsn`).
42. **Un secreto JSON por entorno para terceros** (`duradent-<env>/app`): Terraform crea el contenedor y
    operaciones carga los valores; ECS los inyecta por clave. Nada sensible en tfvars ni en el estado.
43. **GitHub Actions entra a AWS por OIDC**: `duradent-github-deploy` (solo `main` y los entornos
    `staging`/`prod`) y `duradent-github-plan` (solo lectura, PRs). Sin llaves de AWS guardadas.
44. **Staging barato**: un NAT gateway, RDS single-AZ `db.t4g.micro`, una tarea por servicio, sin
    protección de borrado. Prod: NAT por AZ, RDS Multi-AZ con protección y snapshot final, mínimo dos
    tareas de web y API con autoescalado por CPU (60 %).
45. **Imágenes por CloudFront con OAC** (bucket privado). La web sube con PUT prefirmado; el CORS del
    bucket solo acepta el origen de la tienda.
46. **Solo soles (PEN).** Duradent vende solo en soles: sin selector de moneda en la tienda, precios y
    tarifas de envío en PEN, comprobantes sin tipo de cambio. La moneda sigue en el modelo (`currency`
    en tablas, `money.Currency`, campo `currency` del API) pero `money.Supported` solo acepta PEN y el
    API rechaza cualquier otra con `*.currency_invalid`. La migración 0009 borra los precios en USD y la
    columna `shipping_rates.amount_usd`. Volver a vender en dólares exige reintroducirlos en el dominio,
    el admin de precios, las tarifas y la facturación (tipo de cambio SBS).
47. **Filtros del catálogo con facetas disyuntivas** (`GET /v1/catalog/products?brand=…&attr=código:valor&sort=name|newest`): cada grupo se cuenta ignorando su propia selección; los valores de un atributo son alternativas y atributos distintos deben cumplirse en la **misma variante**. La web es un formulario GET (filtros en la URL, sin JS) con barra lateral sticky en escritorio y `popover` en móvil. Precio y stock no son filtros todavía: viven en otros contextos (pricing por lista de cliente, inventory).
48. **Libro de Reclamaciones virtual** (Ley 29571 y su reglamento; contexto `complaints`). Cualquiera,
    con o sin sesión, registra un reclamo o una queja en `/…/complaints-book`; el aviso (libro abierto y la
    frase "Libro de Reclamaciones") está en el pie de todas las páginas, también en la de inicio. Cada hoja:
    número correlativo por año en hora de Lima (`000001-2026`, tabla `complaint_sequences` como las series
    de comprobantes), datos del proveedor congelados (`COMPANY_NAME/RUC/ADDRESS`; RUC 20268534866, fijado
    en `envs/*/main.tf` y obligatorio fuera de local), `Idempotency-Key` por carga del formulario y 10 envíos por hora por IP. El consumidor
    recibe al momento la hoja en pantalla (imprimible) y una copia por correo (`ComplaintFiled`);
    operaciones recibe una alerta. La respuesta se registra una sola vez en `/admin/complaints` y viaja por
    correo con la hoja completa (`ComplaintAnswered`); si pidió carta, el admin además la imprime y la envía.
    Plazo: fin del día 15 hábil (solo se saltan fines de semana, así el plazo calculado nunca es más tarde
    que el legal). Las hojas no se borran.

## Estado por fases

- [x] Fase 1 — monorepo, Compose, CI
- [x] Fase 2 — catálogo, pricing, frontend i18n, admin de catálogo
- [x] Fase 3 — identidad (Google, Microsoft, email)
- [x] Fase 4 — carrito, envíos, pedidos, pagos Culqi + transferencia
- [x] Fase 5 — outbox relay, worker, emails, facturación SUNAT, reembolsos, verificación de correo
- [x] Fase 6 — Terraform, ALB, ECS, despliegue (sin aplicar: falta la cuenta AWS de Duradent)

## Runbook (AWS)

Región `us-east-1` (Duradent no necesita datos en Perú y ahí están todos los servicios). Dominio
supuesto `duradent.pe` / `staging.duradent.pe`: cámbialo en `envs/*/main.tf` si es otro.

### Primera vez (una sola vez por cuenta)

1. Bucket de estado y tabla de locks (fuera de Terraform):
   `aws s3api create-bucket --bucket duradent-terraform-state-<cuenta>` (con versionado y cifrado) y
   `aws dynamodb create-table --table-name duradent-terraform-locks --attribute-definitions AttributeName=LockID,AttributeType=S --key-schema AttributeName=LockID,KeyType=HASH --billing-mode PAY_PER_REQUEST`.
   Copia `infra/terraform/backend.hcl.example` a `backend.hcl`.
2. `cd infra/terraform/shared && terraform init -backend-config=../backend.hcl -backend-config="key=shared.tfstate"` y
   `terraform apply -var github_repository=<owner/repo> -var state_bucket=… -var lock_table=…`: crea ECR y
   los roles de GitHub. En GitHub: variables `AWS_REGION`, `AWS_DEPLOY_ROLE_ARN`, `AWS_PLAN_ROLE_ARN`,
   `TF_STATE_BUCKET`, `TF_LOCK_TABLE`; entornos `staging` y `prod` (este con revisor obligatorio).
3. Certificados ACM (validación DNS) para `staging.duradent.pe` y `duradent.pe` en us-east-1.
4. Sube una imagen: un push a `main` corre el job *build* (el deploy fallará hasta el paso 6, es normal).
5. Por entorno, **prod primero** (crea la identidad SES del dominio): copia `terraform.tfvars.example` a
   `terraform.tfvars` y rellénalo (`image_tag` = el SHA subido);
   `terraform init -backend-config=../../backend.hcl -backend-config="key=<env>.tfstate"`; `terraform apply`;
   guarda la copia aplicada con `aws s3 cp terraform.tfvars s3://<bucket>/tfvars/<env>.tfvars` (la usa
   el plan de los PRs).
6. Carga los secretos de terceros (todas las claves, vacías si no aplican):
   `aws secretsmanager put-secret-value --secret-id duradent-<env>/app --secret-string file://app.json`
   con `CULQI_SECRET_KEY`, `CULQI_WEBHOOK_SECRET` (24 caracteres aleatorios o más), `NUBEFACT_URL`,
   `NUBEFACT_TOKEN`, `GOOGLE_CLIENT_SECRET`, `MICROSOFT_CLIENT_SECRET`, `ADMIN_TOKEN` (32 o más, o vacío).
   Borra `app.json` después.
7. DNS: CNAME/alias del dominio al `alb_dns_name`; registros de `ses_dns_records` (DKIM, MAIL FROM,
   DMARC). Pide a AWS sacar SES del sandbox. Confirma el correo de suscripción a las alarmas (SNS).
8. Webhook en CulqiPanel: `culqi_webhook_url_prefix` + `CULQI_WEBHOOK_SECRET`. Redirect URIs de
   Google/Microsoft: `https://<dominio>/auth/<google|microsoft>/callback`.
9. Despliega con `task deploy -- <env> <sha>` o re-ejecutando el workflow. Prueba: pedido con tarjeta de
   prueba de Culqi en staging, correo recibido, comprobante en la demo de Nubefact.

### Despliegue normal

Merge a `main` → imágenes `duradent/{api,web}:<sha>` → staging automático (migración + rollout).
Prod: Actions → *Deploy* → *Run workflow* con `environment=prod` y el mismo SHA → aprobación del
revisor. Cambios de infraestructura: PR (el plan sale en el job *Terraform plan*) y `terraform apply`
local tras el merge; actualiza la copia de tfvars en S3 si cambió.

### Rollback

`task deploy -- prod <sha-anterior>` (o el workflow con ese SHA). Las migraciones no se revierten:
escríbelas compatibles hacia atrás (añadir columnas; renombrar o borrar en dos versiones). Si un
rollout no arranca, el circuit breaker de ECS vuelve solo a la revisión anterior.

### Rotar secretos

- Terceros (Culqi, Nubefact, OIDC, `ADMIN_TOKEN`): `put-secret-value` con el JSON nuevo y
  `aws ecs update-service --cluster duradent-<env> --service <servicio> --force-new-deployment` para api,
  worker y outbox-relay (los contenedores leen los secretos al arrancar). Si cambia
  `CULQI_WEBHOOK_SECRET`, actualiza también la URL en CulqiPanel.
- Base de datos: `terraform apply -replace=module.stack.module.db.random_password.db` (cambia la
  contraseña en RDS y el secreto del DSN) y fuerza el redeploy de los cuatro servicios Go.

### Mensajes en la DLQ

La alarma `duradent-<env>-<consumidor>-dlq-not-empty` avisa por correo. Busca el error en
`/ecs/duradent-<env>-worker` (mensaje `handle message (will retry)`), corrige la causa y devuelve los
mensajes a la cola: consola SQS → la DLQ → *Start DLQ redrive* (o
`aws sqs start-message-move-task --source-arn <arn-de-la-dlq>`). Los consumidores son idempotentes:
reintentar no duplica correos ni comprobantes. Comprobantes rechazados: admin → pedido →
*Comprobantes electrónicos* → *Reintentar*.

### Otros

- Shell en un contenedor: `aws ecs execute-command --cluster duradent-<env> --task <id> --container app --interactive --command sh`
  (solo la web tiene shell; las imágenes Go son distroless).
- Logs: CloudWatch `/ecs/duradent-<env>-<servicio>`; los errores (`level=ERROR`) tienen alarma propia.
