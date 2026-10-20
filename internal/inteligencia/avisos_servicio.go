package inteligencia

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrAvisoNoEncontrado reporta un aviso inexistente o de otro usuario. Los dos casos son el mismo
// para no filtrar existencia.
var ErrAvisoNoEncontrado = errors.New("inteligencia: aviso no encontrado")

// EtiquetasGenericas son las contrapartes que no identifican un destino recurrente. Salen de la
// misma lista que usa la corrección para rechazar un destino genérico.
var EtiquetasGenericas = []string{
	"Usuario de la billetera",
	"Comercio o red externa",
	"Banco custodio",
	"GMF (impuesto)",
	"Comisión de la billetera",
	"Movimiento",
}

// AvisoEmitido es una tarjeta guardada, lista para la bandeja.
type AvisoEmitido struct {
	ID                uuid.UUID
	Tipo              avisos.Tipo
	Clave             string
	Titulo            string
	Texto             string
	Porque            string
	Accion            string
	Periodo           time.Time
	SeveridadCentavos int64
	Prioridad         int
	TransaccionID     *uuid.UUID
	CreadoEn          time.Time
}

// DescarteDeAviso es el resultado de descartar un aviso.
type DescarteDeAviso struct {
	AvisoID      uuid.UUID
	DescartadoEn time.Time
}

// ConConfiguracionDeAvisos cambia la configuración del emisor. Sin ella usa la de la referencia.
func ConConfiguracionDeAvisos(cfg avisos.Configuracion) OpcionServicio {
	return func(s *Servicio) { s.avisosCfg = cfg }
}

// EmitirAvisos evalúa a todos los usuarios con historial y guarda los avisos que disparan.
func (s *Servicio) EmitirAvisos(ctx context.Context, ahora time.Time) (int, error) {
	emitidos := 0
	despuesDe := ""
	for {
		var usuarios []string
		_, err := enTransaccion(ctx, s.pool,
			func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (struct{}, error) {
				filas, err := q.UsuariosConHistorial(ctx, repositorio.UsuariosConHistorialParams{
					DespuesDe: despuesDe, Limite: 200,
				})
				usuarios = filas
				return struct{}{}, err
			})
		if err != nil {
			return emitidos, fmt.Errorf("inteligencia: listar usuarios para avisos: %w", err)
		}
		if len(usuarios) == 0 {
			return emitidos, nil
		}
		for _, usuario := range usuarios {
			insertados, err := s.emitirAvisosDeUsuario(ctx, usuario, ahora)
			if err != nil {
				return emitidos, err
			}
			emitidos += insertados
		}
		despuesDe = usuarios[len(usuarios)-1]
	}
}

func (s *Servicio) emitirAvisosDeUsuario(ctx context.Context, usuarioID string, ahora time.Time) (int, error) {
	insertados := 0
	_, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, tx transaccionDeInteligencia, q *repositorio.Queries) (struct{}, error) {
			if err := tomarElCandadoDeAvisos(ctx, tx); err != nil {
				return struct{}{}, err
			}
			contexto, err := s.contextoDeAvisos(ctx, q, usuarioID, ahora)
			if err != nil {
				return struct{}{}, err
			}
			hechos, err := avisos.HechosDelDia(contexto)
			if err != nil {
				return struct{}{}, err
			}
			yaEmitidas, emitidosDelMes, err := estadoDeAvisos(ctx, q, usuarioID, ahora)
			if err != nil {
				return struct{}{}, err
			}
			tarjetas, err := avisos.TarjetasDelDia(hechos, yaEmitidas, emitidosDelMes, s.avisosCfg.TopeAvisosMes)
			if err != nil {
				return struct{}{}, err
			}
			for _, tarjeta := range tarjetas {
				nuevo, err := guardarAviso(ctx, q, usuarioID, tarjeta, ahora)
				if err != nil {
					return struct{}{}, err
				}
				if nuevo {
					insertados++
				}
			}
			return struct{}{}, nil
		})
	if err != nil {
		return insertados, err
	}
	return insertados, nil
}

