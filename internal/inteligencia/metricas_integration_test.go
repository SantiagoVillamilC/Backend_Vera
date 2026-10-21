//go:build integration

package inteligencia

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestLoteConfirmadoPublicaSusContadores mide el lote real contra Postgres y exige que lo publicado
// coincida con lo que devolvió el servicio. Es la prueba de que la métrica no miente.
func TestLoteConfirmadoPublicaSusContadores(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	grabadas := &metricasGrabadas{}
	servicio := NuevoServicio(pool, ConMetricas(grabadas))

	primero, segundo := uuid.NewString(), uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, primero)
	sembrarCuentaDeUsuario(t, pool, segundo)
	sembrarDeposito(t, pool, primero, 500_000_00, time.Date(2026, 7, 5, 15, 0, 0, 0, time.UTC))
	sembrarDeposito(t, pool, segundo, 900_000_00, time.Date(2026, 7, 6, 15, 0, 0, 0, time.UTC))

	resultado, err := servicio.Enriquecer(t.Context(), 50)
	if err != nil {
		t.Fatalf("Enriquecer: %v", err)
	}
	if len(grabadas.lotes) != 1 {
		t.Fatalf("observaciones del lote = %d, quiero 1", len(grabadas.lotes))
	}
	publicado := grabadas.lotes[0]
	if publicado.Asientos != resultado.Asientos || publicado.Movimientos != resultado.Movimientos {
		t.Fatalf("publicado = %+v, devuelto = %+v", publicado, resultado)
	}
	if publicado.Asientos < 2 {
		t.Fatalf("asientos publicados = %d, quiero al menos los 2 sembrados", publicado.Asientos)
	}
	if len(publicado.PorOrigen) == 0 {
		t.Fatal("el lote publicado no trae orígenes")
	}
	suma := 0
	for _, cantidad := range publicado.PorOrigen {
		suma += cantidad
	}
	if suma != publicado.Movimientos {
		t.Fatalf("la suma de orígenes = %d, los movimientos = %d", suma, publicado.Movimientos)
	}
	if publicado.VersionClasificador == "" {
		t.Fatal("la versión del clasificador debe declararse siempre")
	}
	if len(grabadas.duracionesLote) != 1 || grabadas.duracionesLote[0] <= 0 {
		t.Fatalf("duración publicada = %v, quiero una muestra positiva", grabadas.duracionesLote)
	}
}

// TestLoteEnCursoPublicaLaOmision comprueba que un candado tomado se informa como omisión, no como
// error, y no suma asientos.
func TestLoteEnCursoPublicaLaOmision(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	grabadas := &metricasGrabadas{}
	servicio := NuevoServicio(pool, ConMetricas(grabadas))

	// El candado se toma en una conexión aparte porque el pool de pruebas usa una sola.
	conexion, err := pgx.Connect(t.Context(), os.Getenv("APP_DATABASE_URL"))
	if err != nil {
		t.Fatalf("abrir la conexión del candado: %v", err)
	}
	defer func() { _ = conexion.Close(t.Context()) }()
	bloqueo, err := conexion.Begin(t.Context())
	if err != nil {
		t.Fatalf("abrir la transacción del candado: %v", err)
	}
	defer func() { _ = bloqueo.Rollback(t.Context()) }()
	if _, err := bloqueo.Exec(t.Context(),
		"SELECT pg_advisory_xact_lock(hashtext('inteligencia.enriquecimiento')::bigint)"); err != nil {
		t.Fatalf("tomar el candado: %v", err)
	}

	if _, err := servicio.Enriquecer(t.Context(), 10); err != ErrLoteEnCurso {
		t.Fatalf("error = %v, quiero ErrLoteEnCurso", err)
	}
	if len(grabadas.desenlacesFallidos) != 1 || grabadas.desenlacesFallidos[0] != DesenlaceLoteEnCurso {
		t.Fatalf("desenlaces = %v, quiero una omisión por candado", grabadas.desenlacesFallidos)
	}
	if len(grabadas.lotes) != 0 {
		t.Fatalf("lotes publicados = %d, quiero 0: la transacción no se confirmó", len(grabadas.lotes))
	}
}

