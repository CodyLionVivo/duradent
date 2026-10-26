# Logotipo de Duradent

`logo.svg` es la fuente: vector trazado del logotipo original (`logo-original.png`, 447 px),
color `#046880`. Todo lo demás se deriva de él.

| Archivo | Uso |
|---|---|
| `logo.svg` | Web (cabecera, pie, admin). Escala sin pérdida a cualquier tamaño |
| `logo-white.svg` | Sobre fondos oscuros o el color de marca |
| `logo-1024.png` | Impresión, redes, documentos (fondo transparente) |
| `logo-email.png` | Cabecera de los correos (192 px, se muestra a 40 px) |
| `og-image.png` | Vista previa al compartir enlaces (WhatsApp, LinkedIn, Facebook), 1200 × 630 |
| `icon-192.png`, `icon-512.png` | Android / "Añadir a pantalla de inicio" (`/site.webmanifest`) |
| `icon-maskable-512.png` | Icono adaptable de Android (dentro de la zona segura del 80 %) |
| `/favicon.svg` | Pestaña del navegador (se aclara en modo oscuro) |
| `/favicon.ico` | Navegadores antiguos y Windows (16, 32 y 48 px) |
| `/apple-touch-icon.png` | iPhone / iPad, 180 px |

Si cambia el logotipo, reemplaza `logo.svg` y vuelve a exportar los PNG desde él con los
mismos tamaños (por ejemplo con `sharp` o Inkscape).
