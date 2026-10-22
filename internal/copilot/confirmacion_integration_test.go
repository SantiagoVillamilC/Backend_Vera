//go:build integration

package copilot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type canalConfirmacionPrueba struct {
	antes func(string)
	err   error
}

func (c *canalConfirmacionPrueba) CodigoFijo() (string, bool) { return "123456", true }
func (c *canalConfirmacionPrueba) Enviar(_ context.Context, celular, _ string) (string, error) {
	if c.antes != nil {
		c.antes(celular)
	}
	return "mensaje-prueba", c.err
}

type entornoConfirmacion struct {
	*entornoPreparacion
	control *ControlAcciones
	otp     *auth.GestorDesafios
	consent *auth.ServicioDeConsentimientos
	canal   *canalConfirmacionPrueba
}

func nuevoEntornoConfirmacion(t *testing.T) *entornoConfirmacion {
	t.Helper()
	base := nuevoEntornoPreparacion(t)
	e := &entornoConfirmacion{entornoPreparacion: base, canal: &canalConfirmacionPrueba{}}
	cred, familia := uuid.New(), uuid.New()
	cel := fmt.Sprintf("+57310%07d", int(uuid.New().ID()%8000000)+1000000)
	if _, err := e.admin.Exec(t.Context(), "INSERT INTO auth.credenciales(id,parte_id,correo,celular,celular_verificado_en,valor_derivado,estado) VALUES($1,$2,$3,$4,now(),'prueba','activo')", cred, e.parte, uuid.NewString()+"@test.local", cel); err != nil {
		t.Fatal(err)
	}
	if _, err := e.admin.Exec(t.Context(), "INSERT INTO auth.sesiones(id,familia_id,parte_id,contexto_activo,refresh_hash,vence_en) VALUES($1,$2,$3,'empresa',$4,now()+interval '1 day')", uuid.New(), familia, e.parte, []byte(uuid.NewString())); err != nil {
		t.Fatal(err)
	}
	e.ctx = identidad.ConIdentidad(e.ctx, identidad.Identidad{ParteID: e.parte, UsuarioID: e.usuario, SesionID: familia.String(), Contexto: auth.ContextoEmpresa})
	e.consent = auth.NuevoServicioDeConsentimientos(e.app)
	for _, tipo := range []string{auth.TipoConsentimientoAsistente, auth.TipoConsentimientoWhatsApp} {
		if _, err := e.consent.Registrar(e.ctx, auth.SolicitudDeConsentimientoNuevo{ParteID: uuid.MustParse(e.parte), Tipo: tipo, VersionTexto: "v1", Aceptado: true, Metodo: "app", Origen: "127.0.0.1"}); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	e.otp, err = auth.NuevoGestorDesafios(e.app, e.canal, nil, auth.NuevoLectorOptInDeConsentimientos(e.app), bytes.Repeat([]byte{9}, 32), auth.ConRacionamiento(e.contador, e.claves))
	if err != nil {
		t.Fatal(err)
	}
	e.control, err = NuevoControlAcciones(e.app, e.servicio, e.otp, NuevoControlConsentimiento(e.consent, NuevoCanceladorConsentimiento(e.app)), 50000000)
	if err != nil {
		t.Fatal(err)
	}
	e.abonar(t, 100000000)
	t.Cleanup(func() {
		ctx := context.Background()
		tx, err := e.admin.Begin(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err = tx.Exec(ctx, "SET LOCAL session_replication_role = replica"); err != nil {
			t.Error(err)
			return
		}
		for _, sql := range []string{"DELETE FROM asistente.llamada_funcion WHERE usuario_id=$1", "DELETE FROM asistente.solicitud_texto WHERE usuario_id=$1", "DELETE FROM asistente.mensaje WHERE conversacion_id IN (SELECT id FROM asistente.conversacion WHERE usuario_id=$1)", "DELETE FROM asistente.accion_pendiente WHERE usuario_id=$1", "DELETE FROM auth.desafios WHERE parte_id=$1", "DELETE FROM auth.sesiones WHERE parte_id=$1", "DELETE FROM auth.consentimientos WHERE parte_id=$1"} {
			if _, err = tx.Exec(ctx, sql, e.parte); err != nil {
				t.Error(err)
				return
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Error(err)
		}
	})
	return e
}
func (e *entornoConfirmacion) crear(t *testing.T, monto int64, codigo bool, tipo string) uuid.UUID {
	t.Helper()
	a, err := EnTransaccionAsistente(e.ctx, e.app, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
		p := repositorio.CrearAccionParams{ConversacionID: uuidCiclo(e.conv), UsuarioID: uuidCiclo(uuid.MustParse(e.parte)), UsuarioFinancieroID: pgtype.Text{String: e.usuario, Valid: true}, Tipo: tipo, Destino: e.celular, NombreMostrado: "Laura R.", Monto: monto, RequiereCodigo: codigo}
		if tipo == "transferencia_llave" {
			p.TipoLlave = pgtype.Text{String: "celular", Valid: true}
			p.Destino = e.celular[3:]
		}
		return q.CrearAccion(e.ctx, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	return uuid.UUID(a.ID.Bytes)
}
func (e *entornoConfirmacion) estado(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var estado string
	if err := e.admin.QueryRow(e.ctx, "SELECT estado FROM asistente.accion_pendiente WHERE id=$1", id).Scan(&estado); err != nil {
		t.Fatal(err)
	}
	return estado
}
func (e *entornoConfirmacion) contar(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.admin.QueryRow(e.ctx, "SELECT count(*) FROM nucleo.transferencia WHERE usuario_id=$1", e.usuario).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestConfirmacionDBConYSinCodigo(t *testing.T) {
	for _, conCodigo := range []bool{false, true} {
		t.Run(fmt.Sprint(conCodigo), func(t *testing.T) {
			e := nuevoEntornoConfirmacion(t)
			id := e.crear(t, 100000, conCodigo, "transferencia_interna")
			sol := ConfirmacionAccion{}
			if conCodigo {
				if _, err := e.control.Confirmar(e.ctx, id, sol); !errors.Is(err, ErrCodigoAccionRequerido) {
					t.Fatal(err)
				}
				e.canal.antes = func(cel string) {
					var vinculado bool
					err := e.admin.QueryRow(e.ctx, "SELECT desafio_id IS NOT NULL FROM asistente.accion_pendiente WHERE id=$1", id).Scan(&vinculado)
					if err != nil || !vinculado || cel == e.celular {
						t.Error("entrega antes del vínculo o al destinatario incorrecto", err)
					}
				}
				d, err := e.control.SolicitarCodigo(e.ctx, id, "127.0.0.1")
				if err != nil {
					t.Fatal(err)
				}
				sol = ConfirmacionAccion{DesafioID: d.ID, Codigo: "123456"}
				if _, err := e.control.SolicitarCodigo(e.ctx, id, "127.0.0.1"); !errors.Is(err, auth.ErrEnfriamientoReenvio) {
					t.Fatal(err)
				}
			}
			r, err := e.control.Confirmar(e.ctx, id, sol)
			if err != nil || r.Accion.Estado != "confirmada" || r.Transferencia == nil || r.Transferencia.Monto != 100000 || r.Transferencia.UsuarioID != e.usuario {
				t.Fatalf("%+v %v", r, err)
			}
			r2, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{})
			if err != nil || r2.Transferencia.ID != r.Transferencia.ID || e.contar(t) != 1 {
				t.Fatalf("replay: %+v %v", r2, err)
			}
			var clave string
			if err := e.admin.QueryRow(e.ctx, "SELECT idempotency_key FROM nucleo.transferencia WHERE id=$1", r.Transferencia.ID).Scan(&clave); err != nil || clave != id.String() {
				t.Fatalf("clave %q %v", clave, err)
			}
			var contexto string
			if err := e.admin.QueryRow(e.ctx, "SELECT contenido FROM asistente.mensaje WHERE conversacion_id=$1 ORDER BY numero_orden DESC LIMIT 1", e.conv).Scan(&contexto); err != nil || contexto == "" {
				t.Fatal("sin contexto de resolución", err)
			}
		})
	}
}

func TestConfirmacionDBConcurrenteLlavesYRechazos(t *testing.T) {
	t.Run("concurrente", func(t *testing.T) {
		e := nuevoEntornoConfirmacion(t)
		id := e.crear(t, 100000, false, "transferencia_interna")
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				_, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{})
				if err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if e.contar(t) != 1 {
			t.Fatal("duplicó transferencia")
		}
	})
	t.Run("llave", func(t *testing.T) {
		e := nuevoEntornoConfirmacion(t)
		id := e.crear(t, 100000, false, "transferencia_llave")
		r, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{})
		if err != nil || r.Transferencia == nil || r.Transferencia.Estado != "reservada" {
			t.Fatalf("%+v %v", r, err)
		}
		var outbox int
		if err := e.admin.QueryRow(e.ctx, "SELECT count(*) FROM nucleo.outbox WHERE payload->>'transferencia_id'=$1::text", r.Transferencia.ID).Scan(&outbox); err != nil || outbox != 1 {
			t.Fatalf("outbox=%d %v", outbox, err)
		}
	})
	t.Run("saldo cambia", func(t *testing.T) {
		e := nuevoEntornoConfirmacion(t)
		id := e.crear(t, 100000, false, "transferencia_interna")
		e.abonar(t, -100000000)
		_, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{})
		if !errors.Is(err, nucleo.ErrSaldoInsuficiente) || e.estado(t, id) != "fallida" || e.contar(t) != 0 {
			t.Fatalf("%s %v", e.estado(t, id), err)
		}
	})
	t.Run("tope", func(t *testing.T) {
		e := nuevoEntornoConfirmacion(t)
		id := e.crear(t, 2000000000, false, "transferencia_interna")
		e.control.umbral = 3000000000
		_, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{})
		if !errors.Is(err, nucleo.ErrTopeOperacionExcedido) || e.estado(t, id) != "fallida" || e.contar(t) != 0 {
			t.Fatal(err)
		}
	})
}

func TestConfirmacionDBCodigoIntentosYDispositivo(t *testing.T) {
	e := nuevoEntornoConfirmacion(t)
	id := e.crear(t, 100000, false, "transferencia_interna")
	if _, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{SinVerificacionLocal: true}); !errors.Is(err, ErrCodigoAccionRequerido) {
		t.Fatal(err)
	}
	if _, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{}); !errors.Is(err, ErrCodigoAccionRequerido) {
		t.Fatal("rebajó el código", err)
	}
	d, err := e.control.SolicitarCodigo(e.ctx, id, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		_, err = e.control.Confirmar(e.ctx, id, ConfirmacionAccion{DesafioID: d.ID, Codigo: "000000"})
		if !errors.Is(err, auth.ErrCodigoInvalido) {
			t.Fatal(err)
		}
	}
	if _, err = e.control.Confirmar(e.ctx, id, ConfirmacionAccion{DesafioID: d.ID, Codigo: "123456"}); !errors.Is(err, auth.ErrDemasiadosIntentos) {
		t.Fatal(err)
	}
	if e.estado(t, id) != "pendiente" || e.contar(t) != 0 {
		t.Fatal("ejecutó sin OTP")
	}
}

