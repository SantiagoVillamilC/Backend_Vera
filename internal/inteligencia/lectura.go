package inteligencia

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Límites de la página de la vista enriquecida: 50 por defecto y 100 como tope.
const (
	LimiteMovimientosPorDefecto = 50
	LimiteMovimientosMaximo     = 100
)

// MesesRequeridosParaEvaluar es el mínimo de meses completos que exige la mediana.
const MesesRequeridosParaEvaluar = 3

// CupoMensualDeAvisos es el tope de avisos por usuario y mes del punto 250.
const CupoMensualDeAvisos = 3

// AlcanceBilletera declara que el resumen solo ve los movimientos de la billetera.
const AlcanceBilletera = "billetera"

// MonedaDeLaBilletera es la moneda con la que se presenta la vista enriquecida.
const MonedaDeLaBilletera = "COP"

// ErrCursorInvalido reporta un cursor que no se puede decodificar.
var ErrCursorInvalido = errors.New("inteligencia: cursor de movimientos inválido")

// ErrMesInvalido reporta un mes que no tiene la forma YYYY-MM.
var ErrMesInvalido = errors.New("inteligencia: mes inválido")

// zonaBogota es el huso de Colombia, sin horario de verano desde 1993.
var zonaBogota = time.FixedZone("America/Bogota", -5*60*60)

// CursorDeMovimiento es el ancla opaca de la página: el último movimiento entregado.
type CursorDeMovimiento struct {
	OcurridoEn    time.Time
	TransaccionID uuid.UUID
}

// CodificarCursor devuelve el cursor opaco de un ancla, listo para meta.next_cursor.
func CodificarCursor(ancla CursorDeMovimiento) string {
	crudo := "v1|" + strconv.FormatInt(ancla.OcurridoEn.UTC().UnixNano(), 10) + "|" + ancla.TransaccionID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(crudo))
}

// DecodificarCursor lee un cursor opaco emitido por CodificarCursor.
func DecodificarCursor(texto string) (CursorDeMovimiento, error) {
	crudo, err := base64.RawURLEncoding.DecodeString(texto)
	if err != nil {
		return CursorDeMovimiento{}, fmt.Errorf("%w: base64", ErrCursorInvalido)
	}
	partes := strings.Split(string(crudo), "|")
	if len(partes) != 3 || partes[0] != "v1" {
		return CursorDeMovimiento{}, fmt.Errorf("%w: forma", ErrCursorInvalido)
	}
	nanos, err := strconv.ParseInt(partes[1], 10, 64)
	if err != nil {
		return CursorDeMovimiento{}, fmt.Errorf("%w: instante", ErrCursorInvalido)
	}
	transaccionID, err := uuid.Parse(partes[2])
	if err != nil {
		return CursorDeMovimiento{}, fmt.Errorf("%w: transacción", ErrCursorInvalido)
	}
	return CursorDeMovimiento{OcurridoEn: time.Unix(0, nanos).UTC(), TransaccionID: transaccionID}, nil
}

// MovimientoDeLectura es una fila de la vista enriquecida apta para la API.
type MovimientoDeLectura struct {
	TransaccionID       uuid.UUID
	OcurridoEn          time.Time
	Moneda              string
	Direccion           string
	MontoCentavos       int64
	Contraparte         string
	ContraparteResuelta bool
	Descripcion         string
	Categoria           string
	CategoriaNombre     string
	OrigenCategoria     string
}

// PaginaDeMovimientos es una página y el ancla de la siguiente, si la hay.
type PaginaDeMovimientos struct {
	Movimientos []MovimientoDeLectura
	Siguiente   *string
	HayMas      bool
}

