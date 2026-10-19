//go:build integration

package copilot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func prepararTextoDB(t *testing.T, p ProveedorIA, manejadores map[string]ManejadorHerramienta) (*pgxpool.Pool, *pgxpool.Pool, context.Context, *ServicioTexto) {
	t.Helper()
	admin, app := poolPersistencia(t, "DATABASE_URL"), poolPersistencia(t, "APP_DATABASE_URL")
	parte := uuid.New()
	if _, err := admin.Exec(t.Context(), "INSERT INTO auth.partes(id,tipo) VALUES($1,'persona')", parte); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, tabla := range []string{"llamada_funcion", "solicitud_texto"} {
			_, _ = admin.Exec(ctx, "DELETE FROM asistente."+tabla+" WHERE usuario_id=$1", parte)
		}
		_, _ = admin.Exec(ctx, "DELETE FROM asistente.mensaje WHERE conversacion_id IN (SELECT id FROM asistente.conversacion WHERE usuario_id=$1)", parte)
		_, _ = admin.Exec(ctx, "DELETE FROM asistente.accion_pendiente WHERE usuario_id=$1", parte)
		_, _ = admin.Exec(ctx, "DELETE FROM asistente.conversacion WHERE usuario_id=$1", parte)
		_, _ = admin.Exec(ctx, "DELETE FROM auth.partes WHERE id=$1", parte)
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := NuevoCatalogo(manejadores, logger, ConAuditoriaLlamadas(NuevaAuditoriaPostgres(app)))
	if err != nil {
		t.Fatal(err)
	}
	o, err := NuevoOrquestador(p, c, NuevaMemoriaCicloPostgres(app), autorizacionCicloPrueba{}, "simulado", logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: parte.String(), UsuarioID: uuid.NewString()})
	return admin, app, ctx, NuevoServicioTexto(app, o)
}

func respuestaTextoDB() RespuestaIA {
	return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: "Respuesta verificada."}}}}
}

