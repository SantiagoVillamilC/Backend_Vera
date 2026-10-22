package copilot_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/provsim"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

type memoriaSimulada struct{ registros []copilot.RegistroCiclo }

func (m *memoriaSimulada) Abrir(context.Context, copilot.EntradaOrquestador) (copilot.SesionCiclo, error) {
	return m, nil
}
func (m *memoriaSimulada) Historial() []copilot.TurnoIA {
	return []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Texto: "Consulta mis límites"}}}}
}
func (m *memoriaSimulada) Guardar(_ context.Context, r copilot.RegistroCiclo) error {
	m.registros = append(m.registros, r)
	return nil
}
func (m *memoriaSimulada) Accion(context.Context, uuid.UUID) (*copilot.AccionOrquestador, error) {
	return nil, nil
}
func (m *memoriaSimulada) Cerrar(context.Context) error { return nil }

type autorizacionSimulada struct{}

func (autorizacionSimulada) Exigir(context.Context) error { return nil }

func TestOrquestadorConProveedorSimulado(t *testing.T) {
	p := provsim.Nuevo(
		provsim.Paso{Respuesta: copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Llamada: &copilot.LlamadaIA{ID: "limites", Nombre: "consultar_limites", Argumentos: []byte(`{}`)}}}}}},
		provsim.Paso{Respuesta: copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: "La consulta de límites aún no está disponible."}}}}},
	)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := copilot.NuevoCatalogo(nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	memoria := &memoriaSimulada{}
	o, err := copilot.NuevoOrquestador(p, c, memoria, autorizacionSimulada{}, "simulado", logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()})
	r, err := o.Responder(ctx, copilot.EntradaOrquestador{ConversacionID: uuid.New(), MensajeID: uuid.New()})
	if err != nil || r.Vueltas != 2 || r.Llamadas != 1 || len(memoria.registros) != 2 || !memoria.registros[1].Final {
		t.Fatalf("simulado: %+v %v", r, err)
	}
}
