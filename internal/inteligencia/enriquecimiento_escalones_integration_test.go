//go:build integration

package inteligencia

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// casoDeEscalon es una entrada de la cascada con el escalón, el origen y la categoría que deben
// ganar en el lote. La entrada se arma igual que en escribirMovimiento: un pago por QR a un
// comercio externo, con la descripción del asiento como nombre de la contraparte.
type casoDeEscalon struct {
	nombre            string
	descripcion       string
	mcc               string
	cuentaContraparte string
	categoriaInterna  Categoria
	escalon           int
	origen            string
	categoria         Categoria
	usuarioConMemoria bool
	abstuvo           bool
}

func (c casoDeEscalon) entrada() Entrada {
	return Entrada{
		Canal:            CanalQR,
		Nombre:           c.descripcion,
		MCC:              c.mcc,
		TipoDestino:      DestinoComercio,
		CategoriaInterna: string(c.categoriaInterna),
	}
}

// TestElLoteEscribeElOrigenDeCadaEscalon recorre los escalones con el catálogo cargado por
// CargarCatalogo, comprueba la decisión de la cascada y después corre un lote real sobre asientos
// sembrados, de modo que el origen y el escalón queden comprobados en
// inteligencia.movimiento_enriquecido y no solo en la llamada a Decidir.
func TestElLoteEscribeElOrigenDeCadaEscalon(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	sufijo := strings.Split(uuid.NewString(), "-")[0]

	nombreDelCatalogo := "DROGUERIA PRUEBA " + sufijo
	nombreDelLexico := "DROGUERIA CALLE " + sufijo
	nombreConMemoria := "COMERCIO MEMORIA " + sufijo
	origenDelCatalogo := "prueba-escalones-" + sufijo

	lexico, err := CargarLexicoV2()
	if err != nil {
		t.Fatalf("CargarLexicoV2: %v", err)
	}
	modelo := modeloClasificador(t, idfDeTexto("ALFA"),
		map[string]map[string]string{"compras": centroideConCoseno(t, "ALFA", 0.9)}, nil)
	servicio := NuevoServicio(pool,
		ConCatalogo(make(map[string]Categoria), make(map[string]Categoria)),
		ConClasificador(modelo),
		ConLexico(lexico))
	if err := servicio.CargarCatalogo(ctx); err != nil {
		t.Fatalf("CargarCatalogo: %v", err)
	}

	// Los nombres se comprueban contra el catálogo curado antes de sembrar: si alguno ya estuviera
	// ahí, el upsert de la siembra lo sobrescribiría y la limpieza por origen lo borraría de la base.
	for _, nombre := range []string{nombreDelCatalogo, nombreDelLexico, nombreConMemoria, "ALFA"} {
		if _, existe := servicio.catalogo().Nombres[ClaveDeCatalogo(nombre)]; existe {
			t.Fatalf("el catálogo de la base ya contiene %q", ClaveDeCatalogo(nombre))
		}
	}
	sembrarCatalogoCurado(t, origenDelCatalogo, []repositorio.UpsertCatalogoMarcaParams{{
		NombreNormalizado: ClaveDeCatalogo(nombreDelCatalogo),
		Categoria:         string(CategoriaCompras),
		Origen:            origenDelCatalogo,
		Version:           "prueba",
	}})
	if err := servicio.CargarCatalogo(ctx); err != nil {
		t.Fatalf("CargarCatalogo: %v", err)
	}

	// El escalón 4 lo enciende el lote al cargar el catálogo desde la tabla, no una lista fija.
	escalones := escalonesDelLote(servicio.catalogo(), servicio.clasificador, servicio.lexico.ParesDeLexico())
	for _, escalon := range []int{EscalonMemoria, EscalonInterna, EscalonMCC, EscalonCatalogo,
		EscalonLexico, EscalonClasificador} {
		if !escalones.Contiene(escalon) {
			t.Fatalf("escalones = %v, quiero los seis escalones del lote encendidos", escalones)
		}
	}

	claveDelCatalogo := ClaveDeCatalogo(nombreDelCatalogo)
	if cargada := servicio.catalogo().Nombres[claveDelCatalogo]; cargada != CategoriaCompras {
		t.Fatalf("catálogo = %q para %q, quiero %q", cargada, claveDelCatalogo, CategoriaCompras)
	}

	usuario := uuid.NewString()
	usuarioConMemoria := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	sembrarCuentaDeUsuario(t, pool, usuarioConMemoria)
	insertarMemoria(t, pool, usuarioConMemoria, string(DestinoComercio), NivelNombre,
		NormalizarNombre(nombreConMemoria), CategoriaTransporte)
	t.Cleanup(func() {
		for _, consulta := range []string{
			"DELETE FROM inteligencia.movimiento_enriquecido WHERE usuario_id = ANY($1)",
			"DELETE FROM inteligencia.memoria_correccion WHERE usuario_id = ANY($1)",
		} {
			borrarPorUsuarios(t, pool, consulta, []string{usuario, usuarioConMemoria})
		}
	})

	const cuentaDelRiel = "pasivo:liquidacion_pendiente:riel"
	casos := []casoDeEscalon{
		{
			nombre:            "memoria",
			descripcion:       nombreConMemoria,
			cuentaContraparte: cuentaDelRiel,
			escalon:           EscalonMemoria,
			origen:            OrigenMemoria,
			categoria:         CategoriaTransporte,
			usuarioConMemoria: true,
		},
		{
			nombre:            "contraparte interna",
			descripcion:       textoGMF,
			cuentaContraparte: cuentaGMFPorPagar,
			categoriaInterna:  CategoriaComisiones,
			escalon:           EscalonInterna,
			origen:            OrigenInterna,
			categoria:         CategoriaComisiones,
		},
		{
			nombre:            "mcc sobre el catálogo",
			descripcion:       nombreDelCatalogo,
			mcc:               "5411",
			cuentaContraparte: cuentaDelRiel,
			escalon:           EscalonMCC,
			origen:            OrigenMCC,
			categoria:         CategoriaAlimentosMercado,
		},
		{
			nombre:            "catálogo sobre el léxico",
			descripcion:       nombreDelCatalogo,
			cuentaContraparte: cuentaDelRiel,
			escalon:           EscalonCatalogo,
			origen:            OrigenCatalogo,
			categoria:         CategoriaCompras,
		},
		{
			nombre:            "léxico",
			descripcion:       nombreDelLexico,
			cuentaContraparte: cuentaDelRiel,
			escalon:           EscalonLexico,
			origen:            OrigenLexico,
			categoria:         CategoriaSalud,
		},
		{
			nombre:            "clasificador",
			descripcion:       "ALFA",
			cuentaContraparte: cuentaDelRiel,
			escalon:           EscalonClasificador,
			origen:            OrigenTFIDF,
			categoria:         CategoriaCompras,
		},
		{
			nombre:            "abstención del clasificador",
			descripcion:       "OMEGA " + sufijo,
			cuentaContraparte: cuentaDelRiel,
			escalon:           EscalonClasificador,
			origen:            OrigenSinCategoria,
			categoria:         SinCategoria,
			abstuvo:           true,
		},
	}

	for _, caso := range casos {
		t.Run("decidir/"+caso.nombre, func(t *testing.T) {
			recursos := Recursos{
				Memoria:         map[string]Categoria{},
				CatalogoNombres: servicio.catalogo().Nombres,
				CatalogoLlaves:  servicio.catalogo().Llaves,
				Lexico:          servicio.lexico.ParesDeLexico(),
				Clasificador:    modelo,
			}
			if caso.usuarioConMemoria {
				recursos.Memoria[NormalizarNombre(caso.descripcion)] = caso.categoria
			}
			decision := Decidir(caso.entrada(), recursos, escalones)
			if caso.abstuvo {
				if decision.Escalon != caso.escalon || decision.Categoria != "" ||
					decision.Origen != "" || !decision.PendienteLLM {
					t.Fatalf("decisión = %+v, want abstención en el escalón %d", decision, caso.escalon)
				}
				return
			}
			if decision.Escalon != caso.escalon || decision.Origen != caso.origen ||
				decision.Categoria != caso.categoria {
				t.Fatalf("decisión = %+v, want escalón %d, origen %q y categoría %q",
					decision, caso.escalon, caso.origen, caso.categoria)
			}
		})
	}

	base := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	asientos := make([]int64, len(casos))
	for i, caso := range casos {
		usuarioDelCaso := usuario
		if caso.usuarioConMemoria {
			usuarioDelCaso = usuarioConMemoria
		}
		asientos[i] = sembrarAsiento(t, pool, "despacho_riel", nil, caso.descripcion, caso.mcc,
			base.Add(time.Duration(i)*time.Minute), []lineaDePrueba{
				{cuenta: "pasivo:usuario:" + usuarioDelCaso + ":disponible", monto: 5000},
				{cuenta: caso.cuentaContraparte, monto: -5000},
			})
	}

	// El cursor se mueve al asiento anterior al primero de la prueba para que el lote procese solo
	// estos asientos. Al terminar no se restaura: el ledger es de solo inserción, así que un cursor
	// por delante del último asiento sembrado no salta ninguno y evita que un lote posterior los
	// reprocese.
	cursorAnterior := leerCursorDeEnriquecimiento(t, pool)
	t.Cleanup(func() {
		escribirCursorDeEnriquecimiento(t, pool, mayorDe(append([]int64{cursorAnterior}, asientos...)))
	})
	escribirCursorDeEnriquecimiento(t, pool, menorDe(asientos)-1)
	resultado, err := servicio.Enriquecer(ctx, len(casos))
	if err != nil {
		t.Fatalf("Enriquecer: %v", err)
	}
	if resultado.Movimientos < len(casos) {
		t.Fatalf("movimientos = %d, quiero al menos %d", resultado.Movimientos, len(casos))
	}

	origenes := make(map[string]struct{}, len(casos))
	for i, caso := range casos {
		usuarioDelCaso := usuario
		if caso.usuarioConMemoria {
			usuarioDelCaso = usuarioConMemoria
		}
		transaccion := transaccionDeGrupo(grupo{clave: fmt.Sprintf("entry:%d", asientos[i])})
		leido := leerMovimiento(t, pool, usuarioDelCaso, transaccion)
		if leido.Origen != caso.origen || leido.Categoria != string(caso.categoria) {
			t.Errorf("%s en la copia = origen %q, categoría %q, want origen %q, categoría %q",
				caso.nombre, leido.Origen, leido.Categoria, caso.origen, caso.categoria)
		}
		if leido.Escalon != int16(caso.escalon) {
			t.Errorf("%s en la copia = escalón %d, want %d", caso.nombre, leido.Escalon, caso.escalon)
		}
		origenes[leido.Origen] = struct{}{}
	}
	queridos := make(map[string]struct{}, len(casos))
	for _, caso := range casos {
		queridos[caso.origen] = struct{}{}
	}
	if len(origenes) != len(queridos) {
		t.Fatalf("orígenes distintos en la copia = %v, quiero exactamente los de %v", origenes,
			queridos)
	}
	for origen := range queridos {
		if _, esta := origenes[origen]; !esta {
			t.Fatalf("orígenes distintos en la copia = %v, falta %q", origenes, origen)
		}
	}
}