// contextoDeAvisos arma el contexto del usuario desde la copia y el ledger. Todo se corta en ahora.
func (s *Servicio) contextoDeAvisos(ctx context.Context, q *repositorio.Queries,
	usuarioID string, ahora time.Time) (avisos.Contexto, error) {
	instante := avisos.Instantaneo{
		GastoCategoria: map[string]map[avisos.Mes]int64{},
		IngresoMes:     map[avisos.Mes]int64{},
	}
	primero, err := q.PrimerMovimientoDelUsuario(ctx, usuarioID)
	if err != nil {
		return avisos.Contexto{}, fmt.Errorf("inteligencia: leer el primer movimiento de %q: %w", usuarioID, err)
	}
	if primero.Valid {
		momento := primero.Time
		instante.Primero = &momento
	}

	gastos, err := q.GastoPorCategoriaYMesParaAvisos(ctx, repositorio.GastoPorCategoriaYMesParaAvisosParams{
		UsuarioID: usuarioID,
		Ahora:     pgtype.Timestamptz{Time: ahora, Valid: true},
	})
	if err != nil {
		return avisos.Contexto{}, fmt.Errorf("inteligencia: leer el gasto de %q: %w", usuarioID, err)
	}
	for _, fila := range gastos {
		mes := mesDeFechaCivil(fila.Mes.Time)
		if instante.GastoCategoria[fila.Categoria] == nil {
			instante.GastoCategoria[fila.Categoria] = map[avisos.Mes]int64{}
		}
		instante.GastoCategoria[fila.Categoria][mes] += fila.TotalCentavos
	}

	ingresos, err := q.IngresoPorMesParaAvisos(ctx, repositorio.IngresoPorMesParaAvisosParams{
		UsuarioID:              usuarioID,
		TiposAsiento:           s.avisosCfg.TiposAsientoIngreso,
		DescripcionesExcluidas: s.avisosCfg.DescripcionesNoIngreso,
		Ahora:                  pgtype.Timestamptz{Time: ahora, Valid: true},
	})
	if err != nil {
		return avisos.Contexto{}, fmt.Errorf("inteligencia: leer el ingreso de %q: %w", usuarioID, err)
	}
	for _, fila := range ingresos {
		instante.IngresoMes[mesDeFechaCivil(fila.Mes.Time)] += fila.TotalCentavos
	}

	pagos, err := q.PagosDelUsuarioParaSeries(ctx, repositorio.PagosDelUsuarioParaSeriesParams{
		UsuarioID:          usuarioID,
		EtiquetasGenericas: EtiquetasGenericas,
		Ahora:              pgtype.Timestamptz{Time: ahora, Valid: true},
	})
	if err != nil {
		return avisos.Contexto{}, fmt.Errorf("inteligencia: leer los pagos de %q: %w", usuarioID, err)
	}
	for _, fila := range pagos {
		pago := avisos.Pago{
			OcurridoEn:    fila.OcurridoEn.Time,
			FechaLocal:    fila.OcurridoEn.Time.In(avisos.Zona),
			MontoCentavos: fila.MontoCentavos,
			Categoria:     fila.Categoria,
		}
		if fila.TransaccionID.Valid {
			pago.TransaccionID = uuid.UUID(fila.TransaccionID.Bytes).String()
		}
		switch {
		case fila.ContraparteLlave.Valid && fila.ContraparteLlave.String != "":
			pago.Identidad = avisos.Identidad{Tipo: "llave", Valor: fila.ContraparteLlave.String}
		case fila.ContraparteNombreNormalizado.Valid && fila.ContraparteNombreNormalizado.String != "":
			pago.Identidad = avisos.Identidad{Tipo: "nombre", Valor: fila.ContraparteNombreNormalizado.String}
		default:
			continue
		}
		instante.Pagos = append(instante.Pagos, pago)
	}

	saldo, err := q.SaldoDelUsuarioHasta(ctx, repositorio.SaldoDelUsuarioHastaParams{
		UsuarioID: usuarioID,
		Ahora:     pgtype.Timestamptz{Time: ahora, Valid: true},
	})
	if err != nil {
		return avisos.Contexto{}, fmt.Errorf("inteligencia: leer el saldo de %q: %w", usuarioID, err)
	}
	// En el diario una subcuenta pasivo acumula negativo: el saldo del usuario es cero menos la suma.
	instante.SaldoCentavos = -saldo

	return avisos.NuevoContexto(instante, ahora, s.avisosCfg)
}