func TestTextoDBReintentosHistorialYAislamiento(t *testing.T) {
	llamadas := 0
	fallar := true
	historialUsuarios := 0
	p := proveedorCicloPrueba{generar: func(_ context.Context, s SolicitudIA) (RespuestaIA, error) {
		llamadas++
		historialUsuarios = 0
		for _, turno := range s.Historial {
			if turno.Rol == "usuario" {
				historialUsuarios++
			}
		}
		if fallar {
			return RespuestaIA{}, ErrNoDisponibleIA
		}
		return respuestaTextoDB(), nil
	}}
	admin, _, ctx, s := prepararTextoDB(t, p, nil)
	clave := uuid.New()
	r, err := s.EnviarTexto(ctx, uuid.Nil, "Hola", clave)
	if !errors.Is(err, ErrNoDisponibleIA) || r.ConversacionID == uuid.Nil || r.MensajeUsuarioID == uuid.Nil {
		t.Fatalf("entrada no conservada: %+v %v", r, err)
	}
	conv, user := r.ConversacionID, r.MensajeUsuarioID
	var usuarios, respuestas int
	if err := admin.QueryRow(ctx, "SELECT count(*) FILTER(WHERE rol='usuario'),count(*) FILTER(WHERE respuesta_a IS NOT NULL) FROM asistente.mensaje WHERE conversacion_id=$1", conv).Scan(&usuarios, &respuestas); err != nil || usuarios != 1 || respuestas != 0 {
		t.Fatalf("fallo: %d %d %v", usuarios, respuestas, err)
	}
	fallar = false
	r, err = s.EnviarTexto(ctx, uuid.Nil, "Hola", clave)
	if err != nil || r.ConversacionID != conv || r.MensajeUsuarioID != user || r.MensajeID == uuid.Nil {
		t.Fatalf("reintento: %+v %v", r, err)
	}
	final := r.MensajeID
	r, err = s.EnviarTexto(ctx, uuid.Nil, "Hola", clave)
	if err != nil || r.MensajeID != final || llamadas != 2 {
		t.Fatalf("replay invocó IA: %+v %v llamadas=%d", r, err, llamadas)
	}
	if _, err = s.EnviarTexto(ctx, uuid.Nil, "Otro", clave); !errors.Is(err, ErrClaveTextoEnConflicto) {
		t.Fatal(err)
	}
	for _, ajena := range []uuid.UUID{uuid.New(), conv} {
		otro, _ := identidad.DeContexto(ctx)
		if ajena == conv {
			otro.ParteID = uuid.NewString()
		}
		_, err = s.EnviarTexto(identidad.ConIdentidad(ctx, otro), ajena, "No autorizado", uuid.Nil)
		if !errors.Is(err, ErrConversacionTextoNoEncontrada) {
			t.Fatal(err)
		}
	}
	cambiado, _ := identidad.DeContexto(ctx)
	cambiado.UsuarioID = uuid.NewString()
	if _, err = s.EnviarTexto(identidad.ConIdentidad(ctx, cambiado), conv, "Otro contexto", uuid.Nil); !errors.Is(err, ErrConversacionTextoNoEncontrada) {
		t.Fatal(err)
	}
	if llamadas != 2 {
		t.Fatal("consulta ajena llegó al proveedor")
	}
	for i := range 22 {
		r, err = s.EnviarTexto(ctx, conv, fmt.Sprintf("Mensaje %d", i), uuid.Nil)
		if err != nil || r.MensajeID == final {
			t.Fatalf("continuación: %v", err)
		}
	}
	if historialUsuarios != 20 {
		t.Fatalf("historial=%d; esperado 20", historialUsuarios)
	}
	fallar = true
	r, err = s.EnviarTexto(ctx, conv, "Reintento conocido", uuid.Nil)
	if err == nil {
		t.Fatal("faltó fallo")
	}
	user = r.MensajeUsuarioID
	fallar = false
	r, err = s.EnviarTexto(ctx, conv, "Reintento conocido", uuid.Nil)
	if err != nil || r.MensajeUsuarioID != user {
		t.Fatalf("duplicó pendiente: %+v %v", r, err)
	}
	r, err = s.EnviarTexto(ctx, conv, "Reintento conocido", uuid.Nil)
	if err != nil || r.MensajeUsuarioID == user {
		t.Fatalf("un nuevo envío resuelto debe crear turno: %+v %v", r, err)
	}
}

func TestTextoDBReservaConcurrenteYTimeout(t *testing.T) {
	entrado, continuar := make(chan struct{}), make(chan struct{})
	p := proveedorCicloPrueba{generar: func(ctx context.Context, _ SolicitudIA) (RespuestaIA, error) {
		close(entrado)
		select {
		case <-continuar:
			return respuestaTextoDB(), nil
		case <-ctx.Done():
			return RespuestaIA{}, ctx.Err()
		}
	}}
	admin, _, ctx, s := prepararTextoDB(t, p, nil)
	clave := uuid.New()
	done := make(chan error, 1)
	go func() { _, err := s.EnviarTexto(ctx, uuid.Nil, "Una vez", clave); done <- err }()
	select {
	case <-entrado:
	case <-time.After(5 * time.Second):
		t.Fatal("no inició IA")
	}
	if _, err := s.EnviarTexto(ctx, uuid.Nil, "Una vez", clave); !errors.Is(err, ErrTextoEnProceso) {
		t.Fatalf("no excluyó: %v", err)
	}
	close(continuar)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var conv uuid.UUID
	if err := admin.QueryRow(ctx, "SELECT conversacion_id FROM asistente.solicitud_texto WHERE clave=$1", clave).Scan(&conv); err != nil {
		t.Fatal(err)
	}
	s.procesador = &procesadorTextoCancelado{}
	breve, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	r, err := s.EnviarTexto(breve, conv, "Conservar al agotar plazo", uuid.Nil)
	if !errors.Is(err, context.DeadlineExceeded) || r.MensajeUsuarioID == uuid.Nil {
		t.Fatalf("timeout: %+v %v", r, err)
	}
	var libre bool
	if err := admin.QueryRow(ctx, "SELECT ciclo_token IS NULL FROM asistente.conversacion WHERE id=$1", conv).Scan(&libre); err != nil || !libre {
		t.Fatalf("reserva no liberada: %v %v", libre, err)
	}
}

