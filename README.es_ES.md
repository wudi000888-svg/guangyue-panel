<!-- language-navigation -->
[简体中文](README.md) · [English](README_EN.md) · [فارسی](README.fa_IR.md) · [العربية](README.ar_EG.md) · [Español](README.es_ES.md) · [Русский](README.ru_RU.md) · [Türkçe](README.tr_TR.md)
<!-- /language-navigation -->

<div align="center">

<img src="docs/assets/banner.svg" alt="Guangyue Panel: despliegue en un paso, gestión de planes, IP y sitios hijos" width="960" />

# Guangyue Panel · 广月面板

**Empieza con un dominio y reúne nodos, planes y sitios hijos en un mismo espacio de trabajo.**

[![Última versión](https://img.shields.io/github/v/release/wudi000888-svg/guangyue-panel?color=2563eb)](https://github.com/wudi000888-svg/guangyue-panel/releases/latest)
[![CI](https://github.com/wudi000888-svg/guangyue-panel/actions/workflows/ci.yml/badge.svg)](https://github.com/wudi000888-svg/guangyue-panel/actions/workflows/ci.yml)
[![LGPL-3.0](https://img.shields.io/badge/License-LGPL--3.0-blue.svg)](LICENSE)

[Instalar](docs/INSTALL.md) · [Descargar la última versión](https://github.com/wudi000888-svg/guangyue-panel/releases/latest) · [Guía de uso](docs/USER_GUIDE.md) · [Documentación](docs/README.md)

</div>

Guangyue es un panel autohospedado para servicios proxy y recursos de red, desarrollado con Go y Vue 3. Con un servidor adecuado y un dominio que ya apunte a él, puedes desplegar VLESS Reality/Vision y Hysteria 2 y, después, gestionar desde el panel suscripciones por plan, salidas IP, sitios hijos independientes, pedidos, tickets y cobros.

Está pensado para operadores de servicios proxy que empiezan con un solo sitio, para administradores que necesitan gestionar de forma centralizada varios sitios independientes y para equipos que asignan salidas IP a sus miembros. Lite sirve para operar un sitio autónomo; Pro lo amplía con una consola principal. Ambas ediciones se instalan con el mismo script interactivo.

## Primeros pasos

### Antes de instalar

- **El dominio debe apuntar ya a este servidor, sin proxy de CDN.** En Cloudflare, selecciona «DNS only» y desactiva la nube naranja.
- Usa un **servidor Linux amd64 (x86_64) completo con systemd** y acceso root o sudo. La comprobación previa acepta Debian 12/13 y Ubuntu 22.04/24.04; las pruebas de instalación nativa en máquinas virtuales cubren por ahora Ubuntu 22.04/24.04. Que un sistema se acepte no significa que se hayan probado todas sus configuraciones.
- **Los puertos públicos TCP 443 y UDP 443 deben estar libres y permitidos**, tanto en el grupo de seguridad del proveedor como en el cortafuegos del servidor. El script usa siempre el 443 y no migra servicios HTTPS existentes; puedes mantener un servicio Nginx en HTTP 80.
- **Docker es la opción predeterminada; la instalación nativa es opcional.** Por ahora Docker solo publica por IPv4: configura únicamente registros A, sin AAAA. Si necesitas IPv6, elige la instalación nativa de doble pila. Docker requiere Linux 5.4 o posterior.
- Como punto de partida, reserva 1 CPU / 1 GiB para Lite y 2 CPU / 2 GiB para Pro, con espacio adicional para imágenes, datos y copias de seguridad. Es una recomendación de instalación, no una promesa de usuarios simultáneos ni de ancho de banda.

### Ejecutar el instalador

Copia el bloque completo en la terminal del servidor:

```bash
gy_install_file=$(mktemp)
curl --proto '=https' --proto-redir '=https' -fsSL \
  https://github.com/wudi000888-svg/guangyue-panel/releases/latest/download/install.sh \
  -o "$gy_install_file" && sudo bash "$gy_install_file"
rm -f "$gy_install_file"
```

El asistente te pide elegir Lite o Pro, el dominio y el tipo de despliegue. Comprueba el sistema, los recursos, el DNS, los puertos y cualquier configuración existente de Nginx / Docker, y después muestra un plan de instalación para que lo confirmes. Los componentes y las imágenes Docker proceden de la versión seleccionada y se verifican con sus sumas de comprobación; no necesitas una cuenta en ningún registro de imágenes. Los certificados se emiten y renuevan automáticamente mediante TLS-ALPN-01 en TCP 443.

Tras la instalación, lee el archivo privado de la cuenta inicial `/var/lib/guangyue/initial-owner.json` como indica el script, inicia sesión, cambia la contraseña del administrador y prueba VLESS y HY2 con un cliente real. Configura según necesites el registro, la publicación de planes y los canales de cobro; los servicios externos de pago y correo requieren tu propia configuración válida.

`--check` realiza una comprobación previa de solo lectura y `--resume` continúa una instalación interrumpida. Los sitios existentes deben usar [actualización y reversión en línea](docs/UPDATES.md) en lugar de sobrescribir sus datos con una instalación nueva. La [guía de instalación](docs/INSTALL.md) detalla los requisitos, la recuperación ante fallos y los límites del despliegue.

## Lite o Pro

| | Lite | Pro |
| --- | --- | --- |
| Uso predeterminado | Sitio independiente; también puede ser sitio hijo | Sitio principal que gestiona sitios hijos |
| Base de datos | SQLite | PostgreSQL |
| Caché y tareas | Caché local limitada y cola de tareas persistente | Caché Redis y cola de tareas persistente |
| Funciones locales | Nodos, grupo de IP, planes, usuarios, pedidos y tickets | Lo mismo que Lite, más la gestión centralizada |
| Conexión | Genera un token de sitio hijo para que el principal lo importe | Pega el token de un sitio hijo para conectarlo |
| Instalación | Docker / systemd nativo | Docker / systemd nativo |

La instalación no necesita archivo de registro ni grupos de sitios creados de antemano. **El sitio hijo genera el token y el sitio principal Pro lo pega.** Los tokens pueden caducar o seguir vigentes hasta que se revoquen. Un sitio hijo conectado conserva sus administradores locales, cuentas, base de datos y servicios independientes, y el acceso se puede revocar. Cuando un sitio hijo pasa a estar gestionado por primera vez, su módulo de pagos queda desactivado por defecto; su administrador local puede activarlo si lo necesita. Consulta [ediciones](docs/EDITIONS.md) y [gestión de sitios](docs/BUSINESS-SITES.md).

## Qué puedes hacer

| Ámbito | Funciones |
| --- | --- |
| Nodos y suscripciones | VLESS Reality/Vision, HY2; suscripciones Raw / Base64 / Mihomo, copia y códigos QR; enlaces a clientes oficiales por plataforma, con descarga opcional retransmitida por tu sitio |
| Gestión de planes | Acceso por grupo de nodos de origen o por nodos concretos de un grupo; cuota y vigencia, planes gratuitos, límites de compra, planes en cola; multiplicadores de nodo por plan en pasos de 0,5, hasta 10× |
| Pedidos y soporte | Saldo interno, códigos de canje, pedidos, solicitud / cancelación / aprobación / rechazo de reembolsos, tickets con imágenes, notas internas del administrador y avisos en el panel |
| Recursos IP | Salidas HTTP CONNECT / SOCKS5, suscripciones de proveedores autorizados, actualización automática de fuentes, comprobaciones de calidad, políticas de DNS e IPv6; el acceso a recursos públicos está oculto por defecto y puede mostrarse como preferencia del navegador actual |
| Gestión de sitios hijos | Conexión por token, permisos y operaciones de servicio, presupuestos compartidos con recuperación tras el período de espera, montaje de nodos, salidas IP del sitio hijo, comprobación de versiones compatibles con actualización y reversión |
| Supervisión | Gráficas de tráfico por usuario, muestreo de sesiones VLESS y conexiones HY2, estado de servicios, progreso de tareas y recuperación tras reinicios |
| Correo y pagos | Correo SMTP, EPay V1, Stripe Checkout y cobros cripto para cadenas admitidas y activos de la lista permitida; los canales externos requieren configuración y pruebas de aceptación |
| Monederos | Monedero caliente HD integrado / importación de clave pública externa, direcciones permanentes por pedido, saldos y direcciones con fondos, estimación de gas y consolidaciones confirmadas por el administrador |
| Trabajo diario | Interfaz en chino e inglés, temas claro y oscuro, diseño móvil, barra lateral configurable, reglas de acceso por país, copias de seguridad / restauración y gestión de versiones |

### El plan decide qué nodos puede usar cada miembro

El administrador elige para cada plan grupos de nodos locales, de sitios hijos o públicos, o nodos concretos dentro de un grupo. Los miembros ven su plan actual, el tiempo y la cuota restantes, el acceso a la suscripción y los clientes, sin necesidad de entender los grupos de nodos ni las reglas internas de las suscripciones mixtas.

Al comprar otro plan, la vigencia restante del actual se pausa y se reanuda según la cola de derechos. Los reembolsos requieren la revisión del administrador: el plan activo se reembolsa según la cuota o el tiempo restantes, los pedidos en cola devuelven por ahora el importe original del pedido y se conserva el orden de restauración de los demás derechos. Las reglas exactas están en [planes](docs/PLANS.md) y [pedidos y reembolsos](docs/COMMERCE_EN.md). Los cambios de nodos, multiplicadores y protocolos del plan activo se sincronizan con los permisos del servidor; la cuota, la vigencia y los planes en cola conservan su instantánea. Las suscripciones que el cliente tenga en caché deben actualizarse.

### Los nodos y las IP de un sitio hijo siguen rutas distintas

- **Nodos montados del sitio hijo:** el cliente se conecta directamente al nodo del sitio hijo; el tráfico no pasa por el sitio principal.
- **Salidas IP del sitio hijo:** el cliente se conecta a un nodo del sitio principal, que retransmite el tráfico a través de la salida IP del sitio hijo.

Ambos tipos de recurso se gestionan por separado e indican su origen. Cuando se agota el presupuesto compartido de un sitio hijo, el acceso se pausa según la configuración y se restablece al terminar el período de espera; la pertenencia a los grupos de nodos se conserva. El procedimiento está en [montaje de nodos y presupuestos](docs/NODE_MOUNTS.md).

### Cobros cripto y monederos

Cuando un usuario paga con criptomonedas, el pedido recibe una dirección propia que se conserva de forma permanente. El cobro y la consolidación automáticos cubren por ahora los activos USDT / USDC admitidos en Ethereum, BNB Smart Chain, Plasma y Solana, según la lista permitida de contratos o mints; configurar una L2 no implica que admita liquidación y consolidación automáticas.

Las consultas de saldo y de gas son de solo lectura. **Las comisiones solo se pagan cuando el administrador confirma una consolidación.** El aporte automático de comisiones solo se aplica a direcciones de pedidos reales cuyo saldo del stablecoin seleccionado de la lista permitida sea estrictamente superior a 0,1. En EVM solo se cubre la diferencia que falta; en Solana, las comisiones se pagan desde una dirección fija de fondos en SOL. Si se superara el límite de comisión aprobado inicialmente, la operación se pausa para revisión manual. Los detalles y las diferencias entre cadenas están en [gestión de monederos](docs/CRYPTO_WALLETS.md) y [cobro y consolidación de pedidos](docs/CRYPTO_TREASURY.md).

El monedero caliente integrado está pensado para saldos operativos pequeños. Aunque la frase mnemónica se guarda cifrada, la capacidad de firma sigue en el mismo servidor: haz una copia de seguridad privada y retira los fondos con regularidad. Un xpub EVM externo sirve para derivar y consultar direcciones mientras las transferencias se hacen desde el monedero externo; no otorga capacidad de firma al servidor.

## Alcance y límites

- Los protocolos de entrada que se ofrecen directamente son **VLESS Reality/Vision y Hysteria 2**. Los formatos de origen que se pueden importar dependen del analizador y de Mihomo, y no añaden entradas nativas con el mismo nombre.
- El despliegue actual es un controlador por sitio. No se promete alta disponibilidad multimaestro para un mismo sitio, aislamiento de inquilinos a nivel de organización, SLA ni auditoría de seguridad independiente. La base de datos y la caché de Pro no mejoran una ruta de red que ya esté congestionada.
- La CI incluye instalación aislada en Linux, recuperación de actualizaciones y verificación de protocolos. Eso no es un informe de capacidad para producción a gran escala ni la aceptación de tu configuración comercial o de transacciones en la red principal. Antes de abrir el servicio, prueba clientes reales, copias de seguridad, correo y flujos de cobro.
- Las fuentes de RPC, cotizaciones y comprobaciones de calidad pueden tener límites o dejar de estar disponibles. Un resultado desconocido nunca se trata como comisión cero, y llegar a una página pública no significa que las cuentas, la reproducción, los pagos o los servicios regionales estén desbloqueados.
- Este repositorio ofrece **la presentación del proyecto en 7 idiomas**; la interfaz del panel admite por ahora **chino simplificado e inglés**. Los manuales detallados están sobre todo en chino y algunos tienen versión en inglés; una presentación traducida no significa que la interfaz y los manuales estén localizados a ese idioma.

## Documentación por tarea

| ¿Qué quieres hacer? | Empieza aquí |
| --- | --- |
| Instalar y elegir edición | [Instalación en un paso](docs/INSTALL.md) · [Docker](docs/DOCKER.md) · [Lite / Pro](docs/EDITIONS.md) |
| Configurar planes, ventas y suscripciones | [Guía de uso](docs/USER_GUIDE.md) · [Planes](docs/PLANS.md) · [Pedidos y pagos](docs/COMMERCE_EN.md) |
| Conectar sitios hijos, montar nodos y asignar salidas | [Gestión de sitios](docs/BUSINESS-SITES.md) · [Montaje de nodos](docs/NODE_MOUNTS.md) · [Supervisión en tiempo real](docs/LIVE-MONITORING.md) |
| Configurar correo, cobros cripto y consolidación | [Correo](docs/EMAIL.md) · [Monederos](docs/CRYPTO_WALLETS.md) · [Cobro y consolidación](docs/CRYPTO_TREASURY.md) |
| Operar, diagnosticar y actualizar | [Operación](docs/OPERATIONS.md) · [Actualizaciones](docs/UPDATES.md) · [Configuración](docs/CONFIGURATION.md) |
| Desarrollar e informar de problemas de seguridad | [Contribución](CONTRIBUTING.md) · [Arquitectura](docs/ARCHITECTURE.md) · [Seguridad](SECURITY.md) |

El índice completo está en la [guía de documentación](docs/README.md). Los materiales de diseño están en la [fuente de diseño de Penpot](docs/designs/penpot/README.md) y en el [sistema de diseño de la interfaz](docs/designs/penpot-courtyard-system.md).

## Código abierto y contribución

Se agradecen los informes de problemas reproducibles, las sugerencias y los pull requests. Al informar de un problema, indica la versión, la edición, el tipo de despliegue, el rol del sitio y los pasos para reproducirlo; no subas credenciales reales de suscripción, claves privadas, frases mnemónicas ni configuraciones de producción completas. Los requisitos de verificación para cambios de código y documentación están en la [guía de contribución](CONTRIBUTING.md); para problemas de seguridad, usa el [proceso de notificación privada](SECURITY.md).

Guangyue Panel usa **LGPL-3.0-only** desde la versión 0.15.0. El aviso MIT original de fake-ui se conserva en la [licencia histórica](licenses/guangyue-legacy-MIT.txt), junto con el [texto de la GPL-3.0](licenses/GPL-3.0.txt). El parche propio de Hysteria usa MIT, Xray MPL-2.0 y Mihomo GPL-3.0; las atribuciones de componentes y el código fuente correspondiente están en los [avisos de terceros](THIRD_PARTY_NOTICES.md).

La estructura de esta presentación sigue a [3x-ui](https://github.com/MHSanaei/3x-ui), y el contenido multilingüe se ha escrito según las capacidades reales de Guangyue; la estructura anterior de la documentación se inspiró en Sub2API, sin usar su marca ni sus capturas. Las versiones completas incluyen paquetes de ejecución, el instalador, archivos de imágenes Docker, un manifiesto de sumas de comprobación e información de dependencias; consulta las [notas de publicación](docs/RELEASE.md). Si el proyecto te resulta útil, una estrella o tus comentarios tras una instalación real serán bienvenidos.