// Movimientos devuelve una página de la vista enriquecida del usuario, de la más nueva a la más vieja.
func (s *Servicio) Movimientos(ctx context.Context, usuarioID string, ancla *CursorDeMovimiento,
	limite int) (PaginaDeMovimientos, error) {
	if limite <= 0 {
		limite = LimiteMovimientosPorDefecto
	}
	if limite > LimiteMovimientosMaximo {
		limite = LimiteMovimientosMaximo
	}
	parametros := repositorio.PaginaDeMovimientosParams{
		UsuarioID: usuarioID,
		Limite:    int32(limite + 1),
	}
	if ancla != nil {
		parametros.CursorOcurridoEn = pgtype.Timestamptz{Time: ancla.OcurridoEn.UTC(), Valid: true}
		parametros.CursorTransaccionID = pgtype.UUID{Bytes: ancla.TransaccionID, Valid: true}
	}

	filas, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) ([]repositorio.PaginaDeMovimientosRow, error) {
			return q.PaginaDeMovimientos(ctx, parametros)
		})
	if err != nil {
		return PaginaDeMovimientos{}, fmt.Errorf("inteligencia: leer la página de movimientos: %w", err)
	}

	hayMas := len(filas) > limite
	if hayMas {
		filas = filas[:limite]
	}
	pagina := PaginaDeMovimientos{
		Movimientos: make([]MovimientoDeLectura, 0, len(filas)),
		HayMas:      hayMas,
	}
	for _, fila := range filas {
		pagina.Movimientos = append(pagina.Movimientos, MovimientoDeLectura{
			TransaccionID:       uuid.UUID(fila.TransaccionID.Bytes),
			OcurridoEn:          fila.OcurridoEn.Time.UTC(),
			Moneda:              fila.Moneda,
			Direccion:           fila.Direccion,
			MontoCentavos:       fila.MontoCentavos,
			Contraparte:         fila.Contraparte,
			ContraparteResuelta: fila.ContraparteResuelta,
			Descripcion:         fila.Descripcion.String,
			Categoria:           fila.Categoria,
			CategoriaNombre:     fila.CategoriaNombre,
			OrigenCategoria:     fila.OrigenCategoria,
		})
	}
	if hayMas && len(pagina.Movimientos) > 0 {
		ultimo := pagina.Movimientos[len(pagina.Movimientos)-1]
		siguiente := CodificarCursor(CursorDeMovimiento{
			OcurridoEn:    ultimo.OcurridoEn,
			TransaccionID: ultimo.TransaccionID,
		})
		pagina.Siguiente = &siguiente
	}
	return pagina, nil
}

// ResumenDeCategoria es el gasto del mes de una categoría y su referencia histórica.
type ResumenDeCategoria struct {
	Codigo          string
	Nombre          string
	TotalCentavos   int64
	MedianaCentavos *int64
	MesesConGasto   int
}

// ResumenDelMes es el gasto del mes por categoría y la referencia de los meses completos.
type ResumenDelMes struct {
	Mes             string
	Desde           time.Time
	Hasta           time.Time
	Alcance         string
	Moneda          string
	Evaluable       bool
	MesesCompletos  int
	MesesRequeridos int
	TotalCentavos   int64
	CupoAvisos      int
	AvisosEmitidos  int
	Categorias      []ResumenDeCategoria
}

// Resumen agrupa el gasto del mes por categoría y calcula la mediana de los meses completos.
func (s *Servicio) Resumen(ctx context.Context, usuarioID, mes string) (ResumenDelMes, error) {
	inicio, err := inicioDelMes(mes, time.Now())
	if err != nil {
		return ResumenDelMes{}, err
	}
	siguiente := inicio.AddDate(0, 1, 0)

	resumen, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (ResumenDelMes, error) {
			return resumenEnTransaccion(ctx, q, usuarioID, inicio, siguiente)
		})
	if err != nil {
		return ResumenDelMes{}, err
	}
	return resumen, nil
}

func resumenEnTransaccion(ctx context.Context, q *repositorio.Queries, usuarioID string,
	inicio, siguiente time.Time) (ResumenDelMes, error) {
	primero, err := q.PrimerMovimientoDelUsuario(ctx, usuarioID)
	if err != nil {
		return ResumenDelMes{}, fmt.Errorf("inteligencia: leer el primer movimiento: %w", err)
	}

	var meses []time.Time
	desde := inicio
	if primero.Valid {
		primeroLocal := primero.Time.In(zonaBogota)
		meses = mesesCompletosAntes(primeroLocal, inicio)
		if desdePrimero := inicioDeMesDe(primeroLocal); desdePrimero.Before(desde) {
			desde = desdePrimero
		}
	}

	filas, err := q.TotalesPorCategoriaYMes(ctx, repositorio.TotalesPorCategoriaYMesParams{
		UsuarioID: usuarioID,
		Desde:     pgtype.Timestamptz{Time: desde.UTC(), Valid: true},
		Hasta:     pgtype.Timestamptz{Time: siguiente.UTC(), Valid: true},
	})
	if err != nil {
		return ResumenDelMes{}, fmt.Errorf("inteligencia: leer los totales del mes: %w", err)
	}

	avisos, err := q.ContarAvisosDelMes(ctx, repositorio.ContarAvisosDelMesParams{
		UsuarioID: usuarioID,
		Desde:     pgtype.Date{Time: fechaCivil(inicio), Valid: true},
		Hasta:     pgtype.Date{Time: fechaCivil(siguiente), Valid: true},
	})
	if err != nil {
		return ResumenDelMes{}, fmt.Errorf("inteligencia: contar los avisos del mes: %w", err)
	}

	resumen := ResumenDelMes{
		Mes:             inicio.Format("2006-01"),
		Desde:           inicio.UTC(),
		Hasta:           siguiente.UTC(),
		Alcance:         AlcanceBilletera,
		Moneda:          MonedaDeLaBilletera,
		Evaluable:       len(meses) >= MesesRequeridosParaEvaluar,
		MesesCompletos:  len(meses),
		MesesRequeridos: MesesRequeridosParaEvaluar,
		CupoAvisos:      CupoMensualDeAvisos,
		AvisosEmitidos:  int(avisos),
	}
	resumen.Categorias = categoriasDelMes(filas, meses, claveDeMes(inicio))
	for _, categoria := range resumen.Categorias {
		resumen.TotalCentavos += categoria.TotalCentavos
	}
	return resumen, nil
}

