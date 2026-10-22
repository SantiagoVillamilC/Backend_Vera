//go:build integration

package copilot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type entornoHistorial struct {
	admin, app *pgxpool.Pool
	ctx        context.Context
	parte      uuid.UUID
	financiero string
	h          *ServicioHistorial
}

func nuevoEntornoHistorial(t *testing.T, dias int) *entornoHistorial {
	t.Helper()
	e := &entornoHistorial{admin: poolPersistencia(t, "DATABASE_URL"), app: poolPersistencia(t, "APP_DATABASE_URL"), parte: uuid.New(), financiero: uuid.NewString()}
	if _, err := e.admin.Exec(t.Context(), "INSERT INTO auth.partes(id,tipo) VALUES($1,'persona')", e.parte); err != nil {
		t.Fatal(err)
	}
	e.ctx = identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: e.parte.String(), UsuarioID: e.financiero})
	var err error
	e.h, err = NuevoServicioHistorial(e.app, dias)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, sql := range []string{"DELETE FROM asistente.llamada_funcion WHERE usuario_id=$1", "DELETE FROM asistente.conversacion WHERE usuario_id=$1", "DELETE FROM auth.partes WHERE id=$1"} {
			if _, err := e.admin.Exec(ctx, sql, e.parte); err != nil {
				t.Error(err)
			}
		}
	})
	return e
}

