# Prompts siguientes

Fecha: 6 de octubre de 2026. Los prompts de este archivo ya se ejecutaron (Prompt 2 redactor, Prompt 3 verificación y Prompt 4 advisor). Se conserva como registro. El estado real, al 12 de octubre de 2026, es el siguiente.

## Estado al cierre (12 de octubre de 2026)

| Pieza | Estado |
|---|---|
| Experimentos A, B y C | Cerrados, con bitácora, más las extensiones A2, A3 y la calibración de similitud documentadas en la sección Actualizaciones de la bitácora de A. |
| Suite | 577 pruebas OK. Los conteos de 413 y 501 en las bitácoras quedaron corregidos. |
| Bitácora de A (`bitacora-tecnica/2026-10-06-convergencia-multitud-k.md`) | Actualizada con A2, A3 y similitud, y con las frases que decían que la similitud no se midió, corregidas. |
| Bitácora de C (`bitacora-tecnica/2026-10-06-avisos-presupuesto-aceptacion.md`) | Completa, con la sección de fuentes y R1 retiquetada como Probado en código. |
| CR de aportes técnicos | CR-031 (recurrencia) y CR-032 (convergencia) escritas y corregidas, y CR-033 (avisos) creada en `aportes-tecnicos/borrador-inteligencia/`. Falta integrarlas al documento de equipo. |
| `PLAN-ANALISIS.md` | Actualizado, con la tabla por experimento, las extensiones y los pendientes en orden. |

## Orden y quién lanza qué

El investigador NO puede lanzar al redactor: sus herramientas son solo lectura y web, sin la herramienta `Agent`. La cadena la orquesta la sesión principal, un subagente a la vez:

1. Prompt 1 (investigador, opcional pero pedido): cierra los huecos de C.
2. La sesión principal pega los hallazgos nuevos en el marcador del Prompt 2 y lanza el redactor.
3. Prompt 3: verificación posterior hecha por la sesión principal.
4. Prompt 4: consulta final al advisor antes de dar el día por cerrado.

Reglas de estilo para todo texto en español: tuteo sin voseo, sin raya larga ni media, sin punto y coma en prosa, cifras redondeadas en prosa. Para leer y editar archivos, usar Read, Edit y Write, no sed, cat, head ni grep por shell. Borrar con `trash-put -rf`.

## Prompt 1: investigador de C, cierre de huecos

Agente: `investigador`.

```
Investiga en la web y verifica abriendo los documentos, no solo resúmenes del buscador. Cita URL y nivel de confianza (alta, media, baja) y nombra lo que no pudiste abrir y por qué. Español neutro con tuteo, sin raya larga ni media, sin punto y coma en prosa.

Contexto: tesis sobre una billetera digital. Se evalúa una regla de avisos de presupuesto "el gasto acumulado del mes hasta hoy supera la mediana de los meses completos anteriores", sin prorratear. Una primera pasada encontró lo siguiente y NO hay que repetirlo: Yodlee documenta topes de frecuencia (saldo bajo una vez cada 16 días, gasto sobre el promedio una vez por mes por categoría, semanal los lunes en el agregado), Copilot compara contra el ritmo del mes sin fórmula publicada, Quicken usa montos fijos, Localytics 2017 (de segunda mano) da 10 por ciento de desactivación con 1 notificación por semana, y ninguna app revisada documenta un multiplicador ni un percentil.

Cierra estos huecos, con intentos alternativos cuando una página dé 403 o contenido vacío (versión en web.archive.org, caché, otra URL oficial del mismo producto):
1. Monarch Money: documentación oficial de alertas y presupuestos. Si prorratea o compara contra el ritmo, y con qué umbral (por ejemplo avisar al 80 por ciento del presupuesto).
2. YNAB: documentación oficial sobre cómo se marca una categoría en rojo o amarillo y si usa el ritmo del mes.
3. Plaid: documentación de `/transactions/recurring/get` y de insights de gasto. Qué campos entrega (próxima fecha prevista, monto promedio) y cómo se calculan.
4. Pielot, Church y de Oliveira (MobileHCI 2014), "An in-situ study of mobile phone notifications": abre el PDF o un espejo y cita lo que afirma sobre la frecuencia de notificaciones y la atención del usuario.
5. Original de Localytics sobre notificaciones push (2017 o cercano) o un espejo de Upland o MarketingProfs: confirma las cifras de desactivación por notificaciones por semana citadas arriba.
6. Un estudio académico o de la industria con cifras abiertas sobre la fatiga de alertas financieras o de apps bancarias (no push de marketing en general).
Para cada punto entrega 2 a 4 líneas con cita textual corta entre comillas si abriste el documento, y para qué decisión de la tesis sirve (tope de avisos por mes, prorrateo, umbral). Cierra con la lista de lo que seguiste sin poder verificar.
```

