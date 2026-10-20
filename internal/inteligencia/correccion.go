package inteligencia

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// LargoNombreComercio es el máximo de puntos de código del nombre visible corregido.
	LargoNombreComercio = 80
)

// ErrMovimientoNoEncontrado reporta un movimiento que no existe para el usuario.
var ErrMovimientoNoEncontrado = errors.New("inteligencia: el movimiento no existe para el usuario")

// ErrCorreccionVacia reporta una corrección sin categoría ni nombre.
var ErrCorreccionVacia = errors.New("inteligencia: la corrección necesita categoría o nombre")

// ErrCopiaSinDestino reporta una fila escrita antes de que la copia guardara su destino.
var ErrCopiaSinDestino = errors.New("inteligencia: la fila no tiene destino hasta reconstruir la copia")

// ErrDestinoGenerico reporta una corrección sobre una etiqueta compartida por varios destinos.
var ErrDestinoGenerico = errors.New("inteligencia: el destino es una etiqueta compartida y no se puede corregir")

// ErrNombreLargo reporta un nombre visible que supera el límite.
var ErrNombreLargo = errors.New("inteligencia: el nombre del comercio supera el largo máximo")

// ErrCorreccionOcupada reporta que el lote de enriquecimiento tiene el candado.
var ErrCorreccionOcupada = errors.New("inteligencia: el lote de enriquecimiento está en curso")

// SolicitudDeCorreccion es lo que el usuario quiere cambiar de un movimiento.
type SolicitudDeCorreccion struct {
	Categoria      *Categoria
	NombreComercio *string
}

// CorreccionAplicada es el movimiento actualizado y cuántas filas cambiaron.
type CorreccionAplicada struct {
	Movimiento     MovimientoDeLectura
	FilasCambiadas int
}

