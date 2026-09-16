package nucleo

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// MotivoMovimiento es el motivo legible de una línea del historial: un conjunto cerrado.
type MotivoMovimiento string

// Los seis motivos derivados que el historial muestra al usuario.
const (
	MotivoDeposito              MotivoMovimiento = "deposito"
	MotivoTransferenciaRecibida MotivoMovimiento = "transferencia_recibida"
	MotivoEnvio                 MotivoMovimiento = "envio"
	MotivoGravamen              MotivoMovimiento = "gravamen"
	MotivoDevolucion            MotivoMovimiento = "devolucion"
	MotivoDevolucionGravamen    MotivoMovimiento = "devolucion_gravamen"
)

// LineaMovimiento es un apunte visible del usuario dentro de un movimiento.
type LineaMovimiento struct {
	Monto  Centavos         `json:"monto"`
	Motivo MotivoMovimiento `json:"motivo"`
}

// Movimiento agrupa los asientos de una misma operación, anclado en su asiento más antiguo.
type Movimiento struct {
	EntryID      int64             `json:"entry_id"`
	OcurridoEn   time.Time         `json:"ocurrido_en"`
	RegistradoEn time.Time         `json:"registrado_en"`
	Moneda       string            `json:"moneda"`
	Lineas       []LineaMovimiento `json:"lineas"`
}

// PaginaDeMovimientos es una página, el cursor de la siguiente y si existe.
type PaginaDeMovimientos struct {
	Movimientos []Movimiento
	Siguiente   *int64
	HayMas      bool
}

// Historial devuelve una página del historial del usuario, del movimiento más reciente al más antiguo.
func (s *Servicio) Historial(ctx context.Context, usuarioID string, cursor *int64, limite int) (PaginaDeMovimientos, error) {
	cuentas := []string{NombreCuentaDisponible(usuarioID), NombreCuentaRetenido(usuarioID)}
	pagina, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (PaginaDeMovimientos, error) {
			return consultarPagina(ctx, q, usuarioID, cuentas, cursor, limite)
		})
	if err != nil {
		return PaginaDeMovimientos{}, fmt.Errorf("nucleo: consultar historial de %q: %w", usuarioID, err)
	}
	return pagina, nil
}

// consultarPagina pagina las anclas, trae las líneas del grupo y las arma como movimientos.
func consultarPagina(ctx context.Context, q *repositorio.Queries, usuarioID string, cuentas []string, cursor *int64, limite int) (PaginaDeMovimientos, error) {
	anclas, err := q.PaginaDeAnclas(ctx, repositorio.PaginaDeAnclasParams{
		Cursor:  cursorDePG(cursor),
		Limite:  int32(limite + 1),
		Cuentas: cuentas,
	})
	if err != nil {
		return PaginaDeMovimientos{}, err
	}
	hayMas := len(anclas) > limite
	if hayMas {
		anclas = anclas[:limite]
	}
	pagina := PaginaDeMovimientos{Movimientos: []Movimiento{}, HayMas: hayMas}
	if len(anclas) == 0 {
		return pagina, nil
	}
	if hayMas {
		ultimo := anclas[len(anclas)-1].Ancla
		pagina.Siguiente = &ultimo
	}

	ids := make([]int64, 0, len(anclas))
	porGrupo := make(map[string]int64, len(anclas))
	for _, a := range anclas {
		ids = append(ids, a.Ancla)
		porGrupo[a.Grupo] = a.Ancla
	}
	filas, err := q.LineasDeMovimientos(ctx, ids)
	if err != nil {
		return PaginaDeMovimientos{}, err
	}
	pagina.Movimientos, err = armarMovimientos(usuarioID, anclas, porGrupo, filas)
	if err != nil {
		return PaginaDeMovimientos{}, err
	}
	return pagina, nil
}

// apunte es una línea del diario con lo mínimo para derivar su motivo.
type apunte struct {
	entryID  int64
	tipo     string
	cuentaID string
	monto    int64
}

