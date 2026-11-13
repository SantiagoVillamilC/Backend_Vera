//go:build integration

package inteligencia

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// El periodo de un aviso es el mes al que pertenece el hecho que lo dispara y la fecha de creación es
// cuándo se entregó la tarjeta. Estas pruebas siembran los dos en meses distintos a propósito: el cupo,
// la bandeja y el resumen cuentan por periodo, así que un aviso entregado en otro mes pertenece al mes
// de su periodo. El tope sale siempre de la configuración del emisor y se elige distinto del valor por
// defecto para que se vea de dónde se lee.

const topeDeCupo = 4

func servicioConTope(t *testing.T, pool *pgxpool.Pool, tope int) *Servicio {
	t.Helper()
	configuracion := avisos.ConfiguracionPorDefecto()
	configuracion.TopeAvisosMes = tope
	return NuevoServicio(pool, ConConfiguracionDeAvisos(configuracion))
}

// cerrarElCupoDeAvisos emite con un tope holgado lo que el cupo dejó pendiente en un usuario. Sin
// esto, una corrida posterior de la suite informaría como recorte un aviso que esta prueba dejó
// pendiente a propósito.
func cerrarElCupoDeAvisos(t *testing.T, pool *pgxpool.Pool, usuario string, ahora time.Time) {
	t.Helper()
	if _, _, err := servicioConTope(t, pool, 10).emitirAvisosDeUsuario(t.Context(), usuario, ahora); err != nil {
		t.Fatalf("corrida de cierre de %q: %v", usuario, err)
	}
}

func sembrarAvisoDeCupo(t *testing.T, pool *pgxpool.Pool, usuario, clave string, periodo,
	creadoEn time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, `
INSERT INTO inteligencia.aviso (id, usuario_id, tipo, clave, periodo, severidad_centavos, prioridad,
    texto, texto_verificado, creado_en)
VALUES ($1, $2, 'cobro_nuevo', $3, $4, 1000, 3, 'aviso de cupo', true, $5)`,
				pgtype.UUID{Bytes: id, Valid: true}, usuario, clave,
				pgtype.Date{Time: fechaCivil(periodo), Valid: true},
				pgtype.Timestamptz{Time: creadoEn, Valid: true})
			return struct{}{}, err
		})
	if err != nil {
		t.Fatalf("sembrar aviso %q de %q: %v", clave, usuario, err)
	}
	return id
}

// sembrarCupoLleno deja el cupo de julio lleno con avisos cuyo periodo es julio pero que se entregaron
// en junio o en agosto.
func sembrarCupoLleno(t *testing.T, pool *pgxpool.Pool, usuario string, tope int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, 0, tope)
	for i := 0; i < tope; i++ {
		entregado := time.Date(2026, time.June, 20, 15, 0, 0, 0, time.UTC)
		if i%2 == 1 {
			entregado = time.Date(2026, time.August, 3, 15, 0, 0, 0, time.UTC)
		}
		ids = append(ids, sembrarAvisoDeCupo(t, pool, usuario, fmt.Sprintf("cupo-%d", i),
			time.Date(2026, time.July, 2+i, 12, 0, 0, 0, time.UTC), entregado))
	}
	return ids
}

// avisosContadosPorElEmisor lee la ocupación del cupo de un periodo por el mismo camino que recorre
// el emisor.
func avisosContadosPorElEmisor(t *testing.T, pool *pgxpool.Pool, usuario string, ahora time.Time) int {
	t.Helper()
	contados := 0
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (struct{}, error) {
			_, emitidos, err := estadoDelPeriodo(ctx, q, usuario, inicioDelMesDe(ahora, 0))
			contados = emitidos
			return struct{}{}, err
		})
	if err != nil {
		t.Fatalf("contar avisos de %q: %v", usuario, err)
	}
	return contados
}

func resumenYListado(t *testing.T, servicio *Servicio, usuario, mes string, ahora time.Time) (ResumenDelMes, int) {
	t.Helper()
	resumen, err := servicio.Resumen(t.Context(), usuario, mes, ahora)
	if err != nil {
		t.Fatalf("Resumen de %q: %v", mes, err)
	}
	listado, err := servicio.AvisosDelUsuario(t.Context(), usuario, mes, ahora)
	if err != nil {
		t.Fatalf("AvisosDelUsuario de %q: %v", mes, err)
	}
	return resumen, len(listado)
}