func estadoDeAvisos(ctx context.Context, q *repositorio.Queries, usuarioID string,
	ahora time.Time) (map[avisos.ClaveDeAviso]struct{}, int, error) {
	desde := inicioDelMesDe(ahora, 0)
	hasta := inicioDelMesDe(ahora, 1)
	filas, err := q.AvisosDelUsuarioEnVentana(ctx, repositorio.AvisosDelUsuarioEnVentanaParams{
		UsuarioID: usuarioID,
		Desde:     pgtype.Date{Time: desde.AddDate(0, -3, 0), Valid: true},
		Hasta:     pgtype.Date{Time: hasta.AddDate(0, 3, 0), Valid: true},
	})
	if err != nil {
		return nil, 0, fmt.Errorf("inteligencia: leer los avisos de %q: %w", usuarioID, err)
	}
	yaEmitidas := make(map[avisos.ClaveDeAviso]struct{}, len(filas))
	for _, fila := range filas {
		yaEmitidas[avisos.ClaveDeAviso{
			Tipo:    avisos.Tipo(fila.Tipo),
			Clave:   fila.Clave,
			Periodo: fila.Periodo.Time.Format("2006-01-02"),
		}] = struct{}{}
	}
	emitidos, err := q.ContarAvisosEmitidosEnElMes(ctx, repositorio.ContarAvisosEmitidosEnElMesParams{
		UsuarioID: usuarioID,
		Desde:     pgtype.Timestamptz{Time: desde, Valid: true},
		Hasta:     pgtype.Timestamptz{Time: hasta, Valid: true},
	})
	if err != nil {
		return nil, 0, fmt.Errorf("inteligencia: contar los avisos de %q: %w", usuarioID, err)
	}
	return yaEmitidas, int(emitidos), nil
}

// guardarAviso inserta la tarjeta sin reescribir un aviso ya emitido. Devuelve si insertó.
func guardarAviso(ctx context.Context, q *repositorio.Queries, usuarioID string,
	tarjeta avisos.Tarjeta, ahora time.Time) (bool, error) {
	id := uuid.New()
	_, err := q.EmitirAviso(ctx, repositorio.EmitirAvisoParams{
		ID:                pgtype.UUID{Bytes: id, Valid: true},
		UsuarioID:         usuarioID,
		Tipo:              string(tarjeta.Tipo),
		Clave:             tarjeta.Clave,
		Periodo:           pgtype.Date{Time: tarjeta.Periodo, Valid: true},
		SeveridadCentavos: avisos.SeveridadDe(tarjeta),
		Prioridad:         avisos.Prioridad(tarjeta.Tipo),
		Texto:             tarjeta.Texto,
		TextoVerificado:   true,
		CreadoEn:          pgtype.Timestamptz{Time: ahora, Valid: true},
	})
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("inteligencia: emitir el aviso %s de %q: %w", tarjeta.Tipo, usuarioID, err)
	}
	// El conflicto de la clave única no devuelve fila: se lee el aviso existente y se conserva tal cual.
	existente, err := q.AvisoPorIdentidad(ctx, repositorio.AvisoPorIdentidadParams{
		UsuarioID: usuarioID, Tipo: string(tarjeta.Tipo), Clave: tarjeta.Clave,
		Periodo: pgtype.Date{Time: tarjeta.Periodo, Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("inteligencia: leer el aviso ya emitido de %q: %w", usuarioID, err)
	}
	if !existente.ID.Valid {
		return false, fmt.Errorf("inteligencia: el aviso %s de %q quedó sin identificar", tarjeta.Tipo, usuarioID)
	}
	return false, nil
}

// AvisosDelUsuario devuelve las tarjetas vigentes del mes pedido, en orden de prioridad y severidad.
func (s *Servicio) AvisosDelUsuario(ctx context.Context, usuarioID, mes string, ahora time.Time) ([]AvisoEmitido, error) {
	desde, err := inicioDelMesPedido(mes, ahora)
	if err != nil {
		return nil, err
	}
	hasta := desde.AddDate(0, 1, 0)
	filas, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) ([]repositorio.AvisosVigentesDelMesRow, error) {
			return q.AvisosVigentesDelMes(ctx, repositorio.AvisosVigentesDelMesParams{
				UsuarioID: usuarioID,
				Desde:     pgtype.Timestamptz{Time: desde, Valid: true},
				Hasta:     pgtype.Timestamptz{Time: hasta, Valid: true},
			})
		})
	if err != nil {
		return nil, fmt.Errorf("inteligencia: listar los avisos de %q: %w", usuarioID, err)
	}
	avisosDelMes := make([]AvisoEmitido, 0, len(filas))
	for _, fila := range filas {
		tipo := avisos.Tipo(fila.Tipo)
		aviso := AvisoEmitido{
			ID:                uuid.UUID(fila.ID.Bytes),
			Tipo:              tipo,
			Clave:             fila.Clave,
			Titulo:            avisos.TituloDe(tipo, fila.Clave),
			Texto:             fila.Texto,
			Porque:            avisos.PorqueDe(tipo),
			Accion:            avisos.AccionDe(tipo),
			Periodo:           fila.Periodo.Time,
			SeveridadCentavos: fila.SeveridadCentavos,
			Prioridad:         int(fila.Prioridad),
			CreadoEn:          fila.CreadoEn.Time,
		}
		if fila.TransaccionID.Valid {
			transaccion := uuid.UUID(fila.TransaccionID.Bytes)
			aviso.TransaccionID = &transaccion
		}
		avisosDelMes = append(avisosDelMes, aviso)
	}
	return avisosDelMes, nil
}

