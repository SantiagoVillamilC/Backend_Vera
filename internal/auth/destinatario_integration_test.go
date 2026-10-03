//go:build integration

package auth

import (
	"context"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// HU-NF-06 C15.1: consulta Postgres real y presenta persona y empresa sin datos adicionales.
func TestHU_NF_06_C15_1_NombreRealDePersonaYEmpresa(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	resolutor := NuevoResolutorDestino(pool, 24*time.Hour)
	personaID, celularPersona := sembrarDestinoDePrueba(t, pool, moldeDeDestino{tipo: "persona", conCredencial: true, estado: "activo", verificado: true})
	id, _ := uuid.Parse(personaID)
	_, err := enTransaccion(t.Context(), poolDeAuth{pool: pool}, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		_, err := q.CrearPersona(ctx, repositorio.CrearPersonaParams{
			ParteID: pgtype.UUID{Bytes: id, Valid: true}, TipoDocumento: "CC", Documento: uuid.NewString(), Nombre: "Laura Ramírez", PerfilNombre: pgtype.Text{},
		})
		return struct{}{}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	nombre, existe, propio, err := resolutor.NombreDeDestinoPorCelular(t.Context(), celularPersona, uuid.NewString())
	if err != nil || !existe || propio || nombre != "Laura R." {
		t.Fatalf("persona: %q %v %v %v", nombre, existe, propio, err)
	}

	_, celularEmpresa := sembrarDestinoDePrueba(t, pool, moldeDeDestino{tipo: "empresa", conEmpresa: true, conCredencial: true, estado: "activo", verificado: true})
	nombre, existe, propio, err = resolutor.NombreDeDestinoPorCelular(t.Context(), celularEmpresa, uuid.NewString())
	if err != nil || !existe || propio || len(nombre) < len("Empresa ") || nombre[:len("Empresa ")] != "Empresa " {
		t.Fatalf("empresa: %q %v %v %v", nombre, existe, propio, err)
	}
}

// HU-NF-06 C15.2 y C15.3: el resolutor real aplica inexistencia, destino propio y enfriamiento.
func TestHU_NF_06_C15_2_C15_3_ResolucionReal(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	resolutor := NuevoResolutorDestino(pool, 24*time.Hour)
	id, celular := sembrarDestinoDePrueba(t, pool, moldeDeDestino{tipo: "persona", conCredencial: true, estado: "activo", verificado: true})
	_, existe, propio, err := resolutor.NombreDeDestinoPorCelular(t.Context(), celular, id)
	if err != nil || !existe || !propio {
		t.Fatalf("propio: %v %v %v", existe, propio, err)
	}
	_, existe, propio, err = resolutor.NombreDeDestinoPorCelular(t.Context(), uuid.NewString(), uuid.NewString())
	if err != nil || existe || propio {
		t.Fatalf("inexistente: %v %v %v", existe, propio, err)
	}
	_, celularEnfriado := sembrarDestinoDePrueba(t, pool, moldeDeDestino{tipo: "persona", conCredencial: true, estado: "activo", verificado: true, cambiado: true, edadCambio: time.Hour})
	_, existe, propio, err = resolutor.NombreDeDestinoPorCelular(t.Context(), celularEnfriado, uuid.NewString())
	if err != nil || existe || propio {
		t.Fatalf("enfriamiento: %v %v %v", existe, propio, err)
	}
}