// sembrarCaidaDeIngresoDeJunio deja un usuario cuyo único hecho es la caída del ingreso de junio. El
// hecho pertenece a junio y se entrega en julio, que es el caso que separa el periodo del mes de
// entrega.
func sembrarCaidaDeIngresoDeJunio(t *testing.T, pool *pgxpool.Pool, usuario string) {
	t.Helper()
	gastoMensual(t, pool, usuario, 5, 30_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	montosIngreso := []int64{200_000_000, 200_000_000, 200_000_000, 200_000_000, 200_000_000, 100_000_000}
	for i, mes := range mesesDeAvisos {
		sembrarDeposito(t, pool, usuario, montosIngreso[i], time.Date(2026, mes, 1, 14, 0, 0, 0, time.UTC))
	}
}

func TestResumenYListadoCoincidenConElCupoLleno(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := servicioConTope(t, pool, topeDeCupo)
	usuario := usuarioDeAvisos(t, pool)
	sembrarCupoLleno(t, pool, usuario, topeDeCupo)

	resumen, listados := resumenYListado(t, servicio, usuario, "2026-07", ahoraDeAvisos)
	if resumen.CupoAvisos != topeDeCupo {
		t.Errorf("cupo = %d, want %d: el resumen no lee el tope de la configuración",
			resumen.CupoAvisos, topeDeCupo)
	}
	if resumen.AvisosEmitidos != topeDeCupo || listados != topeDeCupo {
		t.Fatalf("resumen = %d, listado = %d, want %d en los dos",
			resumen.AvisosEmitidos, listados, topeDeCupo)
	}
	if contados := avisosContadosPorElEmisor(t, pool, usuario, ahoraDeAvisos); contados != listados {
		t.Fatalf("el emisor cuenta %d y el listado muestra %d", contados, listados)
	}
}

func TestElAvisoEntregadoEnOtroMesCuentaDondePertenece(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NuevoServicio(pool)
	usuario := usuarioDeAvisos(t, pool)
	sembrarAvisoDeCupo(t, pool, usuario, "cobro-de-junio",
		time.Date(2026, time.June, 20, 15, 0, 0, 0, time.UTC), ahoraDeAvisos)

	deJunio, listadosDeJunio := resumenYListado(t, servicio, usuario, "2026-06", ahoraDeAvisos)
	if deJunio.AvisosEmitidos != 1 || listadosDeJunio != 1 {
		t.Fatalf("junio: resumen = %d, listado = %d, want 1 en los dos",
			deJunio.AvisosEmitidos, listadosDeJunio)
	}
	if contados := avisosContadosPorElEmisor(t, pool, usuario,
		time.Date(2026, time.June, 15, 15, 0, 0, 0, time.UTC)); contados != 1 {
		t.Fatalf("el emisor cuenta %d en junio, want 1", contados)
	}

	deJulio, listadosDeJulio := resumenYListado(t, servicio, usuario, "2026-07", ahoraDeAvisos)
	if deJulio.AvisosEmitidos != 0 || listadosDeJulio != 0 {
		t.Fatalf("julio: resumen = %d, listado = %d, want 0 en los dos",
			deJulio.AvisosEmitidos, listadosDeJulio)
	}
	if contados := avisosContadosPorElEmisor(t, pool, usuario, ahoraDeAvisos); contados != 0 {
		t.Fatalf("el emisor cuenta %d en julio, want 0: el aviso pertenece a junio", contados)
	}
}

func TestDescartarLiberaCupoEnElResumenYEnElEmisor(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := servicioConTope(t, pool, topeDeCupo)
	usuario := usuarioDeAvisos(t, pool)
	ids := sembrarCupoLleno(t, pool, usuario, topeDeCupo)

	if _, err := servicio.DescartarAviso(t.Context(), usuario, ids[0], ahoraDeAvisos); err != nil {
		t.Fatalf("descartar: %v", err)
	}

	quiero := topeDeCupo - 1
	resumen, listados := resumenYListado(t, servicio, usuario, "2026-07", ahoraDeAvisos)
	if resumen.AvisosEmitidos != quiero || listados != quiero {
		t.Fatalf("resumen = %d, listado = %d, want %d en los dos",
			resumen.AvisosEmitidos, listados, quiero)
	}
	if contados := avisosContadosPorElEmisor(t, pool, usuario, ahoraDeAvisos); contados != quiero {
		t.Fatalf("el emisor cuenta %d, want %d: el descarte no liberó cupo", contados, quiero)
	}
}

// sembrarGastoHastaAgosto deja cinco meses completos de gasto moderado y un agosto que dispara el
// presupuesto y la categoría. Cada mes usa una llave distinta para que ningún comercio se vuelva una
// serie recurrente y el único hecho del mes nuevo sea el gasto.
func sembrarGastoHastaAgosto(t *testing.T, pool *pgxpool.Pool, usuario string) {
	t.Helper()
	meses := []time.Month{time.March, time.April, time.May, time.June, time.July}
	for _, mes := range meses {
		sembrarCopia(t, pool, filaDeCopia{
			usuario: usuario, ocurrido: time.Date(2026, mes, 5, 15, 0, 0, 0, time.UTC),
			monto: 10_000_000, direccion: "salida", categoria: "alimentos_mercado",
			contraparte: "MERCADO", llave: "L-MERCADO-" + mes.String(), nombre: "MERCADO",
			transaccion: uuid.New(),
		})
	}
	sembrarCopia(t, pool, filaDeCopia{
		usuario: usuario, ocurrido: time.Date(2026, time.August, 5, 15, 0, 0, 0, time.UTC),
		monto: 90_000_000, direccion: "salida", categoria: "alimentos_mercado",
		contraparte: "MERCADO", llave: "L-MERCADO-AGOSTO", nombre: "MERCADO",
		transaccion: uuid.New(),
	})
}

// TestElMesNuevoEmpiezaConElCupoCompleto llena el cupo de julio con el tope configurado y emite en
// agosto las dos candidatas del mes nuevo. Agosto las admite enteras porque su cupo arranca vacío:
// con el tope en dos, el mes nuevo se llena exactamente y no recorta nada aunque julio esté saturado.
func TestElMesNuevoEmpiezaConElCupoCompleto(t *testing.T) {
	const topeDelMesNuevo = 2
	pool := abrirPoolDePrueba(t)
	servicio := servicioConTope(t, pool, topeDelMesNuevo)
	usuario := usuarioDeAvisos(t, pool)
	sembrarCupoLleno(t, pool, usuario, topeDelMesNuevo)
	sembrarGastoHastaAgosto(t, pool, usuario)

	agosto := inicioDelMesDe(ahoraDeAvisos, 1).AddDate(0, 0, 14)
	insertados, recuento, err := servicio.emitirAvisosDeUsuario(t.Context(), usuario, agosto)
	if err != nil {
		t.Fatalf("emitir los avisos de agosto: %v", err)
	}
	if insertados != topeDelMesNuevo {
		t.Fatalf("insertados en agosto = %d, want %d: el presupuesto y la categoría del mes nuevo",
			insertados, topeDelMesNuevo)
	}
	if recuento.Candidatas != topeDelMesNuevo || recuento.Elegidas != topeDelMesNuevo ||
		recuento.Recortadas != 0 {
		t.Fatalf("recuento de agosto = %+v, want %d candidatas, %d elegidas y 0 recortadas: "+
			"el cupo de julio no frena agosto", recuento, topeDelMesNuevo, topeDelMesNuevo)
	}

	deAgosto, listadosDeAgosto := resumenYListado(t, servicio, usuario, "2026-08", agosto)
	if deAgosto.CupoAvisos != topeDelMesNuevo {
		t.Fatalf("cupo de agosto = %d, want %d: el mes nuevo arranca con el cupo completo",
			deAgosto.CupoAvisos, topeDelMesNuevo)
	}
	if deAgosto.AvisosEmitidos != topeDelMesNuevo || listadosDeAgosto != topeDelMesNuevo {
		t.Fatalf("agosto: resumen = %d, listado = %d, want %d en los dos: agosto se llenó entero",
			deAgosto.AvisosEmitidos, listadosDeAgosto, topeDelMesNuevo)
	}
	if contados := avisosContadosPorElEmisor(t, pool, usuario, agosto); contados != topeDelMesNuevo {
		t.Fatalf("el emisor cuenta %d en agosto, want %d", contados, topeDelMesNuevo)
	}

	deJulio, listadosDeJulio := resumenYListado(t, servicio, usuario, "2026-07", ahoraDeAvisos)
	if deJulio.CupoAvisos != topeDelMesNuevo || deJulio.AvisosEmitidos != topeDelMesNuevo ||
		listadosDeJulio != topeDelMesNuevo {
		t.Fatalf("julio: cupo = %d, resumen = %d, listado = %d, want %d en los tres: julio seguía lleno",
			deJulio.CupoAvisos, deJulio.AvisosEmitidos, listadosDeJulio, topeDelMesNuevo)
	}
}

// TestElCupoDelEmisorSeAplicaAlPeriodoDelAviso llena el cupo de junio y entrega en julio un aviso de
// junio. El tope se mira en junio, así que el aviso queda fuera y el recorte se informa como tal.
func TestElCupoDelEmisorSeAplicaAlPeriodoDelAviso(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	configuracion := avisos.ConfiguracionPorDefecto()
	configuracion.TopeAvisosMes = 1
	servicio := NuevoServicio(pool, ConConfiguracionDeAvisos(configuracion))

	usuario := usuarioDeAvisos(t, pool)
	sembrarCaidaDeIngresoDeJunio(t, pool, usuario)
	sembrarAvisoDeCupo(t, pool, usuario, "junio-lleno",
		time.Date(2026, time.June, 3, 12, 0, 0, 0, time.UTC),
		time.Date(2026, time.May, 20, 12, 0, 0, 0, time.UTC))

	// El recuento se pide por usuario porque la corrida completa suma a todos los usuarios con
	// historial de la base compartida y solo este es el que la prueba controla.
	insertados, recuento, err := servicio.emitirAvisosDeUsuario(t.Context(), usuario, ahoraDeAvisos)
	if err != nil {
		t.Fatalf("emitir los avisos de %q: %v", usuario, err)
	}
	if insertados != 0 {
		t.Fatalf("insertados = %d, want 0: el cupo de junio, el periodo del aviso, estaba lleno", insertados)
	}
	if recuento.Recortadas != 1 {
		t.Fatalf("recortadas = %d, want 1: el aviso pertenece a junio y junio está lleno", recuento.Recortadas)
	}

	deJunio, listadosDeJunio := resumenYListado(t, servicio, usuario, "2026-06", ahoraDeAvisos)
	if deJunio.CupoAvisos != 1 || deJunio.AvisosEmitidos != 1 || listadosDeJunio != 1 {
		t.Fatalf("junio: cupo = %d, resumen = %d, listado = %d, want 1 en el cupo y 1 en los dos",
			deJunio.CupoAvisos, deJunio.AvisosEmitidos, listadosDeJunio)
	}
	deJulio, listadosDeJulio := resumenYListado(t, servicio, usuario, "2026-07", ahoraDeAvisos)
	if deJulio.AvisosEmitidos != 0 || listadosDeJulio != 0 {
		t.Fatalf("julio: resumen = %d, listado = %d, want 0 en los dos: el aviso es de junio",
			deJulio.AvisosEmitidos, listadosDeJulio)
	}

	// Deja al usuario sin hechos pendientes: las demás corridas de la suite lo vuelven a evaluar.
	cerrarElCupoDeAvisos(t, pool, usuario, ahoraDeAvisos)
}

// TestUnDuplicadoFueraDeLaVentanaNoSeCuentaComoRecorte entrega el mismo aviso de junio cinco meses
// antes de la corrida. La deduplicación lo mira dentro de su periodo, así que no entra como candidato
// ni ocupa un lugar del tope: el emisor no informa un recorte que no ocurrió.
func TestUnDuplicadoFueraDeLaVentanaNoSeCuentaComoRecorte(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NuevoServicio(pool)
	usuario := usuarioDeAvisos(t, pool)

	gastoMensual(t, pool, usuario, 6, 80_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	for _, momento := range []time.Time{
		time.Date(2026, time.June, 20, 15, 0, 0, 0, time.UTC),
		time.Date(2026, time.July, 10, 15, 0, 0, 0, time.UTC),
	} {
		monto := int64(15_000_000)
		if momento.Month() == time.July {
			monto = 15_200_000
		}
		sembrarCopia(t, pool, filaDeCopia{usuario: usuario, ocurrido: momento, monto: monto,
			direccion: "salida", categoria: "entretenimiento", contraparte: "NUEVA",
			llave: "L-NUEVA", nombre: "NUEVA", transaccion: uuid.New()})
	}
	sembrarAvisoDeCupo(t, pool, usuario, "cobro-nuevo",
		time.Date(2026, time.June, 20, 15, 0, 0, 0, time.UTC),
		time.Date(2026, time.January, 5, 12, 0, 0, 0, time.UTC))

	insertados, recuento, err := servicio.emitirAvisosDeUsuario(t.Context(), usuario, ahoraDeAvisos)
	if err != nil {
		t.Fatalf("emitir los avisos de %q: %v", usuario, err)
	}
	if insertados != 0 {
		t.Fatalf("insertados = %d, want 0: el aviso de junio ya existe", insertados)
	}
	if recuento.Recortadas != 0 {
		t.Fatalf("recortadas = %d, want 0: un duplicado no es un recorte", recuento.Recortadas)
	}
	if recuento.YaEmitidas != 1 {
		t.Fatalf("ya emitidas = %d, want 1: el duplicado de junio pertenece a su periodo",
			recuento.YaEmitidas)
	}

	deJunio, listadosDeJunio := resumenYListado(t, servicio, usuario, "2026-06", ahoraDeAvisos)
	if deJunio.AvisosEmitidos != 1 || listadosDeJunio != 1 {
		t.Fatalf("junio: resumen = %d, listado = %d, want 1 en los dos",
			deJunio.AvisosEmitidos, listadosDeJunio)
	}
}