type procesadorTextoCancelado struct{}

func (*procesadorTextoCancelado) Responder(ctx context.Context, _ EntradaOrquestador) (SalidaOrquestador, error) {
	<-ctx.Done()
	return SalidaOrquestador{}, ctx.Err()
}

func TestTextoDBTarjetaPersistidaYReintentoDeHerramientas(t *testing.T) {
	var app *pgxpool.Pool
	preparaciones, vueltas := 0, 0
	p := proveedorCicloPrueba{generar: func(_ context.Context, s SolicitudIA) (RespuestaIA, error) {
		vueltas++
		if vueltas == 1 {
			return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Llamada: &LlamadaIA{ID: "prep", Nombre: "preparar_transferencia", Argumentos: []byte(`{"celular":"+573001234567","monto_pesos":"150000"}`)}}}}}, nil
		}
		if vueltas == 2 {
			return RespuestaIA{}, ErrNoDisponibleIA
		}
		if s.Historial[len(s.Historial)-1].Rol != "herramienta" {
			t.Error("se perdió el resultado al reintentar")
		}
		return respuestaTextoDB(), nil
	}}
	h := func(ctx context.Context, id identidad.Identidad, _ ArgumentosHerramienta) (DatosHerramienta, error) {
		preparaciones++
		conv, _ := ConversacionDelContexto(ctx)
		parte, _ := uuid.Parse(id.ParteID)
		a, err := EnTransaccionAsistente(ctx, app, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
			return q.CrearAccion(ctx, repositorio.CrearAccionParams{ConversacionID: uuidCiclo(conv), UsuarioID: uuidCiclo(parte), UsuarioFinancieroID: pgtype.Text{String: id.UsuarioID, Valid: true}, Tipo: "transferencia_interna", Destino: "+573001234567", NombreMostrado: "Laura R.", CelularEnmascarado: pgtype.Text{String: "***4567", Valid: true}, Monto: 15000000, RequiereCodigo: true})
		})
		if err != nil {
			return nil, err
		}
		return AccionHerramienta{AccionID: uuid.UUID(a.ID.Bytes), Monto: 1, Destinatario: DestinatarioHerramienta{Nombre: "Inventado"}, ExpiraEn: a.ExpiraEn.Time}, nil
	}
	admin, pool, ctx, s := prepararTextoDB(t, p, map[string]ManejadorHerramienta{"preparar_transferencia": h})
	app = pool
	clave := uuid.New()
	r, err := s.EnviarTexto(ctx, uuid.Nil, "Preparar transferencia", clave)
	if !errors.Is(err, ErrNoDisponibleIA) {
		t.Fatal(err)
	}
	conv := r.ConversacionID
	r, err = s.EnviarTexto(ctx, uuid.Nil, "Preparar transferencia", clave)
	if err != nil || preparaciones != 1 || r.Accion == nil || r.Accion.Monto != 15000000 || r.Accion.Nombre != "Laura R." || !r.Accion.RequiereCodigo {
		t.Fatalf("tarjeta: %+v prep=%d %v", r, preparaciones, err)
	}
	var total, audit int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM asistente.accion_pendiente WHERE conversacion_id=$1", conv).Scan(&total); err != nil || total != 1 {
		t.Fatalf("acciones=%d %v", total, err)
	}
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM asistente.llamada_funcion WHERE conversacion_id=$1", conv).Scan(&audit); err != nil || audit != 2 {
		t.Fatalf("auditoría=%d %v", audit, err)
	}
	if _, err = s.EnviarTexto(ctx, uuid.Nil, "Preparar transferencia", clave); err != nil || vueltas != 3 {
		t.Fatalf("replay: %d %v", vueltas, err)
	}
}