func TestConfirmacionDBCancelarExpirarYAislar(t *testing.T) {
	t.Run("cancelar", func(t *testing.T) {
		e := nuevoEntornoConfirmacion(t)
		id := e.crear(t, 100000, false, "transferencia_interna")
		if _, err := e.control.Cancelar(e.ctx, id); err != nil {
			t.Fatal(err)
		}
		if _, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{}); !errors.Is(err, ErrAccionNoPendiente) {
			t.Fatal(err)
		}
	})
	t.Run("ajena y contexto", func(t *testing.T) {
		e := nuevoEntornoConfirmacion(t)
		id := e.crear(t, 100000, false, "transferencia_interna")
		for _, parte := range []bool{false, true} {
			ident, _ := identidad.DeContexto(e.ctx)
			if parte {
				ident.ParteID = uuid.NewString()
			} else {
				ident.UsuarioID = uuid.NewString()
			}
			ctx := identidad.ConIdentidad(e.ctx, ident)
			if _, err := e.control.Confirmar(ctx, id, ConfirmacionAccion{}); !errors.Is(err, ErrAccionNoEncontrada) {
				t.Fatal(err)
			}
			if _, err := e.control.Cancelar(ctx, id); !errors.Is(err, ErrAccionNoEncontrada) {
				t.Fatal(err)
			}
		}
	})
	t.Run("expirar y worker", func(t *testing.T) {
		e := nuevoEntornoConfirmacion(t)
		id := e.crear(t, 100000, false, "transferencia_interna")
		e.vencer(t, id)
		if _, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{}); !errors.Is(err, ErrAccionExpirada) || e.estado(t, id) != "vencida" {
			t.Fatal(err)
		}
		id = e.crear(t, 100000, false, "transferencia_interna")
		e.vencer(t, id)
		if n, err := e.control.Expirar(e.ctx); err != nil || n < 1 || e.estado(t, id) != "vencida" {
			t.Fatalf("worker=%d %v", n, err)
		}
	})
	t.Run("revocacion", func(t *testing.T) {
		e := nuevoEntornoConfirmacion(t)
		id := e.crear(t, 100000, false, "transferencia_interna")
		if _, err := e.consent.Registrar(e.ctx, auth.SolicitudDeConsentimientoNuevo{ParteID: uuid.MustParse(e.parte), Tipo: auth.TipoConsentimientoAsistente, VersionTexto: "v1", Aceptado: false, Metodo: "app", Origen: "127.0.0.1"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{}); !errors.Is(err, ErrConsentimientoAsistente) || e.estado(t, id) != "cancelada" || e.contar(t) != 0 {
			t.Fatal(err)
		}
	})
}
func (e *entornoConfirmacion) vencer(t *testing.T, id uuid.UUID) {
	t.Helper()
	tx, err := e.admin.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(t.Context(), "SET LOCAL session_replication_role = replica"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "UPDATE asistente.accion_pendiente SET creada_en=now()-interval '6 minutes',expira_en=now()-interval '1 minute' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmacionDBRevierteLedgerCodigoYAccionEnFalloCommit(t *testing.T) {
	e := nuevoEntornoConfirmacion(t)
	id := e.crear(t, 100000, true, "transferencia_interna")
	d, err := e.control.SolicitarCodigo(e.ctx, id, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	nombre := "fallo_confirmacion_" + uuid.NewString()[:8]
	fn := pgx.Identifier{"asistente", nombre}.Sanitize()
	trigger := pgx.Identifier{nombre}.Sanitize()
	if _, err := e.admin.Exec(e.ctx, "CREATE FUNCTION "+fn+"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id='"+id.String()+"'::uuid AND NEW.estado='confirmada' THEN RAISE EXCEPTION 'fallo de prueba'; END IF; RETURN NEW; END $$"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.admin.Exec(e.ctx, "CREATE CONSTRAINT TRIGGER "+trigger+" AFTER UPDATE ON asistente.accion_pendiente DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION "+fn+"()"); err != nil {
		t.Fatal(err)
	}
	limpiar := func() {
		_, _ = e.admin.Exec(context.Background(), "DROP TRIGGER IF EXISTS "+trigger+" ON asistente.accion_pendiente")
		_, _ = e.admin.Exec(context.Background(), "DROP FUNCTION IF EXISTS "+fn+"()")
	}
	t.Cleanup(limpiar)
	if _, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{DesafioID: d.ID, Codigo: "123456"}); err == nil {
		t.Fatal("faltó fallo del commit")
	}
	var estado string
	if err := e.admin.QueryRow(e.ctx, "SELECT estado FROM auth.desafios WHERE id=$1", d.ID).Scan(&estado); err != nil || estado != "pendiente" || e.estado(t, id) != "pendiente" || e.contar(t) != 0 {
		t.Fatalf("commit parcial: otp=%s %v", estado, err)
	}
	limpiar()
	if _, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{DesafioID: d.ID, Codigo: "123456"}); err != nil || e.contar(t) != 1 {
		t.Fatal(err)
	}
}

func TestConfirmacionDBReenvioVigenciaYVinculo(t *testing.T) {
	t.Run("reenvio conserva intentos", func(t *testing.T) {
		e := nuevoEntornoConfirmacion(t)
		id := e.crear(t, 100000, true, "transferencia_interna")
		d, err := e.control.SolicitarCodigo(e.ctx, id, "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{DesafioID: d.ID, Codigo: "000000"}); !errors.Is(err, auth.ErrCodigoInvalido) {
			t.Fatal(err)
		}
		if _, err := e.admin.Exec(e.ctx, "UPDATE auth.desafios SET ultimo_envio_en=now()-interval '61 seconds' WHERE id=$1", d.ID); err != nil {
			t.Fatal(err)
		}
		d2, err := e.control.SolicitarCodigo(e.ctx, id, "127.0.0.1")
		if err != nil || d2.ID != d.ID {
			t.Fatal(err)
		}
		var intentos, reenvios int
		if err := e.admin.QueryRow(e.ctx, "SELECT intentos,reenvios FROM auth.desafios WHERE id=$1", d.ID).Scan(&intentos, &reenvios); err != nil || intentos != 1 || reenvios != 1 {
			t.Fatalf("%d %d %v", intentos, reenvios, err)
		}
	})
	t.Run("entrega fallida", func(t *testing.T) {
		e := nuevoEntornoConfirmacion(t)
		id := e.crear(t, 100000, true, "transferencia_interna")
		e.canal.err = errors.New("canal privado")
		if _, err := e.control.SolicitarCodigo(e.ctx, id, "127.0.0.1"); !errors.Is(err, auth.ErrEntregaFallida) {
			t.Fatal(err)
		}
		if _, err := e.admin.Exec(e.ctx, "UPDATE auth.desafios SET ultimo_envio_en=now()-interval '61 seconds' WHERE id=(SELECT desafio_id FROM asistente.accion_pendiente WHERE id=$1)", id); err != nil {
			t.Fatal(err)
		}
		e.canal.err = nil
		d, err := e.control.SolicitarCodigo(e.ctx, id, "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{DesafioID: d.ID, Codigo: "123456"}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("desafio de otra accion", func(t *testing.T) {
		e := nuevoEntornoConfirmacion(t)
		id := e.crear(t, 100000, true, "transferencia_interna")
		d, err := e.control.SolicitarCodigo(e.ctx, id, "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.control.Cancelar(e.ctx, id); err != nil {
			t.Fatal(err)
		}
		id2 := e.crear(t, 100000, true, "transferencia_interna")
		if _, err := e.admin.Exec(e.ctx, "UPDATE asistente.accion_pendiente SET desafio_id=$1 WHERE id=$2", d.ID, id2); err != nil {
			t.Fatal(err)
		}
		if _, err := e.control.Confirmar(e.ctx, id2, ConfirmacionAccion{DesafioID: d.ID, Codigo: "123456"}); !errors.Is(err, auth.ErrDesafioInvalido) || e.contar(t) != 0 {
			t.Fatal(err)
		}
		var intentos int
		if err := e.admin.QueryRow(e.ctx, "SELECT intentos FROM auth.desafios WHERE id=$1", d.ID).Scan(&intentos); err != nil || intentos != 0 {
			t.Fatal("consumió desafío ajeno", err)
		}
	})
	t.Run("codigo vencido", func(t *testing.T) {
		e := nuevoEntornoConfirmacion(t)
		id := e.crear(t, 100000, true, "transferencia_interna")
		d, err := e.control.SolicitarCodigo(e.ctx, id, "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.admin.Exec(e.ctx, "UPDATE auth.desafios SET expira_en=now()-interval '1 second' WHERE id=$1", d.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.control.Confirmar(e.ctx, id, ConfirmacionAccion{DesafioID: d.ID, Codigo: "123456"}); !errors.Is(err, auth.ErrCodigoExpirado) || e.contar(t) != 0 {
			t.Fatal(err)
		}
	})
}

func TestConfirmacionDBUnaConexionYFalloTecnico(t *testing.T) {
	e := nuevoEntornoConfirmacion(t)
	cfg := e.app.Config()
	cfg.MaxConns = 1
	unico, err := pgxpool.NewWithConfig(e.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer unico.Close()
	consent := auth.NuevoServicioDeConsentimientos(unico)
	otp, err := auth.NuevoGestorDesafios(unico, e.canal, nil, auth.NuevoLectorOptInDeConsentimientos(unico), bytes.Repeat([]byte{9}, 32), auth.ConRacionamiento(e.contador, e.claves))
	if err != nil {
		t.Fatal(err)
	}
	servicio := nucleo.NewServicio(unico, kycPreparacionPrueba{}, nil, nil, nil, nucleo.ConResolutorDestino(auth.NuevoResolutorDestino(unico, time.Hour)))
	control, err := NuevoControlAcciones(unico, servicio, otp, NuevoControlConsentimiento(consent, NuevoCanceladorConsentimiento(unico)), 50000000)
	if err != nil {
		t.Fatal(err)
	}
	id := e.crear(t, 100000, true, "transferencia_interna")
	d, err := control.SolicitarCodigo(e.ctx, id, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	control.nucleo = nucleo.NewServicio(unico, kycPreparacionPrueba{}, nil, nil, nil)
	if _, err := control.Confirmar(e.ctx, id, ConfirmacionAccion{DesafioID: d.ID, Codigo: "123456"}); err == nil || e.estado(t, id) != "pendiente" || e.contar(t) != 0 {
		t.Fatal("fallo técnico resuelto como definitivo", err)
	}
	control.nucleo = servicio
	ctx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
	defer cancel()
	if _, err := control.Confirmar(ctx, id, ConfirmacionAccion{DesafioID: d.ID, Codigo: "123456"}); err != nil || e.contar(t) != 1 {
		t.Fatal("no reutilizó OTP o pidió otra conexión", err)
	}
}