type acumuladoDeCategoria struct {
	nombre string
	porMes map[int]int64
}

func categoriasDelMes(filas []repositorio.TotalesPorCategoriaYMesRow, meses []time.Time,
	claveDelMes int) []ResumenDeCategoria {
	acumulados := make(map[string]*acumuladoDeCategoria)
	for _, fila := range filas {
		acumulado, conocido := acumulados[fila.Categoria]
		if !conocido {
			acumulado = &acumuladoDeCategoria{nombre: fila.CategoriaNombre, porMes: map[int]int64{}}
			acumulados[fila.Categoria] = acumulado
		}
		acumulado.porMes[claveDeFecha(fila.Mes.Time)] += fila.TotalCentavos
	}

	categorias := make([]ResumenDeCategoria, 0, len(acumulados))
	for codigo, acumulado := range acumulados {
		total := acumulado.porMes[claveDelMes]
		if total == 0 {
			continue
		}
		valores := make([]int64, 0, len(meses))
		for _, mes := range meses {
			if valor := acumulado.porMes[claveDeMes(mes)]; valor > 0 {
				valores = append(valores, valor)
			}
		}
		categoria := ResumenDeCategoria{
			Codigo:        codigo,
			Nombre:        acumulado.nombre,
			TotalCentavos: total,
			MesesConGasto: len(valores),
		}
		if codigo != string(SinCategoria) && len(valores) >= MesesRequeridosParaEvaluar {
			mediana := medianaEnCentavos(valores)
			categoria.MedianaCentavos = &mediana
		}
		categorias = append(categorias, categoria)
	}
	sort.Slice(categorias, func(i, j int) bool {
		if categorias[i].TotalCentavos != categorias[j].TotalCentavos {
			return categorias[i].TotalCentavos > categorias[j].TotalCentavos
		}
		return categorias[i].Codigo < categorias[j].Codigo
	})
	return categorias
}

// medianaEnCentavos devuelve la mediana de valores positivos, redondeada medio a par.
func medianaEnCentavos(valores []int64) int64 {
	ordenados := make([]int64, len(valores))
	copy(ordenados, valores)
	sort.Slice(ordenados, func(i, j int) bool { return ordenados[i] < ordenados[j] })
	medio := len(ordenados) / 2
	if len(ordenados)%2 == 1 {
		return ordenados[medio]
	}
	return redondearMitadAPar(ordenados[medio-1] + ordenados[medio])
}

func redondearMitadAPar(suma int64) int64 {
	cociente := suma / 2
	if suma%2 == 0 || cociente%2 == 0 {
		return cociente
	}
	return cociente + 1
}

// mesesCompletosAntes porta meses_completos_antes de la referencia: el mes del primer
// movimiento cuenta solo si empezó el día 1, y se listan hasta el mes límite, excluido.
func mesesCompletosAntes(primeroLocal, limite time.Time) []time.Time {
	mes := inicioDeMesDe(primeroLocal)
	if primeroLocal.Day() != 1 {
		mes = mes.AddDate(0, 1, 0)
	}
	var meses []time.Time
	for mes.Before(limite) {
		meses = append(meses, mes)
		mes = mes.AddDate(0, 1, 0)
	}
	return meses
}

func inicioDeMesDe(instante time.Time) time.Time {
	local := instante.In(zonaBogota)
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, zonaBogota)
}

func inicioDelMes(mes string, ahora time.Time) (time.Time, error) {
	if mes == "" {
		return inicioDeMesDe(ahora), nil
	}
	inicio, err := time.ParseInLocation("2006-01", mes, zonaBogota)
	if err != nil || inicio.Format("2006-01") != mes {
		return time.Time{}, fmt.Errorf("%w: %q", ErrMesInvalido, mes)
	}
	return inicio, nil
}

func claveDeMes(mes time.Time) int {
	local := mes.In(zonaBogota)
	return local.Year()*12 + int(local.Month()) - 1
}

func claveDeFecha(fecha time.Time) int {
	return fecha.Year()*12 + int(fecha.Month()) - 1
}

func fechaCivil(instante time.Time) time.Time {
	local := instante.In(zonaBogota)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}
