//go:build integration

package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func prepararCicloDB(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool, context.Context, EntradaOrquestador) {
	t.Helper()
	admin := poolPersistencia(t, "DATABASE_URL")
	cfg, err := pgxpool.ParseConfig(os.Getenv("APP_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	app, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	parte := uuid.New()
	if _, err := admin.Exec(t.Context(), "INSERT INTO auth.partes(id,tipo) VALUES ($1,'persona')", parte); err != nil {
		t.Fatal(err)
	}
	entrada, err := EnTransaccionAsistente(t.Context(), app, func(q *repositorio.Queries) (EntradaOrquestador, error) {
		c, err := q.CrearConversacion(t.Context(), repositorio.CrearConversacionParams{UsuarioID: uuidCiclo(parte)})
		if err != nil {
			return EntradaOrquestador{}, err
		}
		m, err := q.AnexarMensaje(t.Context(), repositorio.AnexarMensajeParams{ConversacionID: c.ID, UsuarioID: uuidCiclo(parte), Rol: "usuario", Canal: "texto", Contenido: "¿Cuál es mi saldo?", Metadatos: []byte(`{}`)})
		return EntradaOrquestador{ConversacionID: uuid.UUID(c.ID.Bytes), MensajeID: uuid.UUID(m.ID.Bytes)}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DELETE FROM asistente.accion_pendiente WHERE conversacion_id=$1", entrada.ConversacionID)
		_, _ = admin.Exec(context.Background(), "DELETE FROM asistente.conversacion WHERE id=$1", entrada.ConversacionID)
		_, _ = admin.Exec(context.Background(), "DELETE FROM auth.partes WHERE id=$1", parte)
	})
	ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: parte.String(), UsuarioID: uuid.NewString()})
	return admin, app, ctx, entrada
}

func TestOrquestadorDBAccionEstructuradaDelServidor(t *testing.T) {
	_, app, ctx, e := prepararCicloDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var accion uuid.UUID
	c, err := NuevoCatalogo(map[string]ManejadorHerramienta{"preparar_transferencia": func(ctx context.Context, id identidad.Identidad, _ ArgumentosHerramienta) (DatosHerramienta, error) {
		conversacion, ok := ConversacionDelContexto(ctx)
		if !ok || conversacion != e.ConversacionID {
			t.Error("conversación no autorizada")
		}
		parte, err := uuid.Parse(id.ParteID)
		if err != nil {
			return nil, err
		}
		a, err := EnTransaccionAsistente(ctx, app, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
			return q.CrearAccion(ctx, repositorio.CrearAccionParams{ConversacionID: uuidCiclo(conversacion), UsuarioID: uuidCiclo(parte), Tipo: "transferencia_interna", Destino: "+573001234567", NombreMostrado: "Laura Ruiz", CelularEnmascarado: pgtype.Text{String: "***567", Valid: true}, Monto: 10000, RequiereCodigo: true})
		})
		if err != nil {
			return nil, err
		}
		accion = uuid.UUID(a.ID.Bytes)
		// La proyección del catálogo tampoco decide el monto o el factor de la respuesta final.
		return AccionHerramienta{AccionID: accion, Monto: 7777, Destinatario: DestinatarioHerramienta{Nombre: "Laura Ruiz", Celular: "+573001234567"}, ExpiraEn: a.ExpiraEn.Time}, nil
	}}, logger)
	if err != nil {
		t.Fatal(err)
	}
	vueltas := 0
	p := proveedorCicloPrueba{generar: func(context.Context, SolicitudIA) (RespuestaIA, error) {
		vueltas++
		if vueltas == 1 {
			return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Llamada: &LlamadaIA{ID: "preparar", Nombre: "preparar_transferencia", Argumentos: []byte(`{"celular":"+573001234567","monto_pesos":"100"}`)}}}}}, nil
		}
		return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: "Revisa la propuesta en la aplicación."}}}}, nil
	}}
	o, err := NuevoOrquestador(p, c, NuevaMemoriaCicloPostgres(app), autorizacionCicloPrueba{}, "simulado", logger)
	if err != nil {
		t.Fatal(err)
	}
	r, err := o.Responder(ctx, e)
	if err != nil || r.AccionPendiente == nil || r.AccionPendiente.ID != accion || r.AccionPendiente.Monto != 10000 || !r.AccionPendiente.RequiereCodigo || r.AccionPendiente.NombreMostrado != "Laura Ruiz" {
		t.Fatalf("acción desde SQL: %+v %v", r, err)
	}
}

