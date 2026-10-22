package nucleo

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrRangoMovimientos rechaza fechas, filtros y agrupaciones fuera del contrato.
var ErrRangoMovimientos = errors.New("nucleo: rango o filtro de movimientos inválido")

// RangoMovimientosColombia transforma fechas inclusivas en un intervalo [desde,hasta).
func RangoMovimientosColombia(desde, hasta string) (time.Time, time.Time, error) {
	a, e1 := time.ParseInLocation(time.DateOnly, desde, zonaHorariaCorte)
	b, e2 := time.ParseInLocation(time.DateOnly, hasta, zonaHorariaCorte)
	if e1 != nil || e2 != nil || a.Year() < 1 || a.After(b) {
		return time.Time{}, time.Time{}, ErrRangoMovimientos
	}
	fin := b.AddDate(0, 0, 1)
	if fin.Sub(a) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, ErrRangoMovimientos
	}
	return a, fin, nil
}

// MovimientoPeriodo representa un único asiento, con sus componentes públicos.
// ContraparteRef es sólo para resolución interna: nunca se entrega al modelo.
type MovimientoPeriodo struct {
	Fecha          time.Time
	Lineas         []LineaMovimiento
	ContraparteRef string
}

// PaginaPeriodo limita la vista pero conserva el conteo completo del filtro.
type PaginaPeriodo struct {
	Movimientos []MovimientoPeriodo
	Total       int64
	HayMas      bool
}

// GrupoPeriodo conserva totales exactos y conteo de asientos distintos del grupo.
type GrupoPeriodo struct {
	Clave                          string
	Entradas, Salidas, Neto, Total Centavos
	Cantidad                       int
}

// ResumenPeriodo incluye todos los asientos aunque sólo muestre diez grupos.
type ResumenPeriodo struct {
	Entradas, Salidas, Neto Centavos
	Cantidad                int
	Grupos                  []GrupoPeriodo
	HayMas                  bool
}

// MovimientosEnRango lista asientos propios por fecha descendente, con hasta veinte resultados.
func (s *Servicio) MovimientosEnRango(ctx context.Context, usuario, desde, hasta string, tipo MotivoMovimiento, limite int) (PaginaPeriodo, error) {
	pagina := PaginaPeriodo{Movimientos: make([]MovimientoPeriodo, 0)}
	if limite < 1 || limite > 20 || !motivoPeriodoValido(tipo) {
		return pagina, ErrRangoMovimientos
	}
	err := s.recorrerPeriodo(ctx, usuario, desde, hasta, func(m MovimientoPeriodo) error {
		if tipo != "" {
			seleccion := make([]LineaMovimiento, 0)
			for _, l := range m.Lineas {
				if l.Motivo == tipo {
					seleccion = append(seleccion, l)
				}
			}
			m.Lineas = seleccion
		}
		if len(m.Lineas) == 0 {
			return nil
		}
		pagina.Total++
		if len(pagina.Movimientos) < limite {
			pagina.Movimientos = append(pagina.Movimientos, m)
		}
		return nil
	})
	pagina.HayMas = pagina.Total > int64(len(pagina.Movimientos))
	return pagina, err
}

// ResumirMovimientos agrupa componentes con Centavos y cuenta cada asiento una sola vez.
func (s *Servicio) ResumirMovimientos(ctx context.Context, usuario, desde, hasta, agrupar string) (ResumenPeriodo, error) {
	if agrupar != "motivo" && agrupar != "contraparte" {
		return ResumenPeriodo{}, ErrRangoMovimientos
	}
	a := nuevoResumenPeriodo(agrupar)
	if err := s.recorrerPeriodo(ctx, usuario, desde, hasta, a.anexar); err != nil {
		return ResumenPeriodo{}, err
	}
	return a.finalizar()
}

func motivoPeriodoValido(m MotivoMovimiento) bool {
	switch m {
	case "", MotivoDeposito, MotivoTransferenciaRecibida, MotivoEnvio, MotivoGravamen, MotivoDevolucion, MotivoDevolucionGravamen:
		return true
	}
	return false
}