func menorDe(valores []int64) int64 {
	menor := valores[0]
	for _, valor := range valores[1:] {
		if valor < menor {
			menor = valor
		}
	}
	return menor
}

func mayorDe(valores []int64) int64 {
	mayor := valores[0]
	for _, valor := range valores[1:] {
		if valor > mayor {
			mayor = valor
		}
	}
	return mayor
}

// leerCursorDeEnriquecimiento devuelve el último asiento que el lote ya procesó.
func leerCursorDeEnriquecimiento(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var ultimo int64
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				"SELECT ultimo_entry_id FROM inteligencia.cursor_enriquecimiento WHERE id = 1").Scan(&ultimo)
		})
	if err != nil {
		t.Fatalf("leer el cursor de enriquecimiento: %v", err)
	}
	return ultimo
}

// escribirCursorDeEnriquecimiento mueve el cursor para acotar o restaurar el lote de la prueba.
func escribirCursorDeEnriquecimiento(t *testing.T, pool *pgxpool.Pool, ultimo int64) {
	t.Helper()
	_, err := enTransaccion(context.Background(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx,
				"UPDATE inteligencia.cursor_enriquecimiento SET ultimo_entry_id = $1 WHERE id = 1", ultimo)
			return struct{}{}, err
		})
	if err != nil {
		t.Errorf("escribir el cursor de enriquecimiento: %v", err)
	}
}