func (e *entornoHistorial) crear(t *testing.T, financiero string) uuid.UUID {
	t.Helper()
	c, err := EnTransaccionAsistente(e.ctx, e.app, func(q *repositorio.Queries) (repositorio.Conversacion, error) {
		return q.CrearConversacion(e.ctx, repositorio.CrearConversacionParams{UsuarioID: uuidCiclo(e.parte), UsuarioFinancieroID: pgtype.Text{String: financiero, Valid: true}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return uuid.UUID(c.ID.Bytes)
}

func (e *entornoHistorial) mensaje(t *testing.T, conv uuid.UUID, rol, canal, texto, meta string, respuesta, accion uuid.UUID) repositorio.Mensaje {
	t.Helper()
	m, err := EnTransaccionAsistente(e.ctx, e.app, func(q *repositorio.Queries) (repositorio.Mensaje, error) {
		p := repositorio.AnexarMensajeParams{ConversacionID: uuidCiclo(conv), UsuarioID: uuidCiclo(e.parte), Rol: rol, Canal: canal, Contenido: texto, Metadatos: []byte(meta)}
		if respuesta != uuid.Nil {
			p.RespuestaA = uuidCiclo(respuesta)
		}
		if accion != uuid.Nil {
			p.AccionID = uuidCiclo(accion)
		}
		return q.AnexarMensaje(e.ctx, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestHistorialPaginacionAislamientoYAnclaBorrada(t *testing.T) {
	e := nuevoEntornoHistorial(t, 30)
	instante := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	for range 5 {
		conv := e.crear(t, e.financiero)
		if _, err := e.admin.Exec(e.ctx, "UPDATE asistente.conversacion SET ultimo_mensaje_en=$2 WHERE id=$1", conv, instante); err != nil {
			t.Fatal(err)
		}
	}
	ajena := e.crear(t, "otro contexto")
	p, err := e.h.ListarConversaciones(e.ctx, ConsultaHistorial{Limite: 2})
	if err != nil || len(p.Elementos) != 2 || p.Siguiente == nil {
		t.Fatalf("pagina=%+v error=%v", p, err)
	}
	if p.Elementos[0].ID.String() < p.Elementos[1].ID.String() {
		t.Fatal("desempate UUID incorrecto")
	}
	vistos := map[uuid.UUID]bool{}
	for _, c := range p.Elementos {
		vistos[c.ID] = true
	}
	if err := e.h.BorrarConversacion(e.ctx, p.Elementos[1].ID); err != nil {
		t.Fatal(err)
	}
	for p.Siguiente != nil {
		p, err = e.h.ListarConversaciones(e.ctx, ConsultaHistorial{Limite: 2, Cursor: p.Siguiente})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range p.Elementos {
			if vistos[c.ID] || c.ID == ajena {
				t.Fatal("duplicado o contexto ajeno")
			}
			vistos[c.ID] = true
		}
	}
	if len(vistos) != 5 {
		t.Fatalf("total=%d", len(vistos))
	}
	for _, ctx := range []context.Context{
		identidad.ConIdentidad(e.ctx, identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: e.financiero}),
		identidad.ConIdentidad(e.ctx, identidad.Identidad{ParteID: e.parte.String(), UsuarioID: "otro contexto"}),
	} {
		for id := range vistos {
			if _, err := e.h.ListarMensajes(ctx, id, ConsultaHistorial{Limite: 2}); !errors.Is(err, ErrConversacionTextoNoEncontrada) {
				t.Fatalf("lectura ajena: %v", err)
			}
			if err := e.h.BorrarConversacion(ctx, id); !errors.Is(err, ErrConversacionTextoNoEncontrada) {
				t.Fatalf("borrado ajeno: %v", err)
			}
		}
	}
	if err := e.h.BorrarConversaciones(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.h.BorrarConversaciones(e.ctx); err != nil {
		t.Fatal(err)
	}
	p, err = e.h.ListarConversaciones(e.ctx, ConsultaHistorial{Limite: 50})
	if err != nil || p.Elementos == nil || len(p.Elementos) != 0 {
		t.Fatalf("vacia=%+v %v", p, err)
	}
	var n int
	if err := e.admin.QueryRow(e.ctx, "SELECT count(*) FROM asistente.conversacion WHERE id=$1", ajena).Scan(&n); err != nil || n != 1 {
		t.Fatalf("otro contexto borrado: %d %v", n, err)
	}
}

func TestHistorialMensajesPublicosTarjetaActualYCursores(t *testing.T) {
	e := nuevoEntornoHistorial(t, 30)
	conv := e.crear(t, e.financiero)
	u := e.mensaje(t, conv, "usuario", "voz", "transcripción", "{}", uuid.Nil, uuid.Nil)
	e.mensaje(t, conv, "asistente", "voz", "llamada privada", `{"interno":true}`, uuid.Nil, uuid.Nil)
	e.mensaje(t, conv, "herramienta", "voz", "resultado privado", "{}", uuid.Nil, uuid.Nil)
	a, err := EnTransaccionAsistente(e.ctx, e.app, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
		return q.CrearAccion(e.ctx, repositorio.CrearAccionParams{ConversacionID: uuidCiclo(conv), UsuarioID: uuidCiclo(e.parte), UsuarioFinancieroID: pgtype.Text{String: e.financiero, Valid: true}, Tipo: "transferencia_llave", TipoLlave: pgtype.Text{String: "celular", Valid: true}, Destino: "3001234567", NombreMostrado: "L*** R***", Monto: 9007199254740993})
	})
	if err != nil {
		t.Fatal(err)
	}
	e.mensaje(t, conv, "asistente", "voz", "propuesta", "{}", uuid.UUID(u.ID.Bytes), uuid.UUID(a.ID.Bytes))
	e.mensaje(t, conv, "asistente", "texto", "notificación de estado", `{"evento_accion":true}`, uuid.Nil, uuid.Nil)
	p, err := e.h.ListarMensajes(e.ctx, conv, ConsultaHistorial{Limite: 1})
	if err != nil || len(p.Elementos) != 1 || p.Elementos[0].Texto != "notificación de estado" || p.Siguiente == nil {
		t.Fatalf("pagina=%+v %v", p, err)
	}
	cursor := p.Siguiente
	p, err = e.h.ListarMensajes(e.ctx, conv, ConsultaHistorial{Limite: 1, Cursor: cursor})
	if err != nil || p.Elementos[0].Accion == nil || p.Elementos[0].Accion.Estado != "pendiente" || p.Elementos[0].Accion.Llave != "***" || p.Elementos[0].Accion.Monto.Int64() != 9007199254740993 {
		t.Fatalf("tarjeta=%+v %v", p, err)
	}
	if _, err := e.admin.Exec(e.ctx, "UPDATE asistente.accion_pendiente SET estado='cancelada',resuelta_en=clock_timestamp() WHERE id=$1", a.ID); err != nil {
		t.Fatal(err)
	}
	actual, err := e.h.ListarMensajes(e.ctx, conv, ConsultaHistorial{Limite: 1, Cursor: cursor})
	if err != nil || actual.Elementos[0].Accion.Estado != "cancelada" {
		t.Fatalf("tarjeta desactualizada: %+v %v", actual, err)
	}
	ultima, err := e.h.ListarMensajes(e.ctx, conv, ConsultaHistorial{Limite: 1, Cursor: p.Siguiente})
	if err != nil || len(ultima.Elementos) != 1 || ultima.Elementos[0].Texto != "transcripción" || ultima.Elementos[0].Canal != "voz" || ultima.Siguiente != nil {
		t.Fatalf("filtrado=%+v %v", ultima, err)
	}
	otra := e.crear(t, e.financiero)
	if _, err := e.h.ListarMensajes(e.ctx, otra, ConsultaHistorial{Limite: 1, Cursor: cursor}); !errors.Is(err, ErrPaginaHistorial) {
		t.Fatalf("cursor de otra conversación: %v", err)
	}
	vacia, err := e.h.ListarMensajes(e.ctx, otra, ConsultaHistorial{Limite: 50})
	if err != nil || vacia.Elementos == nil || len(vacia.Elementos) != 0 {
		t.Fatalf("mensajes vacíos=%+v %v", vacia, err)
	}
}

func TestHistorialBorradoCancelaPreservaAuditoriaTransferenciaYNoRecuerda(t *testing.T) {
	e := nuevoEntornoConfirmacion(t)
	if _, err := e.admin.Exec(e.ctx, "UPDATE asistente.conversacion SET usuario_financiero_id=$2 WHERE id=$1", e.conv, e.usuario); err != nil {
		t.Fatal(err)
	}
	h, err := NuevoServicioHistorial(e.app, 30)
	if err != nil {
		t.Fatal(err)
	}
	confirmada := e.crear(t, 10000, false, "transferencia_interna")
	resultado, err := e.control.Confirmar(e.ctx, confirmada, ConfirmacionAccion{})
	if err != nil {
		t.Fatal(err)
	}
	pendiente := e.crear(t, 10000, true, "transferencia_interna")
	if _, err := e.control.SolicitarCodigo(e.ctx, pendiente, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	parte := uuid.MustParse(e.parte)
	msg, err := EnTransaccionAsistente(e.ctx, e.app, func(q *repositorio.Queries) (repositorio.Mensaje, error) {
		m, err := q.AnexarMensaje(e.ctx, repositorio.AnexarMensajeParams{ConversacionID: uuidCiclo(e.conv), UsuarioID: uuidCiclo(parte), Rol: "usuario", Canal: "texto", Contenido: "secreto que debe borrarse", Metadatos: []byte(`{}`)})
		if err != nil {
			return m, err
		}
		_, err = q.AnexarLlamadaFuncion(e.ctx, repositorio.AnexarLlamadaFuncionParams{LlamadaID: uuidCiclo(uuid.New()), Fase: "inicio", UsuarioID: uuidCiclo(parte), ConversacionID: uuidCiclo(e.conv), MensajeID: m.ID, Funcion: "consultar_saldo", ArgumentosSaneados: []byte(`{}`), Resultado: "InProgress", VersionInstrucciones: VersionInstruccionesVera})
		return m, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.admin.Exec(e.ctx, "INSERT INTO asistente.solicitud_texto(usuario_id,clave,conversacion_id,mensaje_id,huella) VALUES($1,$2,$3,$4,$5)", parte, uuid.New(), e.conv, msg.ID, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	// Este guardia demuestra que la transición a cancelada sucede ANTES del borrado físico.
	nombre := "test_cancelacion_" + fmt.Sprint(uuid.New().ID())
	sql := "CREATE FUNCTION asistente." + nombre + "() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.id='" + pendiente.String() + "'::uuid AND OLD.estado <> 'cancelada' THEN RAISE EXCEPTION 'no cancelada'; END IF; RETURN OLD; END $$; CREATE TRIGGER " + nombre + " BEFORE DELETE ON asistente.accion_pendiente FOR EACH ROW EXECUTE FUNCTION asistente." + nombre + "()"
	if _, err := e.admin.Exec(e.ctx, sql); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := e.admin.Exec(context.Background(), "DROP TRIGGER "+nombre+" ON asistente.accion_pendiente; DROP FUNCTION asistente."+nombre+"()"); err != nil {
			t.Error(err)
		}
	})
	huella := e.huellaDinero(t)
	if err := h.BorrarConversacion(e.ctx, e.conv); err != nil {
		t.Fatal(err)
	}
	if e.huellaDinero(t) != huella {
		t.Fatal("borró dinero")
	}
	for _, tabla := range []string{"mensaje", "accion_pendiente", "solicitud_texto"} {
		var n int
		if err := e.admin.QueryRow(e.ctx, "SELECT count(*) FROM asistente."+tabla+" WHERE conversacion_id=$1", e.conv).Scan(&n); err != nil || n != 0 {
			t.Fatalf("quedó %s: %d %v", tabla, n, err)
		}
	}
	var n int
	if err := e.admin.QueryRow(e.ctx, "SELECT count(*) FROM asistente.llamada_funcion WHERE conversacion_id=$1 AND mensaje_id=$2", e.conv, msg.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("auditoría perdida: %d %v", n, err)
	}
	if _, err := e.control.Confirmar(e.ctx, pendiente, ConfirmacionAccion{}); !errors.Is(err, ErrAccionNoEncontrada) {
		t.Fatalf("confirmó borrada: %v", err)
	}
	if _, err := NuevaMemoriaCicloPostgres(e.app).Abrir(e.ctx, EntradaOrquestador{ConversacionID: e.conv, MensajeID: uuid.UUID(msg.ID.Bytes)}); !errors.Is(err, ErrCicloNoDisponible) {
		t.Fatalf("recuerdo eliminado: %v", err)
	}
	transferencia, err := e.servicio.ConsultarTransferencia(e.ctx, e.usuario, resultado.Transferencia.ID)
	if err != nil || transferencia.ID != resultado.Transferencia.ID {
		t.Fatalf("transferencia perdida: %v", err)
	}
}

func TestHistorialRetencionConfigurableYSaltaCandados(t *testing.T) {
	e := nuevoEntornoHistorial(t, 7)
	antigua, bloqueada, reciente := e.crear(t, e.financiero), e.crear(t, e.financiero), e.crear(t, e.financiero)
	for _, id := range []uuid.UUID{antigua, bloqueada} {
		if _, err := e.admin.Exec(e.ctx, "UPDATE asistente.conversacion SET ultimo_mensaje_en=now()-interval '8 days' WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.admin.Exec(e.ctx, "UPDATE asistente.conversacion SET ultimo_mensaje_en=now()-interval '6 days' WHERE id=$1", reciente); err != nil {
		t.Fatal(err)
	}
	tx, err := e.admin.Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(e.ctx, "SELECT id FROM asistente.conversacion WHERE id=$1 FOR UPDATE", bloqueada); err != nil {
		t.Fatal(err)
	}
	if n, err := e.h.Retener(e.ctx); err != nil || n != 1 {
		t.Fatalf("retener=%d %v", n, err)
	}
	if err := tx.Rollback(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := (&RetenerHistorialWorker{historial: e.h}).Work(e.ctx, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{antigua, bloqueada} {
		if _, err := e.h.ListarMensajes(e.ctx, id, ConsultaHistorial{Limite: 1}); !errors.Is(err, ErrConversacionTextoNoEncontrada) {
			t.Fatalf("antigua retenida: %v", err)
		}
	}
	if _, err := e.h.ListarMensajes(e.ctx, reciente, ConsultaHistorial{Limite: 1}); err != nil {
		t.Fatal(err)
	}
	if n, err := e.h.Retener(e.ctx); err != nil || n != 0 {
		t.Fatalf("barrido no idempotente: %d %v", n, err)
	}
}

func TestHistorialBorradoConcurrenteConConfirmacion(t *testing.T) {
	e := nuevoEntornoConfirmacion(t)
	if _, err := e.admin.Exec(e.ctx, "UPDATE asistente.conversacion SET usuario_financiero_id=$2 WHERE id=$1", e.conv, e.usuario); err != nil {
		t.Fatal(err)
	}
	h, err := NuevoServicioHistorial(e.app, 30)
	if err != nil {
		t.Fatal(err)
	}
	id := e.crear(t, 10000, false, "transferencia_interna")
	inicio := make(chan struct{})
	errores := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Go(func() { <-inicio; _, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{}); errores <- err })
	wg.Go(func() { <-inicio; errores <- h.BorrarConversacion(e.ctx, e.conv) })
	close(inicio)
	wg.Wait()
	close(errores)
	for err := range errores {
		if err != nil && !errors.Is(err, ErrAccionNoEncontrada) {
			t.Fatalf("carrera borrado/confirmación: %v", err)
		}
	}
	if n := e.contar(t); n > 1 {
		t.Fatalf("transferencias duplicadas: %d", n)
	}
	if _, err := h.ListarMensajes(e.ctx, e.conv, ConsultaHistorial{Limite: 1}); !errors.Is(err, ErrConversacionTextoNoEncontrada) {
		t.Fatalf("conversación resucitada: %v", err)
	}
}