## Prompt 2: redactor que aplica todos los cambios

Agente: `general-purpose`, modelo `sonnet`, en segundo plano. Un solo subagente activo.

Antes de lanzarlo, la sesión principal reemplaza el marcador `[HALLAZGOS NUEVOS DEL PROMPT 1]` por los hallazgos del Prompt 1 (o por "ninguno" si no se ejecutó).

```
Eres el redactor de documentación. Trabajas en /home/chris/Documents/universidad-2026B/proyecto-fin-de-grado/. Eres el único agente activo. No hagas commit ni push. SOLO editas estos tres archivos: bitacora-tecnica/2026-10-06-convergencia-multitud-k.md, bitacora-tecnica/2026-10-06-avisos-presupuesto-aceptacion.md y dataset-inteligencia/PLAN-ANALISIS.md. No toques código ni nada más.

IDEMPOTENCIA: un redactor anterior se detuvo a mitad. Probablemente ya está aplicado el bloque de la bitácora de A, parcialmente el de la bitácora de C, y nada del plan. Lee cada archivo completo primero (por tramos de 300 líneas o menos) y aplica SOLO lo que falte. No dupliques ni contradigas lo ya escrito. Si un cambio ya está, déjalo.

REGLAS DE ESTILO (obligatorias): español neutro con tuteo, nunca voseo. NUNCA raya larga ni raya media. En prosa NUNCA punto y coma. Cifras redondeadas en prosa, exactas solo en tablas. Usa Read, Edit y Write. No uses sed, cat, head, tail ni grep por shell y no reescribas archivos con scripts. Busca con `rg`. Mantén el formato de seis secciones y las etiquetas Medido, Probado en código, Verificado en fuente, Inferido. Antes de terminar ejecuta `rg -n "\x{2014}|\x{2013}"` sobre los tres archivos (escapes Unicode, nunca escribas las rayas en el comando) y revisa punto y coma en prosa y corrige.

CAMBIOS EN la bitácora de A (convergencia):
1. Corrige toda afirmación de que "K=2 deja 0 errores de ruido de tipeo". Lo verificado: a tasa 0,1 y 0,35 los errores visibles de nombre con K=2 son 0,016 (2 comercios por corrida) en las 4 semillas según la tabla (Medido). El orquestador descompuso solo la corrida de semilla 2026 con tasa 0,35 y comprobó que esos 2 son los comercios con cambio real de nombre que empatan 2 contra 2 (Probado en código). A tasa 0,6 los errores visibles con K=2 suben a un rango de 0,016 a 0,039 entre semillas (hasta 5 comercios en la peor), así que a esa tasa sí se cuelan errores además de los 2 cambios reales.
2. Hallazgo (Probado en código, script del orquestador sobre el estado final de votos, sin colusión ni cambio real): la abreviatura del generador es DETERMINISTA (`generador/ruido.py` líneas 8 a 15 y 64 a 66, diccionario ABREVIATURAS), así que dos usuarios que abrevian el mismo nombre escriben exactamente lo mismo (por ejemplo `TDAS-D1-KENN`) y K=2 puede promoverlo. El tipeo es aleatorio e independiente por usuario (`ruido.py` líneas 69 a 82). Comercios donde un nombre equivocado llegó a 2 o más votantes distintos (exposición latente, NO errores visibles): tasa 0,35: 1 (semilla 2026), 2 (semilla 1), 1 (semilla 7), 2 (semilla 11). Tasa 0,6: 4 (semilla 2026, tres abreviaturas y un tipeo), 3 (semilla 1), 5 (semilla 7), 6 (semilla 11), casi todos por abreviatura. A tasa 0,35 no se ve como error porque el nombre correcto ya estaba consolidado y el empate conserva al incumbente. Es un artefacto del generador en dos direcciones: pesimista para abreviaturas y optimista para tipeos. Los usuarios reales podrían repetir un mismo tipeo y K=2 lo promovería (sin fuente, inferencia). Agrégalo a limitaciones y a lo pendiente.
3. En "Qué queda abierto": el tope "a lo sumo unos 9 de unos 20 comercios de prueba sin categoría tienen nombre que revela su naturaleza" (denominador correcto: prueba_sin_colusion, unos 38 comercios, de los cuales unos 20 quedan sin categoría global con K=2 y tasa 0,35) vale para clasificadores sobre nombres FICTICIOS (TF-IDF, MiniLM). Un LLM puede reconocer marcas reales y `verdad/empresas.csv` tiene la columna `ficticia`, así que ese tope no le aplica y debe medirse aparte. Revisa que el texto no diga "20 de 50".

CAMBIOS EN la bitácora de C (avisos):
1. RETIQUETA el 13 de 13 de R1 como "Probado en código": es prueba de consistencia, porque R1 es la regla leída de las mismas 13 etiquetas. Lo informativo y "Medido" es: R2 (regla citada, independiente) falla las 2 filas del perfil 4, el falso positivo de R1 en semilla 11 y el volumen del fondo. Quita cualquier "Resultado medido" que presente el 13 de 13 como medición.
2. Volumen del fondo: las evaluaciones de usuarios sin etiqueta son a fin de cada mes más la fecha final de la corrida (21 de octubre), según `analisis/avisos_fondo.py` líneas 54 a 62. A fin de mes el gasto acumulado sin prorratear es casi el total del mes, y comparado con la mediana de los meses anteriores dispara cerca de la mitad de las veces en usuarios de gasto estable POR CONSTRUCCIÓN. Los 82 de 176 de categoria_sobre_media son consistentes con eso (Inferido, no se recalculó el porcentaje). La etiqueta del perfil 4 dispara con 1,3 millones frente a una mediana de 1,25 millones, un 4 por ciento por encima, así que codifica una regla laxa que generaría ruido en un producto. El fallo de R2 ahí es un desacuerdo con una etiqueta laxa, no un defecto de R2.
3. Series oráculo y detectadas dan resultado idéntico: di que la comparación NO es informativa con estos datos (series regulares que B detecta bien), no que los errores de B sean inocuos para C.
4. Limitación para 1.4: excluir los envíos a personas del gasto saca de "gasto" el arriendo pagado a una llave de persona.
5. Sección de fuentes de la búsqueda web de C, cada una con URL y confianza. Yodlee Projected Low Balance, una vez cada 16 días por entidad (alta, https://developer.yodlee.com/resources/yodlee/insights-details/docs/projected-low-balance). Yodlee Spending Exceeded Average por categoría: una vez por mes calendario por categoría, compara contra el promedio de los 3 meses anteriores, umbral por defecto 50 dólares y 10 por ciento, ambas condiciones (alta, https://developer.yodlee.com/resources/yodlee/insights-details/docs/spending-exceeded-average). Yodlee Spending Exceeded Average All Accounts: semanal, los lunes, sin prorrateo documentado (alta, https://developer.yodlee.com/resources/yodlee/insights-details/docs/spending-exceeded-average-all-accounts). Copilot compara contra el ritmo del mes y dibuja una línea de gasto ideal, sin fórmula publicada, de modo que el prorrateo es inferencia (alta para la cita, https://help.copilot.money/en/articles/9504513-categories-tab-overview y https://intercom.help/copilotmoney/en/articles/6045480-dashboard-tab-overview). Ninguna app revisada documenta un multiplicador ni un percentil, solo umbrales fijos: Quicken avisa por categoría sobre el promedio en montos de 200, 500, 1000 o 2000 dólares (alta, https://info.quicken.com/win/email-alerts), Huntington 100 dólares sobre el promedio de 6 meses (media, solo fragmento del buscador). Fatiga: Localytics 2017 citado por un blog, 10 por ciento dice desactivar notificaciones con 1 por semana y 37 por ciento con 2 a 5 por semana, respuestas declaradas en encuesta y no conducta medida (media, https://www.elaine.io/en/blog/push-notifications-marketing-friend-foe/). Huecos: Monarch, YNAB, Mint, Plaid y Braze no se pudieron abrir en la primera pasada, no hay fórmula publicada del ritmo de Copilot y no se encontró estudio abierto que fije un umbral de notificaciones por semana. Implicación para el capítulo 7 (recomendación, no resultado medido): tope de 1 aviso por mes por categoría como precedente, piso de monto además del porcentaje (Yodlee usa 10 por ciento, la etiqueta laxa usa 4), y prorratear el acumulado por día del mes.
[HALLAZGOS NUEVOS DEL PROMPT 1]

CAMBIOS EN dataset-inteligencia/PLAN-ANALISIS.md:
- Actualiza la fecha y el estado: Experimento B cerrado, A cerrado en código y documentado (núcleo, bitácora de convergencia), C cerrado como prueba de aceptación de 13 filas (bitácora de avisos), suite en 501 pruebas. Agrega una tabla por experimento que distinga lo Medido de lo Probado en código.
- Reemplaza la sección de orden propuesto por lo pendiente, en este orden: (1) pasada de similitud de nombres con umbral calibrado sobre pares etiquetados de iguales y distintos (RUTA VELOZ frente a RUTA VELOZ COMIDAS es el negativo difícil, TIENDAS D1 SUBA frente a CHAPINERO son sucursales), trigramas TF-IDF y Jaro-Winkler, y medir el efecto de la abreviatura determinista del generador. (2) Regla de deriva con un escenario de ataque posterior a la consolidación, porque el generador solo tiene colusores antes de los honestos y una regla de deriva se vería bien por construcción. (3) Población de usuarios de gasto con verdad por diseño para C (hoy solo 5 usuarios), con prorrateo del acumulado y tope de avisos por mes. (4) Usuario simulado de feedback con barrido de la tasa de respuesta 0,1, 0,25 y 0,5. (5) Comparación de clasificadores TF-IDF, MiniLM y LLM por API, con la nota de que el LLM puede reconocer marcas reales y hay que usar la columna `ficticia`. (6) Entradas de aportes técnicos (CR) para los capítulos 1.4, 3.2, 4.2, 5.1, 5.2, 7 y 9.
- Agrega las limitaciones nuevas a la sección de entrega a la tesis: razón fija de 3 honestos frente a T colusores, ruido de tipeo independiente y abreviatura determinista, envíos a personas fuera de gasto, etiquetas de C escritas a mano.

Al terminar entrega un resumen corto: qué cambiaste en cada archivo, qué ya estaba aplicado y el resultado del escaneo de rayas y punto y coma.
```