// CorregirMovimiento escribe la memoria del usuario y recalcula las filas del mismo destino.
func (s *Servicio) CorregirMovimiento(ctx context.Context, usuarioID string, transaccionID uuid.UUID,
	solicitud SolicitudDeCorreccion) (CorreccionAplicada, error) {
	if solicitud.Categoria == nil && solicitud.NombreComercio == nil {
		return CorreccionAplicada{}, ErrCorreccionVacia
	}
	if solicitud.Categoria != nil && !EsCategoriaVisible(*solicitud.Categoria) {
		return CorreccionAplicada{}, fmt.Errorf("%w: %q", ErrCategoriaNoVisible, *solicitud.Categoria)
	}
	if solicitud.NombreComercio != nil {
		nombre := strings.TrimSpace(*solicitud.NombreComercio)
		if nombre == "" {
			return CorreccionAplicada{}, ErrNombreVacio
		}
		if utf8.RuneCountInString(nombre) > LargoNombreComercio {
			return CorreccionAplicada{}, ErrNombreLargo
		}
		solicitud.NombreComercio = &nombre
	}

	return enTransaccion(ctx, s.pool,
		func(ctx context.Context, tx transaccionDeInteligencia, q *repositorio.Queries) (CorreccionAplicada, error) {
			if err := bloquearLaCorreccion(ctx, tx); err != nil {
				return CorreccionAplicada{}, err
			}
			fila, err := q.FilaDeMovimiento(ctx, repositorio.FilaDeMovimientoParams{
				UsuarioID:     usuarioID,
				TransaccionID: pgtype.UUID{Bytes: transaccionID, Valid: true},
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return CorreccionAplicada{}, ErrMovimientoNoEncontrado
			}
			if err != nil {
				return CorreccionAplicada{}, fmt.Errorf("inteligencia: leer el movimiento a corregir: %w", err)
			}

			persistido := DestinoPersistido{
				Tipo:              TipoDestino(fila.TipoDestino.String),
				Llave:             fila.ContraparteLlave.String,
				MID:               fila.ComercioMid.String,
				NombreNormalizado: fila.ContraparteNombreNormalizado.String,
			}
			destino, ok := persistido.Destino()
			if !ok {
				return CorreccionAplicada{}, ErrCopiaSinDestino
			}
			if EsDestinoGenerico(fila.Contraparte) {
				return CorreccionAplicada{}, ErrDestinoGenerico
			}
			if solicitud.NombreComercio != nil && destino.Tipo != DestinoComercio {
				return CorreccionAplicada{}, ErrNombreNoAplica
			}

			claves, err := ClavesDe(usuarioID, destino)
			if err != nil {
				return CorreccionAplicada{}, err
			}
			for _, clave := range claves {
				if solicitud.Categoria != nil {
					if err := q.GuardarCategoriaDeMemoria(ctx, repositorio.GuardarCategoriaDeMemoriaParams{
						UsuarioID:   clave.Usuario,
						TipoDestino: string(clave.TipoDestino),
						Nivel:       clave.Nivel,
						Valor:       clave.Valor,
						Categoria:   string(*solicitud.Categoria),
					}); err != nil {
						return CorreccionAplicada{}, fmt.Errorf("inteligencia: guardar la categoría en memoria: %w", err)
					}
				}
				if solicitud.NombreComercio != nil {
					if err := q.GuardarNombreDeMemoria(ctx, repositorio.GuardarNombreDeMemoriaParams{
						UsuarioID:   clave.Usuario,
						TipoDestino: string(clave.TipoDestino),
						Nivel:       clave.Nivel,
						Valor:       clave.Valor,
						Nombre:      *solicitud.NombreComercio,
					}); err != nil {
						return CorreccionAplicada{}, fmt.Errorf("inteligencia: guardar el nombre en memoria: %w", err)
					}
				}
			}

			cambiadas := make(map[uuid.UUID]struct{})
			if solicitud.Categoria != nil {
				ids, err := q.RecalcularCategoriaPorDestino(ctx, repositorio.RecalcularCategoriaPorDestinoParams{
					UsuarioID:         usuarioID,
					TipoDestino:       string(destino.Tipo),
					Llave:             textoODesconocido(persistido.Llave),
					Mid:               textoODesconocido(persistido.MID),
					NombreNormalizado: textoODesconocido(persistido.NombreNormalizado),
				})
				if err != nil {
					return CorreccionAplicada{}, fmt.Errorf("inteligencia: recalcular la categoría: %w", err)
				}
				for _, id := range ids {
					cambiadas[uuid.UUID(id.Bytes)] = struct{}{}
				}
			}
			if solicitud.NombreComercio != nil {
				ids, err := q.RenombrarContrapartePorDestino(ctx, repositorio.RenombrarContrapartePorDestinoParams{
					UsuarioID:         usuarioID,
					Llave:             textoODesconocido(persistido.Llave),
					Mid:               textoODesconocido(persistido.MID),
					NombreNormalizado: textoODesconocido(persistido.NombreNormalizado),
				})
				if err != nil {
					return CorreccionAplicada{}, fmt.Errorf("inteligencia: renombrar la contraparte: %w", err)
				}
				for _, id := range ids {
					cambiadas[uuid.UUID(id.Bytes)] = struct{}{}
				}
			}

			actualizada, err := q.FilaDeMovimiento(ctx, repositorio.FilaDeMovimientoParams{
				UsuarioID:     usuarioID,
				TransaccionID: pgtype.UUID{Bytes: transaccionID, Valid: true},
			})
			if err != nil {
				return CorreccionAplicada{}, fmt.Errorf("inteligencia: releer el movimiento corregido: %w", err)
			}
			return CorreccionAplicada{
				Movimiento:     movimientoDeCorreccion(actualizada),
				FilasCambiadas: len(cambiadas),
			}, nil
		})
}

func bloquearLaCorreccion(ctx context.Context, tx transaccionDeInteligencia) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('inteligencia.enriquecimiento')::bigint)")
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.LockNotAvailable {
		return ErrCorreccionOcupada
	}
	return fmt.Errorf("inteligencia: tomar el candado de la corrección: %w", err)
}

func textoODesconocido(valor string) pgtype.Text {
	return pgtype.Text{String: valor, Valid: valor != ""}
}

func movimientoDeCorreccion(fila repositorio.FilaDeMovimientoRow) MovimientoDeLectura {
	return MovimientoDeLectura{
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
	}
}