// TestAvisosPublicanElRecorteDelCupo mide cuántas tarjetas deja fuera el cupo, que antes no dejaba
// rastro. Con un solo aviso de cupo y dos candidatas, una entra y una se recorta.
func TestAvisosPublicanElRecorteDelCupo(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	grabadas := &metricasGrabadas{}
	configuracion := avisos.ConfiguracionPorDefecto()
	configuracion.TopeAvisosMes = 1
	servicio := NuevoServicio(pool, ConMetricas(grabadas), ConConfiguracionDeAvisos(configuracion))

	usuario := usuarioDeAvisos(t, pool)
	gastoMensual(t, pool, usuario, 5, 90_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	sembrarCopia(t, pool, filaDeCopia{
		usuario: usuario, ocurrido: time.Date(2026, 7, 3, 15, 0, 0, 0, time.UTC),
		monto: 150_000_000, direccion: "salida", categoria: "alimentos_mercado",
		contraparte: "MERCADO", llave: "L-MERCADO", nombre: "MERCADO", transaccion: uuid.New(),
	})

	if _, err := servicio.EmitirAvisos(t.Context(), ahoraDeAvisos); err != nil {
		t.Fatalf("EmitirAvisos: %v", err)
	}
	if len(grabadas.avisos) == 0 {
		t.Fatal("no se publicó ninguna corrida de avisos")
	}
	resumen := grabadas.avisos[len(grabadas.avisos)-1]
	if resumen.Usuarios == 0 {
		t.Fatal("la corrida no visitó a ningún usuario")
	}
	if resumen.Emitidos > resumen.Usuarios {
		t.Fatalf("emitidos = %d con %d usuarios: con cupo de 1 no puede haber más de uno por usuario",
			resumen.Emitidos, resumen.Usuarios)
	}
	if resumen.SuprimidosPorCupo == 0 {
		t.Fatal("el cupo de 1 debía recortar al menos una tarjeta: el presupuesto y la categoría compiten")
	}
	if len(resumen.DuracionesPorUsuario) != resumen.Usuarios {
		t.Fatalf("duraciones por usuario = %d, usuarios = %d",
			len(resumen.DuracionesPorUsuario), resumen.Usuarios)
	}
}

// TestAvisosIdempotentesNoCuentanComoEmitidos separa el dedupe del cupo en una segunda corrida.
func TestAvisosIdempotentesNoCuentanComoEmitidos(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	grabadas := &metricasGrabadas{}
	servicio := NuevoServicio(pool, ConMetricas(grabadas))

	usuario := usuarioDeAvisos(t, pool)
	gastoMensual(t, pool, usuario, 5, 90_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	sembrarCopia(t, pool, filaDeCopia{
		usuario: usuario, ocurrido: time.Date(2026, 7, 3, 15, 0, 0, 0, time.UTC),
		monto: 150_000_000, direccion: "salida", categoria: "alimentos_mercado",
		contraparte: "MERCADO", llave: "L-MERCADO", nombre: "MERCADO", transaccion: uuid.New(),
	})

	if _, err := servicio.EmitirAvisos(t.Context(), ahoraDeAvisos); err != nil {
		t.Fatalf("primera corrida: %v", err)
	}
	if _, err := servicio.EmitirAvisos(t.Context(), ahoraDeAvisos); err != nil {
		t.Fatalf("segunda corrida: %v", err)
	}
	if len(grabadas.avisos) < 2 {
		t.Fatalf("corridas publicadas = %d, quiero 2", len(grabadas.avisos))
	}
	segunda := grabadas.avisos[len(grabadas.avisos)-1]
	if segunda.Emitidos != 0 {
		t.Fatalf("emitidos en la segunda corrida = %d, quiero 0", segunda.Emitidos)
	}
	if segunda.YaEmitidos == 0 {
		t.Fatal("la segunda corrida debía contar las tarjetas ya emitidas")
	}
}

// TestLoteConLexicoEscribeElOrigenLexico es la prueba de que el escalón 5, cuando está encendido,
// deja su origen en la copia y no se confunde con el clasificador.
func TestLoteConLexicoEscribeElOrigenLexico(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	lexico, err := CargarLexicoV2()
	if err != nil {
		t.Fatalf("CargarLexicoV2: %v", err)
	}
	servicio := NuevoServicio(pool, ConLexico(lexico))

	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("abrir la transacción de la cuenta: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE rol_nucleo"); err != nil {
		t.Fatalf("bajar a rol_nucleo: %v", err)
	}
	if _, err := tx.Exec(t.Context(), `
INSERT INTO nucleo.cuenta (id, naturaleza, clase) VALUES ($1, 'pasivo', 'comercio')
ON CONFLICT (id) DO NOTHING`, "pasivo:liquidacion_pendiente:comercio-0"); err != nil {
		t.Fatalf("crear la cuenta del comercio: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("confirmar la cuenta del comercio: %v", err)
	}
	sembrarAsiento(t, pool, "liquidacion_confirmada", nil, "DROGUERIA NORTE 12",
		time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC), []lineaDePrueba{
			{"pasivo:usuario:" + usuario + ":disponible", 120_000_00},
			{"pasivo:liquidacion_pendiente:comercio-0", -120_000_00},
		})

	resultado, err := servicio.Enriquecer(t.Context(), 200)
	if err != nil {
		t.Fatalf("Enriquecer: %v", err)
	}
	if resultado.PorOrigen[OrigenLexico] == 0 {
		t.Fatalf("orígenes = %v, quiero al menos uno por léxico", resultado.PorOrigen)
	}
	var origen, categoria string
	_, err = enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, `
SELECT origen_categoria, categoria FROM inteligencia.movimiento_enriquecido
 WHERE usuario_id = $1 ORDER BY ocurrido_en DESC LIMIT 1`, usuario).Scan(&origen, &categoria)
		})
	if err != nil {
		t.Fatalf("leer la copia: %v", err)
	}
	if origen != OrigenLexico || categoria != string(CategoriaSalud) {
		t.Fatalf("origen %q categoría %q, quiero %q y %q", origen, categoria, OrigenLexico, CategoriaSalud)
	}
}
