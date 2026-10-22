//go:build integration

package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type kycPreparacionPrueba struct{}

func (kycPreparacionPrueba) NivelKYC(context.Context, string) (string, error) {
	return nucleo.NivelKYCDepositoBajoMonto, nil
}

type directorioPreparacionPrueba struct{ err error }

func (d directorioPreparacionPrueba) Resolver(context.Context, nucleo.Llave) (nucleo.ResolucionDirectorio, error) {
	return nucleo.ResolucionDirectorio{Entidad: "Banco de prueba", Producto: "producto-privado"}, d.err
}

type entornoPreparacion struct {
	admin, app                                      *pgxpool.Pool
	ctx                                             context.Context
	usuario, parte, destino, celular, celularPropio string
	conv                                            uuid.UUID
	servicio                                        *nucleo.Servicio
	resolutor                                       *auth.ResolutorDestino
	refs                                            *ReferenciasDestinatario
	redis                                           *miniredis.Miniredis
	contador                                        auth.Contadores
	claves                                          *auth.Claves
	limite                                          *auth.LimiteDestinatarios
	guardador                                       *PropuestasPostgres
}

func nuevoEntornoPreparacion(t *testing.T) *entornoPreparacion {
	t.Helper()
	e := &entornoPreparacion{admin: poolPersistencia(t, "DATABASE_URL"), app: poolPersistencia(t, "APP_DATABASE_URL"), usuario: uuid.NewString(), parte: uuid.NewString(), destino: uuid.NewString()}
	for _, id := range []string{e.parte, e.usuario, e.destino} {
		if _, err := e.admin.Exec(t.Context(), "INSERT INTO auth.partes(id,tipo) VALUES($1,'persona')", id); err != nil {
			t.Fatal(err)
		}
	}
	base := int(uuid.New().ID()%8000000) + 1000000
	for i, id := range []string{e.usuario, e.destino} {
		cel := fmt.Sprintf("+57300%07d", base+i)
		if i == 0 {
			e.celularPropio = cel
		} else {
			e.celular = cel
		}
		if _, err := e.admin.Exec(t.Context(), "INSERT INTO auth.personas(parte_id,tipo_documento,documento,nombre) VALUES($1,'CC',$2,'Laura Rodríguez')", id, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		if _, err := e.admin.Exec(t.Context(), "INSERT INTO auth.credenciales(id,parte_id,correo,celular,celular_verificado_en,valor_derivado,estado) VALUES($1,$2,$3,$4,now(),'prueba','activo')", uuid.New(), id, uuid.NewString()+"@test.local", cel); err != nil {
			t.Fatal(err)
		}
	}
	e.resolutor = auth.NuevoResolutorDestino(e.app, time.Hour)
	e.servicio = nucleo.NewServicio(e.app, kycPreparacionPrueba{}, nil, nil, nil, nucleo.ConResolutorDestino(e.resolutor), nucleo.ConDirectorio(directorioPreparacionPrueba{}))
	for _, id := range []string{e.usuario, e.destino} {
		if _, err := e.servicio.AbrirSubcuentasDeUsuario(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	conv, err := EnTransaccionAsistente(t.Context(), e.app, func(q *repositorio.Queries) (repositorio.Conversacion, error) {
		return q.CrearConversacion(t.Context(), repositorio.CrearConversacionParams{UsuarioID: uuidCiclo(uuid.MustParse(e.parte))})
	})
	if err != nil {
		t.Fatal(err)
	}
	e.conv = uuid.UUID(conv.ID.Bytes)
	e.ctx = identidad.ConIdentidad(context.WithValue(t.Context(), claveConversacionCiclo{}, e.conv), identidad.Identidad{UsuarioID: e.usuario, ParteID: e.parte})
	e.redis = miniredis.RunT(t)
	cliente := redis.NewClient(&redis.Options{Addr: e.redis.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = cliente.Close() })
	e.contador, err = contadores.NuevoRedis(cliente)
	if err != nil {
		t.Fatal(err)
	}
	pepper := bytes.Repeat([]byte{9}, 32)
	e.claves, _ = auth.NuevasClaves(pepper)
	e.refs, _ = NuevasReferenciasDestinatario(pepper)
	e.limite = auth.NuevoLimiteDestinatarios(e.contador, e.claves, 50, time.Hour)
	e.guardador = NuevasPropuestasPostgres(e.app)
	t.Cleanup(func() {
		for _, sentencia := range []string{"DELETE FROM asistente.accion_pendiente WHERE usuario_id=$1", "DELETE FROM asistente.conversacion WHERE usuario_id=$1"} {
			if _, err := e.admin.Exec(context.Background(), sentencia, e.parte); err != nil {
				t.Error(err)
			}
		}
		for _, id := range []string{e.parte, e.usuario, e.destino} {
			for _, tabla := range []string{"credenciales", "personas", "partes"} {
				columna := "parte_id"
				if tabla == "partes" {
					columna = "id"
				}
				if _, err := e.admin.Exec(context.Background(), "DELETE FROM auth."+tabla+" WHERE "+columna+"=$1", id); err != nil {
					t.Error(err)
				}
			}
		}
	})
	return e
}

func (e *entornoPreparacion) abonar(t *testing.T, monto nucleo.Centavos) {
	t.Helper()
	tx, err := e.admin.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var entry int64
	if err := tx.QueryRow(t.Context(), "INSERT INTO nucleo.journal_entry(tipo,ocurrido_en) VALUES('deposito',now()) RETURNING id").Scan(&entry); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), "INSERT INTO nucleo.posting(entry_id,cuenta_id,monto,moneda) VALUES($1,$2,$3,'COP'),($1,$4,$5,'COP')", entry, nucleo.NombreCuentaDisponible(e.usuario), -monto.Int64(), nucleo.CuentaCustodiaBanco, monto.Int64()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (e *entornoPreparacion) catalogo(t *testing.T, llaves bool) *Catalogo {
	t.Helper()
	p, err := NuevoPreparadorTransferencias(e.servicio, e.resolutor, e.guardador, e.refs, e.limite, e.contador, e.claves, ConfigPreparacion{UmbralCodigo: 50000000, LlavesHabilitadas: llaves})
	if err != nil {
		t.Fatal(err)
	}
	c, err := NuevoCatalogo(map[string]ManejadorHerramienta{"preparar_transferencia": p.Manejar}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (e *entornoPreparacion) llamar(t *testing.T, c *Catalogo, args string) json.RawMessage {
	t.Helper()
	r, err := c.Ejecutar(e.ctx, LlamadaIA{Nombre: "preparar_transferencia", Argumentos: []byte(args)})
	if err != nil {
		t.Fatal(err)
	}
	for _, secreto := range []string{e.usuario, e.parte, e.destino, e.celular, "Laura Rodríguez", "producto-privado"} {
		if bytes.Contains(r.Datos, []byte(secreto)) {
			t.Fatalf("filtró %s: %s", secreto, r.Datos)
		}
	}
	return r.Datos
}

func (e *entornoPreparacion) huellaDinero(t *testing.T) string {
	t.Helper()
	var huella string
	err := e.admin.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM nucleo.posting)::text || ':' || (SELECT count(*) FROM nucleo.journal_entry)::text || ':' || (SELECT count(*) FROM nucleo.transferencia)::text || ':' || (SELECT count(*) FROM nucleo.outbox)::text || ':' || COALESCE((SELECT sum(monto) FROM nucleo.posting WHERE cuenta_id=$1),0)::text || ':' || COALESCE((SELECT sum(monto) FROM nucleo.posting WHERE cuenta_id=$2),0)::text`, nucleo.NombreCuentaDisponible(e.usuario), nucleo.NombreCuentaRetenido(e.usuario)).Scan(&huella)
	if err != nil {
		t.Fatal(err)
	}
	return huella
}

func (e *entornoPreparacion) accion(t *testing.T, raw []byte) repositorio.AccionPendiente {
	t.Helper()
	var res struct {
		Datos struct {
			ID uuid.UUID `json:"accion_id"`
		}
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Datos.ID == uuid.Nil {
		t.Fatalf("sin acción: %s %v", raw, err)
	}
	a, err := EnTransaccionAsistente(e.ctx, e.app, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
		return q.ObtenerAccion(e.ctx, repositorio.ObtenerAccionParams{ID: uuidCiclo(res.Datos.ID), UsuarioID: uuidCiclo(uuid.MustParse(e.parte))})
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPreparacionAccionRealSinMoverDinero(t *testing.T) {
	e := nuevoEntornoPreparacion(t)
	e.abonar(t, 100000000)
	c := e.catalogo(t, true)
	antes := e.huellaDinero(t)
	raw := e.llamar(t, c, fmt.Sprintf(`{"celular":%q,"monto_pesos":"500000"}`, e.celular))
	a := e.accion(t, raw)
	if a.Estado != "pendiente" || a.Destino != e.destino || a.UsuarioFinancieroID != e.usuario || uuid.UUID(a.UsuarioID.Bytes).String() == e.usuario || a.Monto != 50000000 || a.Moneda != "COP" || a.RequiereCodigo || a.DesafioID.Valid || a.TransferenciaID.Valid || a.ExpiraEn.Time.Sub(a.CreadaEn.Time) != 5*time.Minute {
		t.Fatalf("propuesta: %+v", a)
	}
	if a.NombreMostrado != "L*** R***" || a.CelularEnmascarado.String != celularParaModelo(e.celular) || !bytes.Contains(raw, []byte(a.CelularEnmascarado.String)) {
		t.Fatalf("enmascarado: %s %+v", raw, a)
	}
	ref, err := e.refs.Emitir(e.usuario, e.conv, e.destino)
	if err != nil {
		t.Fatal(err)
	}
	segunda := e.accion(t, e.llamar(t, c, fmt.Sprintf(`{"destinatario_ref":%q,"monto_pesos":"500001"}`, ref)))
	if !segunda.RequiereCodigo || segunda.Monto != 50000100 {
		t.Fatalf("umbral: %+v", segunda)
	}
	rechazada := e.llamar(t, c, fmt.Sprintf(`{"destinatario_ref":%q,"monto_pesos":"20000000"}`, ref))
	if !bytes.Contains(rechazada, []byte("OperationLimitExceeded")) {
		t.Fatalf("rechazo: %s", rechazada)
	}
	var estadoSegunda string
	if err := e.admin.QueryRow(t.Context(), "SELECT estado FROM asistente.accion_pendiente WHERE id=$1", segunda.ID).Scan(&estadoSegunda); err != nil || estadoSegunda != "pendiente" {
		t.Fatalf("rechazo canceló propuesta válida: %s %v", estadoSegunda, err)
	}
	var estado string
	if err := e.admin.QueryRow(t.Context(), "SELECT estado FROM asistente.accion_pendiente WHERE id=$1", a.ID).Scan(&estado); err != nil || estado != "cancelada" {
		t.Fatalf("anterior: %s %v", estado, err)
	}
	porLlave := e.accion(t, e.llamar(t, c, `{"llave_breb":"@laura","monto_pesos":"1000"}`))
	if porLlave.Tipo != "transferencia_llave" || porLlave.TipoLlave.String != "alfanumerico" || porLlave.Destino != "@laura" || porLlave.EntidadDestino.String != "Banco de prueba" {
		t.Fatalf("llave: %+v", porLlave)
	}
	if despues := e.huellaDinero(t); despues != antes {
		t.Fatalf("preparar cambió dinero: %s -> %s", antes, despues)
	}
	// El origen es inmutable incluso para el dueño de la base.
	if _, err := e.admin.Exec(t.Context(), "UPDATE asistente.accion_pendiente SET usuario_financiero_id=$1 WHERE id=$2", uuid.NewString(), porLlave.ID); err == nil {
		t.Fatal("permitió cambiar el origen financiero")
	}
}

func TestPreparacionRechazosReales(t *testing.T) {
	for nombre, codigo := range map[string]string{"sinSaldo": "InsufficientFunds", "operacion": "OperationLimitExceeded", "mes": "MonthlyQuotaExceeded", "gmf": "InsufficientFunds", "propio": "SelfTransferNotAllowed", "inexistente": "RecipientNotFound", "referenciaAjena": "ValidationError", "llaveInvalida": "ValidationError", "directorio": "DirectoryUnavailable", "llaveDeshabilitada": "FunctionUnavailable", "conversacionAjena": "NotFound"} {
		t.Run(nombre, func(t *testing.T) {
			e := nuevoEntornoPreparacion(t)
			monto := "1000"
			destino := fmt.Sprintf(`"celular":%q`, e.celular)
			if nombre != "sinSaldo" {
				e.abonar(t, 100000)
			}
			switch nombre {
			case "operacion":
				monto = "20000000"
			case "mes", "gmf":
				acumulado := int64(1102472699)
				if nombre == "gmf" {
					acumulado = 340431000
				}
				if _, err := e.admin.Exec(t.Context(), "INSERT INTO nucleo.transferencia(id,usuario_id,cuenta_origen,llave_destino,monto,idempotency_key,estado) VALUES($1,$2,$3,'prueba',$4,$5,'reservada')", uuid.New(), e.usuario, nucleo.NombreCuentaDisponible(e.usuario), acumulado, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			case "propio":
				destino = fmt.Sprintf(`"celular":%q`, e.celularPropio)
			case "inexistente":
				if _, err := e.admin.Exec(t.Context(), "UPDATE auth.credenciales SET estado='suspendido' WHERE parte_id=$1", e.destino); err != nil {
					t.Fatal(err)
				}
			case "referenciaAjena":
				ref, err := e.refs.Emitir(uuid.NewString(), e.conv, e.destino)
				if err != nil {
					t.Fatal(err)
				}
				destino = fmt.Sprintf(`"destinatario_ref":%q`, ref)
			case "llaveInvalida":
				destino = `"llave_breb":"1234567"`
			case "directorio":
				e.servicio = nucleo.NewServicio(e.app, kycPreparacionPrueba{}, nil, nil, nil, nucleo.ConDirectorio(directorioPreparacionPrueba{err: nucleo.ErrDirectorioSinRespuesta}))
				destino = `"llave_breb":"@laura"`
			case "llaveDeshabilitada":
				destino = `"llave_breb":"@laura"`
			case "conversacionAjena":
				e.ctx = identidad.ConIdentidad(e.ctx, identidad.Identidad{UsuarioID: e.usuario, ParteID: e.destino})
			}
			antes := e.huellaDinero(t)
			raw := e.llamar(t, e.catalogo(t, nombre != "llaveDeshabilitada"), fmt.Sprintf(`{%s,"monto_pesos":%q}`, destino, monto))
			if !bytes.Contains(raw, []byte(codigo)) {
				t.Fatalf("quiero %s: %s", codigo, raw)
			}
			var acciones int
			if err := e.admin.QueryRow(t.Context(), "SELECT count(*) FROM asistente.accion_pendiente WHERE conversacion_id=$1", e.conv).Scan(&acciones); err != nil || acciones != 0 {
				t.Fatalf("rechazo creó acción: %d %v", acciones, err)
			}
			if e.huellaDinero(t) != antes {
				t.Fatal("rechazo movió dinero")
			}
		})
	}
}

func TestPreparacionLimiteRedisYReemplazoConcurrente(t *testing.T) {
	e := nuevoEntornoPreparacion(t)
	e.abonar(t, 100000000)
	c := e.catalogo(t, false)
	ref, err := e.refs.Emitir(e.usuario, e.conv, e.destino)
	if err != nil {
		t.Fatal(err)
	}
	args := fmt.Sprintf(`{"destinatario_ref":%q,"monto_pesos":"1000"}`, ref)
	var wg sync.WaitGroup
	fallos := make(chan error, 10)
	for range 10 {
		wg.Go(func() {
			r, err := c.Ejecutar(e.ctx, LlamadaIA{Nombre: "preparar_transferencia", Argumentos: []byte(args)})
			if err != nil || bytes.Contains(r.Datos, []byte(`"error"`)) {
				fallos <- fmt.Errorf("preparar: %s %w", r.Datos, err)
			}
		})
	}
	wg.Wait()
	close(fallos)
	for err := range fallos {
		t.Error(err)
	}
	var pendientes, canceladas int
	if err := e.admin.QueryRow(t.Context(), "SELECT count(*) FILTER(WHERE estado='pendiente'),count(*) FILTER(WHERE estado='cancelada') FROM asistente.accion_pendiente WHERE conversacion_id=$1", e.conv).Scan(&pendientes, &canceladas); err != nil || pendientes != 1 || canceladas != 9 {
		t.Fatalf("concurrencia: %d %d %v", pendientes, canceladas, err)
	}
	if raw := e.llamar(t, c, args); !bytes.Contains(raw, []byte("TooManyRequests")) {
		t.Fatalf("cuota: %s", raw)
	}
	otroContexto := identidad.ConIdentidad(e.ctx, identidad.Identidad{ParteID: e.parte, UsuarioID: uuid.NewString()})
	r, err := c.Ejecutar(otroContexto, LlamadaIA{Nombre: "preparar_transferencia", Argumentos: []byte(args)})
	if err != nil || !bytes.Contains(r.Datos, []byte("TooManyRequests")) {
		t.Fatalf("cambiar contexto eludió cuota: %s %v", r.Datos, err)
	}
	e.redis.FastForward(time.Hour)
	if raw := e.llamar(t, c, args); bytes.Contains(raw, []byte(`"error"`)) {
		t.Fatalf("TTL: %s", raw)
	}
	e.redis.Close()
	if raw := e.llamar(t, c, args); !bytes.Contains(raw, []byte("FunctionUnavailable")) {
		t.Fatalf("Redis caído: %s", raw)
	}
	if _, err := e.guardador.GuardarPropuesta(e.ctx, PropuestaTransferencia{Tipo: "transferencia_interna", Destino: e.destino, Nombre: "L*** R***", Monto: -1, UsuarioFinanciero: e.usuario}); err == nil {
		t.Fatal("INSERT inválido no falló")
	}
	// El INSERT fallido revierte también la cancelación anterior.
	if err := e.admin.QueryRow(t.Context(), "SELECT count(*) FROM asistente.accion_pendiente WHERE conversacion_id=$1 AND estado='pendiente'", e.conv).Scan(&pendientes); err != nil || pendientes != 1 {
		t.Fatalf("rollback perdió propuesta: %d %v", pendientes, err)
	}
}

func TestPreparacionArgumentosInvalidosNoCreanAccion(t *testing.T) {
	e := nuevoEntornoPreparacion(t)
	c := e.catalogo(t, false)
	for _, args := range []string{`{}`, `{"monto_pesos":"1000"}`, `{"celular":"3001234567","llave_breb":"@laura","monto_pesos":"1000"}`, `{"celular":"3001234567","monto_pesos":"0"}`, `{"celular":"3001234567","monto_pesos":"1.5"}`, `{"celular":"3001234567","monto_pesos":1000}`, `{"celular":"3001234567","monto_pesos":"92233720368547759"}`, `{"celular":"3001234567","monto_pesos":"cincuenta"}`} {
		if raw := e.llamar(t, c, args); !bytes.Contains(raw, []byte("ValidationError")) {
			t.Fatalf("aceptó %s: %s", args, raw)
		}
	}
	if _, err := e.redis.Get("vera:preparaciones:" + e.claves.ClaveDeParte(e.parte)); err == nil {
		t.Fatal("argumentos inválidos consumieron cuota")
	}
	var acciones int
	if err := e.admin.QueryRow(t.Context(), "SELECT count(*) FROM asistente.accion_pendiente WHERE conversacion_id=$1", e.conv).Scan(&acciones); err != nil || acciones != 0 {
		t.Fatalf("argumentos inválidos crearon %d acciones: %v", acciones, err)
	}
}

func TestPreparacionReemplazaPendienteYaExpirada(t *testing.T) {
	e := nuevoEntornoPreparacion(t)
	e.abonar(t, 1000000)
	anterior := uuid.New()
	if _, err := e.admin.Exec(t.Context(), "INSERT INTO asistente.accion_pendiente(id,usuario_id,conversacion_id,tipo,destino,nombre_mostrado,monto,requiere_codigo,creada_en,expira_en,usuario_financiero_id) VALUES($1,$2,$3,'transferencia_interna',$4,'L*** R***',100,false,now()-interval '10 minutes',now()-interval '5 minutes',$5)", anterior, e.parte, e.conv, e.destino, e.usuario); err != nil {
		t.Fatal(err)
	}
	raw := e.llamar(t, e.catalogo(t, false), fmt.Sprintf(`{"celular":%q,"monto_pesos":"1000"}`, e.celular))
	nueva := e.accion(t, raw)
	if nueva.Estado != "pendiente" {
		t.Fatal(nueva.Estado)
	}
	var estado string
	if err := e.admin.QueryRow(t.Context(), "SELECT estado FROM asistente.accion_pendiente WHERE id=$1", anterior).Scan(&estado); err != nil || estado != "vencida" {
		t.Fatalf("anterior expirada: %s %v", estado, err)
	}
}