func (s *Servicio) recorrerPeriodo(ctx context.Context, usuario, desde, hasta string, visitar func(MovimientoPeriodo) error) error {
	inicio, fin, err := RangoMovimientosColombia(desde, hasta)
	if err != nil {
		return err
	}
	if usuario == "" {
		return ErrUsuarioVacio
	}
	_, err = enTransaccion(ctx, s.pool, func(ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
		// La fecha, total, páginas y resumen pertenecen al mismo snapshot, aun con inserciones concurrentes.
		if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY"); err != nil {
			return struct{}{}, err
		}
		p := repositorio.ApuntesDePeriodoParams{Disponible: NombreCuentaDisponible(usuario), Desde: pgtype.Timestamptz{Time: inicio, Valid: true}, Hasta: pgtype.Timestamptz{Time: fin, Valid: true}}
		for {
			filas, err := q.ApuntesDePeriodo(ctx, p)
			if err != nil {
				return struct{}{}, err
			}
			if len(filas) == 0 {
				break
			}
			asientos := 0
			for i := 0; i < len(filas); {
				if err := ctx.Err(); err != nil {
					return struct{}{}, err
				}
				cab := filas[i]
				apuntes := make([]apunte, 0)
				j := i
				for j < len(filas) && filas[j].EntryID == cab.EntryID {
					f := filas[j]
					apuntes = append(apuntes, apunte{entryID: f.EntryID, tipo: f.Tipo, cuentaID: f.CuentaID, monto: f.Monto})
					j++
				}
				lineas, err := derivarLineas(usuario, apuntes)
				if err != nil {
					return struct{}{}, err
				}
				if len(lineas) > 0 {
					if err := visitar(MovimientoPeriodo{Fecha: cab.OcurridoEn.Time, Lineas: lineas, ContraparteRef: contrapartePeriodo(usuario, cab)}); err != nil {
						return struct{}{}, err
					}
				}
				p.CursorFecha = cab.OcurridoEn
				p.CursorID = cab.EntryID
				i = j
				asientos++
			}
			if asientos < 500 {
				break
			}
		}
		return struct{}{}, nil
	})
	return err
}

func contrapartePeriodo(usuario string, f repositorio.ApuntesDePeriodoRow) string {
	if f.Emisor == "" {
		return ""
	}
	if f.Emisor != usuario {
		return f.Emisor
	}
	if f.Receptor != "" {
		return f.Receptor
	}
	if id, err := uuid.Parse(strings.TrimPrefix(f.Destino, "intra:")); err == nil && id != uuid.Nil {
		return id.String()
	}
	if f.Destino != "" {
		return "destino:" + f.Destino
	}
	return ""
}

type resumenPeriodo struct {
	agrupar           string
	entradas, salidas Centavos
	cantidad          int
	grupos            map[string]*GrupoPeriodo
}

func nuevoResumenPeriodo(agrupar string) *resumenPeriodo {
	return &resumenPeriodo{agrupar: agrupar, grupos: make(map[string]*GrupoPeriodo)}
}

func acumularPeriodo(entradas, salidas *Centavos, monto Centavos) error {
	var err error
	if monto >= 0 {
		*entradas, err = entradas.Sumar(monto)
	} else {
		var valor Centavos
		valor, err = Centavos(0).Restar(monto)
		if err == nil {
			*salidas, err = salidas.Sumar(valor)
		}
	}
	return err
}

func (a *resumenPeriodo) anexar(m MovimientoPeriodo) error {
	if len(m.Lineas) == 0 {
		return nil
	}
	a.cantidad++
	vistos := make(map[string]bool)
	for _, l := range m.Lineas {
		if err := acumularPeriodo(&a.entradas, &a.salidas, l.Monto); err != nil {
			return err
		}
		clave := m.ContraparteRef
		if a.agrupar == "motivo" {
			clave = string(l.Motivo)
		}
		g := a.grupos[clave]
		if g == nil {
			g = &GrupoPeriodo{Clave: clave}
			a.grupos[clave] = g
		}
		if err := acumularPeriodo(&g.Entradas, &g.Salidas, l.Monto); err != nil {
			return err
		}
		if !vistos[clave] {
			g.Cantidad++
			vistos[clave] = true
		}
	}
	return nil
}

func (a *resumenPeriodo) finalizar() (ResumenPeriodo, error) {
	neto, err := a.entradas.Restar(a.salidas)
	if err != nil {
		return ResumenPeriodo{}, err
	}
	r := ResumenPeriodo{Entradas: a.entradas, Salidas: a.salidas, Neto: neto, Cantidad: a.cantidad, Grupos: make([]GrupoPeriodo, 0, len(a.grupos))}
	for _, g := range a.grupos {
		g.Neto, err = g.Entradas.Restar(g.Salidas)
		if err != nil {
			return ResumenPeriodo{}, err
		}
		g.Total, err = g.Entradas.Sumar(g.Salidas)
		if err != nil {
			return ResumenPeriodo{}, err
		}
		r.Grupos = append(r.Grupos, *g)
	}
	sort.Slice(r.Grupos, func(i, j int) bool {
		if r.Grupos[i].Total == r.Grupos[j].Total {
			return r.Grupos[i].Clave < r.Grupos[j].Clave
		}
		return r.Grupos[i].Total > r.Grupos[j].Total
	})
	r.HayMas = len(r.Grupos) > 10
	if r.HayMas {
		r.Grupos = r.Grupos[:10]
	}
	return r, nil
}