## Prompt 3: verificación posterior (la hace la sesión principal)

```
cd /home/chris/Documents/universidad-2026B/proyecto-fin-de-grado
rg -n "\x{2014}|\x{2013}" bitacora-tecnica/2026-10-06-convergencia-multitud-k.md bitacora-tecnica/2026-10-06-avisos-presupuesto-aceptacion.md dataset-inteligencia/PLAN-ANALISIS.md dataset-inteligencia/PROMPTS-SIGUIENTES.md
cd dataset-inteligencia && python3 -m unittest > /home/chris/.cache/claude-tmp/suite.txt 2>&1 && rg -n "^Ran|^OK|FAILED" /home/chris/.cache/claude-tmp/suite.txt
```

Esperado: ninguna raya, `Ran 501 tests` y `OK`. Después, leer con Read las secciones editadas y comprobar que: (a) ninguna afirmación dice que K=2 elimina todo el ruido, (b) el 13 de 13 de R1 está como "Probado en código", (c) el denominador del residuo es prueba_sin_colusion, (d) el plan lista los seis pendientes en orden.

## Prompt 4: consulta final al advisor

Herramienta `advisor()` y, si cae, subagente `advisor` con `model: "opus"` y contexto resumido (no hereda la conversación). Resumen que debe llevar:

```
Cierre del día 6 de octubre de 2026 en la tesis (billetera digital). Hechos: generador con colusión parametrizada (330 pruebas), experimento A de convergencia por multitud con K en 1, 2 y 3 sobre 12 corridas (tasas 0,1, 0,35 y 0,6, semillas 2026, 1, 7 y 11), titular en el ámbito sin_colusion con cobertura, riesgo y errores visibles, experimento C de avisos como prueba de aceptación de 13 filas con dos reglas declaradas de antemano (R1 implícita en las etiquetas, R2 z modificado de 3,5), suite en 501 pruebas. Hallazgos a validar: K=2 baja los errores visibles de nombre pero pierde unos 31 puntos de cobertura, la regla de relevo estricto no sigue un cambio real de nombre (empate 2 contra 2), la abreviatura determinista del generador permite que K=2 promueva abreviaturas a tasa 0,6, R1 aprueba 13 de 13 por consistencia con sus etiquetas, R2 falla el perfil 4, R1 tiene un falso positivo en semilla 11 por una guarda de 0,25 al borde. Preguntas: ¿alguna afirmación de las bitácoras queda sobrerrepresentada como Medido? ¿Qué se debe declarar en 1.4 que falte? ¿El orden de pendientes del plan es el correcto para los 18 días?
```

## Pendientes de Christian

- Nada está commiteado. Decidir cuándo y con qué rama (regla global: nunca push directo a `main` ni `staging`).
- `manifiesto.json` ahora registra `colusion_comercios` y `colusion_tamanos` incluso con N=0. Si prefieres que con N=0 quede idéntico al anterior, es un cambio de una línea.
- Corregir `TIENDAD1BOG01` en `indicaciones-equipo/*.md` y registrar que el regex del backend para llaves de comercio no coincide con la norma Bre-B.
- Resultado de la reunión del martes sobre el plano H (alcance en `diapositiva-planos/src/data/capacidades.ts`).