// borrarPorUsuarios retira de una tabla las filas de los usuarios de la prueba.
func borrarPorUsuarios(t *testing.T, pool *pgxpool.Pool, consulta string, usuarios []string) {
	t.Helper()
	_, err := enTransaccion(context.Background(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, consulta, usuarios)
			return struct{}{}, err
		})
	if err != nil {
		t.Errorf("limpiar las filas de prueba: %v", err)
	}
}

// escalonDelOrigen es la misma tabla con la que la migración 0031 rellenó la columna.
func escalonDelOrigen(origen string) int16 {
	switch origen {
	case OrigenMemoria:
		return EscalonMemoria
	case OrigenInterna:
		return EscalonInterna
	case OrigenMCC:
		return EscalonMCC
	case OrigenCatalogo:
		return EscalonCatalogo
	case OrigenLexico:
		return EscalonLexico
	case OrigenTFIDF:
		return EscalonClasificador
	default:
		return EscalonSinCategoria
	}
}

// insertarMovimientoParaConteo escribe una fila de la copia con la fecha y el origen pedidos.
func insertarMovimientoParaConteo(t *testing.T, pool *pgxpool.Pool, usuario string, ocurrido time.Time, origen string) {
	t.Helper()
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, q.InsertarMovimientoEnriquecido(ctx, repositorio.InsertarMovimientoEnriquecidoParams{
				UsuarioID:           usuario,
				TransaccionID:       pgtype.UUID{Bytes: uuid.New(), Valid: true},
				EntryAncla:          time.Now().UnixNano(),
				OcurridoEn:          pgtype.Timestamptz{Time: ocurrido, Valid: true},
				Moneda:              "COP",
				Direccion:           "salida",
				MontoCentavos:       1000,
				Contraparte:         "Comercio de prueba",
				ContraparteResuelta: true,
				Categoria:           string(SinCategoria),
				OrigenCategoria:     origen,
				Escalon:             escalonDelOrigen(origen),
				VersionModelo:       VersionModelo,
			})
		})
	if err != nil {
		t.Fatalf("insertar movimiento para el conteo: %v", err)
	}
}