// armarMovimientos agrupa las filas por ancla y deriva las líneas visibles de cada movimiento.
func armarMovimientos(usuarioID string, anclas []repositorio.PaginaDeAnclasRow, porGrupo map[string]int64, filas []repositorio.LineasDeMovimientosRow) ([]Movimiento, error) {
	porAncla := make(map[int64][]apunte, len(anclas))
	cabecera := make(map[int64]repositorio.LineasDeMovimientosRow, len(anclas))
	for _, fila := range filas {
		ancla, ok := porGrupo[grupoDeFila(fila)]
		if !ok {
			continue
		}
		porAncla[ancla] = append(porAncla[ancla], apunte{
			entryID:  fila.EntryID,
			tipo:     fila.Tipo,
			cuentaID: fila.CuentaID,
			monto:    fila.Monto,
		})
		if fila.EntryID == ancla {
			cabecera[ancla] = fila
		}
	}
	movimientos := make([]Movimiento, 0, len(anclas))
	for _, a := range anclas {
		lineas, err := derivarLineas(usuarioID, porAncla[a.Ancla])
		if err != nil {
			return nil, err
		}
		cab := cabecera[a.Ancla]
		movimientos = append(movimientos, Movimiento{
			EntryID:      a.Ancla,
			OcurridoEn:   cab.OcurridoEn.Time.UTC(),
			RegistradoEn: cab.RegistradoEn.Time.UTC(),
			Moneda:       cab.Moneda,
			Lineas:       lineas,
		})
	}
	return movimientos, nil
}

// grupoDeFila identifica el movimiento de una fila: su transferencia o su propio asiento.
func grupoDeFila(fila repositorio.LineasDeMovimientosRow) string {
	if fila.TransferenciaID.Valid {
		return uuid.UUID(fila.TransferenciaID.Bytes).String()
	}
	return "entry:" + strconv.FormatInt(fila.EntryID, 10)
}

// derivarLineas muestra solo las líneas de la subcuenta disponible del usuario, con su signo.
func derivarLineas(usuarioID string, apuntes []apunte) ([]LineaMovimiento, error) {
	disponible := NombreCuentaDisponible(usuarioID)
	retenido := NombreCuentaRetenido(usuarioID)

	porAsiento := map[int64][]apunte{}
	orden := make([]int64, 0, len(apuntes))
	for _, a := range apuntes {
		if _, visto := porAsiento[a.entryID]; !visto {
			orden = append(orden, a.entryID)
		}
		porAsiento[a.entryID] = append(porAsiento[a.entryID], a)
	}

	lineas := []LineaMovimiento{}
	for _, entryID := range orden {
		delAsiento := porAsiento[entryID]
		contrapartes := contrapartesPorCuenta(delAsiento)
		for _, a := range delAsiento {
			if a.cuentaID != disponible {
				continue
			}
			motivo, err := motivoDelApunte(a, contrapartes, retenido)
			if err != nil {
				return nil, err
			}
			monto, err := Centavos(0).Restar(Centavos(a.monto))
			if err != nil {
				return nil, err
			}
			lineas = append(lineas, LineaMovimiento{Monto: monto, Motivo: motivo})
		}
	}
	return lineas, nil
}

// contrapartesPorCuenta indexa los valores absolutos de cada cuenta del asiento.
func contrapartesPorCuenta(apuntes []apunte) map[string]map[int64]bool {
	porCuenta := make(map[string]map[int64]bool, len(apuntes))
	for _, a := range apuntes {
		if porCuenta[a.cuentaID] == nil {
			porCuenta[a.cuentaID] = map[int64]bool{}
		}
		porCuenta[a.cuentaID][absInt64(a.monto)] = true
	}
	return porCuenta
}

// motivoDelApunte traduce el tipo del asiento y sus contrapartes a un motivo cerrado.
func motivoDelApunte(a apunte, contrapartes map[string]map[int64]bool, retenido string) (MotivoMovimiento, error) {
	abs := absInt64(a.monto)
	switch a.tipo {
	case TipoAsientoDeposito:
		return MotivoDeposito, nil
	case TipoAsientoTransferenciaInterna:
		return MotivoTransferenciaRecibida, nil
	case TipoAsientoRetencion:
		if contrapartes[retenido][abs] {
			return MotivoEnvio, nil
		}
		if contrapartes[CuentaGMFPorPagar][abs] {
			return MotivoGravamen, nil
		}
	case TipoAsientoCompensacion:
		if contrapartes[CuentaLiquidacionPendienteRiel][abs] {
			return MotivoDevolucion, nil
		}
		if contrapartes[CuentaGMFPorPagar][abs] {
			return MotivoDevolucionGravamen, nil
		}
	}
	return "", fmt.Errorf("nucleo: asiento tipo %q sin motivo para la linea %d de %q", a.tipo, a.monto, a.cuentaID)
}

// absInt64 devuelve el valor absoluto de un monto del diario.
func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// cursorDePG traduce el cursor opcional a su forma nullable de pgx.
func cursorDePG(cursor *int64) pgtype.Int8 {
	if cursor == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *cursor, Valid: true}
}
