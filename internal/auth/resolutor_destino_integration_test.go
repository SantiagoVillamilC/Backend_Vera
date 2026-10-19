//go:build integration

package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// enfriamientoDePrueba es la ventana con la que el resolutor decide si un celular está bloqueado.
const enfriamientoDePrueba = 24 * time.Hour

// moldeDeDestino describe la parte que la siembra del resolutor debe crear.
type moldeDeDestino struct {
	tipo          string
	conCredencial bool
	estado        string
	verificado    bool
	cambiado      bool
	edadCambio    time.Duration
	conEmpresa    bool
}

// sembrarDestinoDePrueba siembra y confirma una parte con la credencial y la empresa pedidas.
func sembrarDestinoDePrueba(t *testing.T, pool *pgxpool.Pool, molde moldeDeDestino) (string, string) {
	t.Helper()

	parteID := uuid.New()
	celular := "+57" + strings.ReplaceAll(parteID.String(), "-", "")
	correo := parteID.String() + "@centinela.test"

	_, err := enTransaccion(context.Background(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: pgUUID(parteID), Tipo: molde.tipo}); err != nil {
				return struct{}{}, err
			}
			if molde.conEmpresa {
				if _, err := q.CrearEmpresa(ctx, repositorio.CrearEmpresaParams{
					ParteID:     pgUUID(parteID),
					Nit:         strings.ReplaceAll(parteID.String(), "-", ""),
					RazonSocial: "Empresa " + parteID.String(),
				}); err != nil {
					return struct{}{}, err
				}
			}
			if !molde.conCredencial {
				return struct{}{}, nil
			}
			if _, err := q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
				ID:            pgUUID(uuid.New()),
				ParteID:       pgUUID(parteID),
				Correo:        correo,
				Celular:       celular,
				ValorDerivado: valorDerivadoDeEstado(molde.estado),
				Estado:        molde.estado,
			}); err != nil {
				return struct{}{}, err
			}
			if molde.verificado {
				if _, err := tx.Exec(ctx, "UPDATE auth.credenciales SET celular_verificado_en = now() WHERE parte_id = $1", pgUUID(parteID)); err != nil {
					return struct{}{}, err
				}
			}
			if molde.cambiado {
				if _, err := tx.Exec(ctx, "UPDATE auth.credenciales SET celular_cambiado_en = $2 WHERE parte_id = $1", pgUUID(parteID), time.Now().Add(-molde.edadCambio)); err != nil {
					return struct{}{}, err
				}
			}
			return struct{}{}, nil
		})
	if err != nil {
		t.Fatalf("sembrar destino: %v", err)
	}
	return parteID.String(), celular
}

// valorDerivadoDeEstado devuelve el hash ficticio que satisface el check de la credencial.
func valorDerivadoDeEstado(estado string) pgtype.Text {
	if estado == "sin_login" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: "$argon2id$hash", Valid: true}
}

func TestResolutorDestinoPorUUID(t *testing.T) {
	casos := map[string]struct {
		molde    moldeDeDestino
		resuelve bool
	}{
		"persona activa":             {molde: moldeDeDestino{tipo: "persona", conCredencial: true, estado: "activo"}, resuelve: true},
		"persona pendiente":          {molde: moldeDeDestino{tipo: "persona", conCredencial: true, estado: "pendiente_verificacion"}, resuelve: true},
		"persona sin_login":          {molde: moldeDeDestino{tipo: "persona", conCredencial: true, estado: "sin_login"}, resuelve: true},
		"persona credencial cerrada": {molde: moldeDeDestino{tipo: "persona", conCredencial: true, estado: "cerrado"}, resuelve: false},
		"persona sin credencial":     {molde: moldeDeDestino{tipo: "persona"}, resuelve: false},
		"empresa":                    {molde: moldeDeDestino{tipo: "empresa", conEmpresa: true}, resuelve: true},
	}

	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			pool := abrirPoolDeAuth(t)
			resolutor := NuevoResolutorDestino(pool, enfriamientoDePrueba)
			id, _ := sembrarDestinoDePrueba(t, pool, caso.molde)

			got, existe, err := resolutor.UsuarioIDDeDestino(t.Context(), id)
			if err != nil {
				t.Fatalf("UsuarioIDDeDestino(%q): %v", id, err)
			}
			if existe != caso.resuelve {
				t.Fatalf("existe = %v, quiero %v", existe, caso.resuelve)
			}
			if caso.resuelve && got != id {
				t.Errorf("destino = %q, quiero el uuid de la parte %q", got, id)
			}
		})
	}
}