// TestConteoPorOrigenYMesAgrupaLaCopia comprueba la consulta de diagnóstico sobre la copia real.
func TestConteoPorOrigenYMesAgrupaLaCopia(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	usuario := "prueba-conteo-" + uuid.NewString()
	enero := time.Date(1990, 1, 15, 12, 0, 0, 0, time.UTC)
	febrero := time.Date(1990, 2, 10, 12, 0, 0, 0, time.UTC)
	insertarMovimientoParaConteo(t, pool, usuario, enero, OrigenSinCategoria)
	insertarMovimientoParaConteo(t, pool, usuario, enero.Add(time.Hour), OrigenSinCategoria)
	insertarMovimientoParaConteo(t, pool, usuario, febrero, OrigenMCC)
	t.Cleanup(func() {
		_, err := enTransaccion(context.Background(), poolDeInteligencia{pool: pool},
			func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
				_, err := tx.Exec(ctx,
					"DELETE FROM inteligencia.movimiento_enriquecido WHERE usuario_id = $1", usuario)
				return struct{}{}, err
			})
		if err != nil {
			t.Errorf("limpiar la copia de prueba: %v", err)
		}
	})

	filas, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, _ transaccionDeInteligencia,
			q *repositorio.Queries) ([]repositorio.ConteoPorOrigenYMesRow, error) {
			return q.ConteoPorOrigenYMes(ctx)
		})
	if err != nil {
		t.Fatalf("ConteoPorOrigenYMes: %v", err)
	}

	conteos := make(map[string]int64, len(filas))
	for _, fila := range filas {
		if fila.Mes.Time.Day() != 1 {
			t.Fatalf("mes = %s, quiero el primer día del mes", fila.Mes.Time)
		}
		conteos[fila.Mes.Time.UTC().Format("2006-01")+"|"+fila.OrigenCategoria] = fila.Total
	}
	if total := conteos["1990-01|"+OrigenSinCategoria]; total != 2 {
		t.Fatalf("enero con origen %q = %d, quiero 2", OrigenSinCategoria, total)
	}
	if total := conteos["1990-02|"+OrigenMCC]; total != 1 {
		t.Fatalf("febrero con origen %q = %d, quiero 1", OrigenMCC, total)
	}
}
