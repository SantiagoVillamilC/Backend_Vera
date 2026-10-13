# Diseño de la población de gasto con verdad (experimento C)

Fecha: 12 de octubre de 2026. Estado: población, prorrateo y tope implementados.

Hoy el experimento C es una prueba de aceptación sobre 5 usuarios y 13 etiquetas escritas a mano. Cerrarlo como
medición exige una población de usuarios de gasto con verdad por diseño. Este documento fija el diseño.

## Decisión de método

La verdad se etiqueta por **intención de diseño**, igual que `series_fondo.py` etiqueta `es_recurrente` por la
intención con la que genera cada serie y nunca por un umbral. El generador diseña usuarios cuyo patrón realiza una
intención con una brecha grande (por ejemplo 3 veces o 0,3 veces la base), y escribe la verdad esperada en
`avisos_esperados.csv`. La regla se mide contra esa intención. La brecha es de diseño y no se ajusta al umbral de la
regla (nada de afinar el dato hasta que la regla dispare).

## Tipos cubiertos

Los cinco: `presupuesto_anticipado`, `categoria_sobre_media`, `caida_ingreso`, `pago_no_alcanza` y `cobro_nuevo`.
Para cada usuario se escriben cinco filas de verdad (una por tipo) en la fecha de evaluación, con la intención en
`debe_disparar_con_mediana` y `debe_disparar_con_media`.

## Parámetro y activación

- `Parametros.gasto_usuarios: int = 0` (validar mayor o igual a 0), en `a_dict`.
- CLI `--gasto-usuarios` (por defecto 0, tipo `_no_negativo`).
- `orquestador._escenarios_fijos` llama a `gasto_poblacion.construir(ctx)`.
- Azar propio `random.Random(f"{semilla}-gasto")`, separado del flujo principal, para no alterar el resto.
- Con `gasto_usuarios = 0` la salida de datos es byte a byte la de antes. El manifiesto gana la clave nueva, así que
  la prueba del manifiesto de `tests/test_colusion_poblacion.py` debe quitar también `gasto_usuarios`.

## Fecha de evaluación y meses

- `FECHA_EVALUACION = date(2026, 10, 20)` (el fin de ese día local, como el perfil 4).
- Seis meses completos, de abril a septiembre de 2026 (ingreso el día 1, gasto repartido en días del mes).
- Octubre parcial hasta el 20.

## Diseños (ciclo)

El módulo cicla una lista de diseños hasta crear `gasto_usuarios` usuarios. Cada diseño debe ser positivo en su tipo
y negativo en los otros cuatro. Las brechas son grandes para que la intención no dependa del umbral.

1. `presupuesto_pos`: el gasto de octubre hasta el 20 muy por encima de la base mensual. El exceso va en una
   categoría NUEVA (sin gasto en los meses completos) como UN solo pago, para que `categoria_sobre_media` no
   dispare (esa categoría no tiene meses completos evaluables) y `cobro_nuevo` tampoco (un solo pago no es serie).
   Intención: solo `presupuesto_anticipado`.
2. `presupuesto_neg`: gasto de octubre hasta el 20 muy por debajo de la base. Todo falso.
3. `categoria_pos`: una categoría existente muy por encima de su base y el total por debajo de la base, para que
   `categoria_sobre_media` dispare y `presupuesto_anticipado` no. Intención: solo `categoria_sobre_media`.
4. `categoria_neg`: todas las categorías por debajo. Todo falso.
5. `caida_pos`: el ingreso de septiembre muy por debajo de la base de abril a agosto (con MAD cero, así R2 cae en el
   respaldo R1). Intención: solo `caida_ingreso`.
6. `caida_neg`: todos los ingresos iguales. Todo falso.
7. `pago_no_alcanza_pos`: una serie mensual de monto constante de abril a septiembre (el último pago en septiembre,
   así el siguiente esperado cae el 20 de octubre dentro de la ventana de 7 días) y un ingreso parecido al gasto para
   que el saldo al 20 de octubre quede por debajo de un pago de la serie. Intención: solo `pago_no_alcanza`.
8. `cobro_nuevo_pos`: una llave nueva con dos pagos de monto similar en los últimos 35 días (por ejemplo el 7 y el 15
   de octubre), para que `cobro_nuevo` dispare por "dos pagos de monto similar". Intención: solo `cobro_nuevo`.
9. `sin_avisos`: un usuario normal, sin ninguna perturbación. Todo falso.

## Verificación

Cada prueba de diseño debe construir el usuario y comprobar que la decisión de la regla (R1 y R2, mediana y media)
coincide con la intención en los cinco tipos. Eso no es circular: la intención es la brecha de diseño, no un umbral
calibrado. Además: determinismo, esquema de la verdad, y que con `gasto_usuarios = 0` no haya cambios en los datos.

## Tope y prorrateo

- Tope de avisos por usuario y mes (3), con prioridad `pago_no_alcanza` mayor que `caida_ingreso` mayor que
  `cobro_nuevo` mayor que `presupuesto_anticipado` mayor que `categoria_sobre_media`. Se aplica en la evaluación,
  sobre la verdad y sobre la salida de la regla, con la misma prioridad. Pendiente.
- Prorrateo del acumulado: ya implementado en `avisos.py` como bandera `prorrateo_acumulado` (por defecto apagada),
  como variante medida. Pendiente la corrida con y sin.