func TestResolutorDestinoPorCelular(t *testing.T) {
	casos := map[string]struct {
		molde    moldeDeDestino
		resuelve bool
	}{
		"verificado activo": {
			molde:    moldeDeDestino{tipo: "persona", conCredencial: true, estado: "activo", verificado: true},
			resuelve: true,
		},
		"sin verificar": {
			molde:    moldeDeDestino{tipo: "persona", conCredencial: true, estado: "activo"},
			resuelve: false,
		},
		"verificado pero pendiente": {
			molde:    moldeDeDestino{tipo: "persona", conCredencial: true, estado: "pendiente_verificacion", verificado: true},
			resuelve: false,
		},
		"verificado y en enfriamiento": {
			molde:    moldeDeDestino{tipo: "persona", conCredencial: true, estado: "activo", verificado: true, cambiado: true, edadCambio: time.Hour},
			resuelve: false,
		},
		"verificado fuera de enfriamiento": {
			molde:    moldeDeDestino{tipo: "persona", conCredencial: true, estado: "activo", verificado: true, cambiado: true, edadCambio: 25 * time.Hour},
			resuelve: true,
		},
	}

	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			pool := abrirPoolDeAuth(t)
			resolutor := NuevoResolutorDestino(pool, enfriamientoDePrueba)
			id, celular := sembrarDestinoDePrueba(t, pool, caso.molde)

			got, existe, err := resolutor.UsuarioIDDeDestino(t.Context(), celular)
			if err != nil {
				t.Fatalf("UsuarioIDDeDestino(%q): %v", celular, err)
			}
			if existe != caso.resuelve {
				t.Fatalf("existe = %v, quiero %v", existe, caso.resuelve)
			}
			if caso.resuelve && got != id {
				t.Errorf("destino = %q, quiero el uuid de la parte %q", got, id)
			}
		})
	}
}

func TestResolutorDestinoNoResuelveCorreo(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	resolutor := NuevoResolutorDestino(pool, enfriamientoDePrueba)
	id, _ := sembrarDestinoDePrueba(t, pool, moldeDeDestino{tipo: "persona", conCredencial: true, estado: "activo"})

	got, existe, err := resolutor.UsuarioIDDeDestino(t.Context(), id+"@centinela.test")
	if err != nil {
		t.Fatalf("UsuarioIDDeDestino(correo): %v", err)
	}
	if existe {
		t.Fatalf("el correo resolvió a %q, quiero que no resuelva destino (H3)", got)
	}
}

func TestResolutorDestinoDelUsuarioDePrueba(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	SembrarParteDePrueba(t, pool)
	resolutor := NuevoResolutorDestino(pool, enfriamientoDePrueba)

	got, existe, err := resolutor.UsuarioIDDeDestino(t.Context(), UsuarioDePruebaID)
	if err != nil {
		t.Fatalf("UsuarioIDDeDestino(%q): %v", UsuarioDePruebaID, err)
	}
	if !existe {
		t.Fatalf("la fila sin_login del usuario de prueba no resolvió por uuid")
	}
	if got != UsuarioDePruebaID {
		t.Errorf("destino = %q, quiero %q", got, UsuarioDePruebaID)
	}
}