// DescartarAviso marca un aviso propio como descartado. Repetir la llamada conserva el primer
// instante y un aviso ajeno o inexistente devuelve ErrAvisoNoEncontrado.
func (s *Servicio) DescartarAviso(ctx context.Context, usuarioID string, avisoID uuid.UUID,
	ahora time.Time) (DescarteDeAviso, error) {
	fila, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (repositorio.DescartarAvisoRow, error) {
			return q.DescartarAviso(ctx, repositorio.DescartarAvisoParams{
				ID:           pgtype.UUID{Bytes: avisoID, Valid: true},
				UsuarioID:    usuarioID,
				DescartadoEn: pgtype.Timestamptz{Time: ahora, Valid: true},
			})
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return DescarteDeAviso{}, ErrAvisoNoEncontrado
	}
	if err != nil {
		return DescarteDeAviso{}, fmt.Errorf("inteligencia: descartar el aviso %s de %q: %w", avisoID, usuarioID, err)
	}
	return DescarteDeAviso{AvisoID: uuid.UUID(fila.ID.Bytes), DescartadoEn: fila.DescartadoEn.Time}, nil
}

// mesDeFechaCivil lee el mes de una columna date. pgx la entrega como medianoche UTC, así que
// convertirla a la zona local correría el día 1 al mes anterior.
func mesDeFechaCivil(fecha time.Time) avisos.Mes {
	return avisos.Mes{Anio: fecha.Year(), Mes: fecha.Month()}
}

func tomarElCandadoDeAvisos(ctx context.Context, tx transaccionDeInteligencia) error {
	if _, err := tx.Exec(ctx,
		"SELECT pg_advisory_xact_lock(hashtext('inteligencia.avisos')::bigint)"); err != nil {
		return fmt.Errorf("inteligencia: tomar el candado de avisos: %w", err)
	}
	return nil
}

// inicioDelMesDe devuelve el primer instante del mes de ahora desplazado en meses, en la zona local.
func inicioDelMesDe(ahora time.Time, desplazamiento int) time.Time {
	local := ahora.In(avisos.Zona)
	primero := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, avisos.Zona)
	return primero.AddDate(0, desplazamiento, 0)
}

// inicioDelMesPedido valida YYYY-MM y devuelve su primer instante. Un mes vacío es el mes en curso.
func inicioDelMesPedido(mes string, ahora time.Time) (time.Time, error) {
	if mes == "" {
		return inicioDelMesDe(ahora, 0), nil
	}
	valor, err := time.ParseInLocation("2006-01", mes, avisos.Zona)
	if err != nil {
		return time.Time{}, ErrMesInvalido
	}
	return valor, nil
}