func TestOrquestadorDBReanudaPersisteYFiltra(t *testing.T) {
	_, app, ctx, e := prepararCicloDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	funciones := 0
	catalogo, err := NuevoCatalogo(map[string]ManejadorHerramienta{"consultar_saldo": func(context.Context, identidad.Identidad, ArgumentosHerramienta) (DatosHerramienta, error) {
		funciones++
		return SaldoHerramienta{Disponible: 12345}, nil
	}}, logger)
	if err != nil {
		t.Fatal(err)
	}
	vueltas := 0
	p := proveedorCicloPrueba{generar: func(ctx context.Context, s SolicitudIA) (RespuestaIA, error) {
		// Con un pool de una conexión esto también detecta transacciones abiertas durante IA.
		var rol string
		if err := app.QueryRow(ctx, "SELECT current_user").Scan(&rol); err != nil || rol != "rol_api" {
			t.Fatalf("proveedor dentro de transacción o rol incorrecto: %s %v", rol, err)
		}
		vueltas++
		switch vueltas {
		case 1:
			return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Firma: []byte("firma-persistida"), Llamada: &LlamadaIA{ID: "saldo-1", Nombre: "consultar_saldo", Argumentos: []byte(`{}`)}}}}, TokensEntrada: 10, TokensSalida: 2}, nil
		case 2:
			return RespuestaIA{}, ErrTimeoutIA
		default:
			if len(s.Historial) != 3 || s.Historial[1].Partes[0].Llamada.ID != "saldo-1" || string(s.Historial[1].Partes[0].Firma) != "firma-persistida" || s.Historial[2].Partes[0].Resultado.ID != "saldo-1" {
				t.Error("no se restauró el protocolo de funciones")
			}
			return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: " Tu saldo disponible es 123.45 COP. ", Firma: []byte("firma-final")}}}, TokensEntrada: 15, TokensSalida: 5}, nil
		}
	}}
	memoria := NuevaMemoriaCicloPostgres(app)
	o, err := NuevoOrquestador(p, catalogo, memoria, autorizacionCicloPrueba{}, "modelo-db", logger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Responder(ctx, e); !errors.Is(err, ErrTimeoutIA) {
		t.Fatal(err)
	}
	r, err := o.Responder(ctx, e)
	if err != nil || r.Vueltas != 2 || r.Llamadas != 1 || funciones != 1 {
		t.Fatalf("recuperar: %+v %v, funciones=%d", r, err, funciones)
	}
	parte, _ := parteDelAsistente(ctx)
	_, err = EnTransaccionAsistente(ctx, app, func(q *repositorio.Queries) (struct{}, error) {
		filas, err := q.ContextoConversacion(ctx, repositorio.ContextoConversacionParams{ConversacionID: uuidCiclo(e.ConversacionID), UsuarioID: uuidCiclo(parte)})
		if err != nil {
			return struct{}{}, err
		}
		if len(filas) != 4 {
			t.Fatalf("filas: %d", len(filas))
		}
		for i, f := range filas {
			if f.NumeroOrden != int32(i+1) {
				t.Fatal("orden incorrecto")
			}
			if i > 0 && (f.VersionInstrucciones.String != VersionInstruccionesVera || f.NombreModelo.String != "modelo-db") {
				t.Fatal("sin versión o modelo")
			}
			var meta protocoloPersistido
			if err := json.Unmarshal(f.Metadatos, &meta); err != nil {
				t.Fatal(err)
			}
			if (i == 1 || i == 2) != meta.Interno {
				t.Fatal("clasificación pública incorrecta")
			}
			if i == 3 && (string(meta.Protocolo.Partes[0].Firma) != "firma-final" || f.Contenido != "Tu saldo disponible es 123.45 COP.") {
				t.Fatal("se perdió firma del texto final o su proyección pública")
			}
		}
		visibles, err := q.MensajesPublicosConversacion(ctx, repositorio.MensajesPublicosConversacionParams{ConversacionID: uuidCiclo(e.ConversacionID), UsuarioID: uuidCiclo(parte)})
		if err != nil {
			return struct{}{}, err
		}
		if len(visibles) != 2 || visibles[0].Rol != "usuario" || visibles[1].Rol != "asistente" || visibles[1].RespuestaA.Bytes != [16]byte(e.MensajeID) {
			t.Fatal("se expuso un turno interno o faltó correlación")
		}
		pendientes, err := q.MensajesSinRespuesta(ctx, repositorio.MensajesSinRespuestaParams{ConversacionID: uuidCiclo(e.ConversacionID), UsuarioID: uuidCiclo(parte)})
		if err == nil && len(pendientes) != 0 {
			t.Fatal("respuesta no vinculada al mensaje")
		}
		return struct{}{}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := memoria.Abrir(ctx, e); !errors.Is(err, ErrCicloNoDisponible) {
		t.Fatalf("mensaje resuelto se procesó de nuevo: %v", err)
	}
}

func TestOrquestadorDBExclusionCaducidadYTerceros(t *testing.T) {
	admin, app, ctx, e := prepararCicloDB(t)
	memoria := NuevaMemoriaCicloPostgres(app)
	primero, err := memoria.Abrir(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	for _, identidadCtx := range []context.Context{ctx, identidad.ConIdentidad(ctx, identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()})} {
		if _, err := memoria.Abrir(identidadCtx, e); !errors.Is(err, ErrCicloNoDisponible) {
			t.Fatal(err)
		}
	}
	parte, _ := parteDelAsistente(ctx)
	_, err = EnTransaccionAsistente(ctx, app, func(q *repositorio.Queries) (repositorio.Mensaje, error) {
		return q.AnexarMensaje(ctx, repositorio.AnexarMensajeParams{ConversacionID: uuidCiclo(e.ConversacionID), UsuarioID: uuidCiclo(parte), Rol: "usuario", Canal: "texto", Contenido: "otro mensaje", Metadatos: []byte(`{}`)})
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("se aceptó otra entrada durante el ciclo: %v", err)
	}
	if _, err := admin.Exec(ctx, "UPDATE asistente.conversacion SET ciclo_expira_en=clock_timestamp()-interval '1 second' WHERE id=$1", e.ConversacionID); err != nil {
		t.Fatal(err)
	}
	segundo, err := memoria.Abrir(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	registro := RegistroCiclo{Respuesta: RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: "respuesta atrasada"}}}}, Modelo: "prueba", VersionInstrucciones: VersionInstruccionesVera, Final: true, Latencia: time.Millisecond}
	if err := primero.Guardar(ctx, registro); !errors.Is(err, ErrCicloNoDisponible) {
		t.Fatal(err)
	}
	if err := primero.Cerrar(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := memoria.Abrir(ctx, e); !errors.Is(err, ErrCicloNoDisponible) {
		t.Fatal("la limpieza atrasada liberó el ciclo nuevo")
	}
	if err := segundo.Cerrar(ctx); err != nil {
		t.Fatal(err)
	}
	tercero, err := memoria.Abrir(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tercero.Cerrar(ctx); err != nil {
		t.Fatal(err)
	}
}
