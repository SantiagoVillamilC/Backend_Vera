package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ResolutorDestino resuelve la llave de destino de un envío a un usuario de la billetera.
type ResolutorDestino struct {
	pool         abridorDeTransaccionesAuth
	enfriamiento time.Duration
}

// errEnfriamientoNoPositivo reporta la construcción del resolutor sin una ventana de enfriamiento positiva.
var errEnfriamientoNoPositivo = errors.New("auth: NuevoResolutorDestino necesita un enfriamiento positivo")

// NuevoResolutorDestino construye el resolutor sobre el bloque de auth y la ventana de enfriamiento.
func NuevoResolutorDestino(pool *pgxpool.Pool, enfriamiento time.Duration) *ResolutorDestino {
	if enfriamiento <= 0 {
		panic(errEnfriamientoNoPositivo)
	}
	return &ResolutorDestino{pool: poolDeAuth{pool: pool}, enfriamiento: enfriamiento}
}

// UsuarioIDDeDestino acepta el UUID de una parte no cerrada o el celular verificado de una persona activa.
func (r *ResolutorDestino) UsuarioIDDeDestino(ctx context.Context, llave string) (string, bool, error) {
	if id, err := uuid.Parse(llave); err == nil {
		return r.usuarioPorID(ctx, id)
	}
	return r.usuarioPorCelular(ctx, llave)
}

// usuarioPorID resuelve cualquier parte no cerrada por su uuid, sin aplicar el enfriamiento.
func (r *ResolutorDestino) usuarioPorID(ctx context.Context, id uuid.UUID) (string, bool, error) {
	fila, err := enTransaccion(ctx, r.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (pgtype.UUID, error) {
		return q.ResolverDestinoPorID(ctx, pgtype.UUID{Bytes: id, Valid: true})
	})
	return destinoODesconocido(fila, err)
}

// usuarioPorCelular resuelve el celular verificado de una persona activa que no esté en enfriamiento.
func (r *ResolutorDestino) usuarioPorCelular(ctx context.Context, celular string) (string, bool, error) {
	fila, err := enTransaccion(ctx, r.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (pgtype.UUID, error) {
		return q.ResolverDestinoPorCelular(ctx, repositorio.ResolverDestinoPorCelularParams{
			Celular:              celular,
			EnfriamientoSegundos: r.enfriamiento.Seconds(),
		})
	})
	return destinoODesconocido(fila, err)
}

// NombreDeDestinoPorCelular consulta la misma regla del envio y devuelve solo el nombre presentable.
// La consulta de nombre corre en la misma transaccion que ResolverDestinoPorCelular.
func (r *ResolutorDestino) NombreDeDestinoPorCelular(ctx context.Context, celular, usuarioID string) (string, bool, bool, error) {
	type resultado struct {
		nombre string
		existe bool
		propio bool
	}
	res, err := enTransaccion(ctx, r.pool, func(ctx context.Context, tx transaccionDeAuth, q *repositorio.Queries) (resultado, error) {
		id, err := q.ResolverDestinoPorCelular(ctx, repositorio.ResolverDestinoPorCelularParams{
			Celular: celular, EnfriamientoSegundos: r.enfriamiento.Seconds(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return resultado{}, nil
		}
		if err != nil {
			return resultado{}, err
		}
		if uuid.UUID(id.Bytes).String() == usuarioID {
			return resultado{existe: true, propio: true}, nil
		}
		var tipo string
		var nombre, razonSocial *string
		err = tx.QueryRow(ctx, `SELECT p.tipo, pe.nombre, em.razon_social
			FROM auth.partes p
			LEFT JOIN auth.personas pe ON pe.parte_id = p.id
			LEFT JOIN auth.empresas em ON em.parte_id = p.id
			WHERE p.id = $1`, id).Scan(&tipo, &nombre, &razonSocial)
		if err != nil {
			return resultado{}, err
		}
		if tipo == "empresa" && razonSocial != nil && strings.TrimSpace(*razonSocial) != "" {
			return resultado{nombre: *razonSocial, existe: true}, nil
		}
		if tipo == "persona" && nombre != nil {
			if visible := nombreEnmascarado(*nombre); visible != "" {
				return resultado{nombre: visible, existe: true}, nil
			}
		}
		return resultado{}, nil
	})
	return res.nombre, res.existe, res.propio, err
}

// nombreEnmascarado muestra el primer nombre y la inicial de un apellido, por runa.
//
// Supuesto pendiente de la respuesta S5 de Santiago: con 2 componentes toma el último, con 3 o más
// toma el penúltimo (el primer apellido en nombres como "Juan Carlos Rodríguez Pérez"). Con 1
// componente devuelve el nombre solo y con la entrada vacía devuelve cadena vacía.
func nombreEnmascarado(nombre string) string {
	partes := strings.Fields(nombre)
	if len(partes) == 0 {
		return ""
	}
	if len(partes) == 1 {
		return partes[0]
	}
	indiceApellido := len(partes) - 1
	if len(partes) >= 3 {
		indiceApellido--
	}
	return partes[0] + " " + string([]rune(partes[indiceApellido])[0]) + "."
}

// destinoODesconocido traduce el no encontrado a existe falso y formatea el uuid resuelto.
func destinoODesconocido(fila pgtype.UUID, err error) (string, bool, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return uuid.UUID(fila.Bytes).String(), true, nil
}
