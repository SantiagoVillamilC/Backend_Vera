//go:build integration

package copilot

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestVozDBReintentaPreparacionSinAudioPersistido(t *testing.T) {
	var app *pgxpool.Pool
	asr, vueltas, preparaciones := 0, 0, 0
	p := proveedorCicloPrueba{generar: func(_ context.Context, s SolicitudIA) (RespuestaIA, error) {
		if s.Historial[0].Partes[0].Audio != nil {
			asr++
			if len(s.Funciones) != 0 {
				t.Error("funciones habilitadas durante ASR")
			}
			return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: `{"transcripcion":"Prepara una transferencia de 150000 pesos"}`}}}}, nil
		}
		for _, turno := range s.Historial {
			for _, parte := range turno.Partes {
				if parte.Audio != nil {
					t.Error("audio llegó al historial persistido")
				}
			}
		}
		vueltas++
		if vueltas == 1 {
			return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Llamada: &LlamadaIA{ID: "prep", Nombre: "preparar_transferencia", Argumentos: []byte(`{"celular":"+573001234567","monto_pesos":"150000"}`)}}}}}, nil
		}
		if vueltas == 2 {
			return RespuestaIA{}, ErrNoDisponibleIA
		}
		return respuestaTextoDB(), nil
	}}
	h := func(ctx context.Context, id identidad.Identidad, _ ArgumentosHerramienta) (DatosHerramienta, error) {
		preparaciones++
		conv, _ := ConversacionDelContexto(ctx)
		parte, _ := uuid.Parse(id.ParteID)
		a, err := EnTransaccionAsistente(ctx, app, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
			return q.CrearAccion(ctx, repositorio.CrearAccionParams{ConversacionID: uuidCiclo(conv), UsuarioID: uuidCiclo(parte), UsuarioFinancieroID: pgtype.Text{String: id.UsuarioID, Valid: true}, Tipo: "transferencia_interna", Destino: "+573001234567", NombreMostrado: "Laura R.", Monto: 15000000, RequiereCodigo: true})
		})
		if err != nil {
			return nil, err
		}
		return AccionHerramienta{AccionID: uuid.UUID(a.ID.Bytes), Monto: 1, ExpiraEn: a.ExpiraEn.Time}, nil
	}
	admin, pool, ctx, texto := prepararTextoDB(t, p, map[string]ManejadorHerramienta{"preparar_transferencia": h})
	app = pool
	voz := NuevoServicioVoz(texto, p)
	a := &AudioIA{MIME: "audio/m4a", Datos: clipPrueba(t, "tono.m4a")}
	clave := uuid.New()
	r, err := voz.EnviarVoz(ctx, uuid.Nil, a, clave)
	if !errors.Is(err, ErrNoDisponibleIA) || r.MensajeUsuarioID == uuid.Nil {
		t.Fatalf("no conservó transcripción: %+v %v", r, err)
	}
	conv, user := r.ConversacionID, r.MensajeUsuarioID
	r, err = voz.EnviarVoz(ctx, uuid.Nil, a, clave)
	if err != nil || r.MensajeUsuarioID != user || r.Transcripcion != "Prepara una transferencia de 150000 pesos" || r.Accion == nil || r.Accion.Monto != 15000000 || !r.Accion.RequiereCodigo || preparaciones != 1 || asr != 1 {
		t.Fatalf("reintento: %+v %v asr=%d preparaciones=%d", r, err, asr, preparaciones)
	}
	final := r.MensajeID
	r, err = voz.EnviarVoz(ctx, uuid.Nil, a, clave)
	if err != nil || r.MensajeID != final || asr != 1 || vueltas != 3 {
		t.Fatalf("replay: %+v %v", r, err)
	}
	if _, err = texto.EnviarTexto(ctx, uuid.Nil, "Otro", clave); !errors.Is(err, ErrClaveTextoEnConflicto) {
		t.Fatal(err)
	}
	otroAudio := &AudioIA{MIME: "audio/wav", Datos: clipPrueba(t, "tono.wav")}
	if _, err = voz.EnviarVoz(ctx, uuid.Nil, otroAudio, clave); !errors.Is(err, ErrClaveTextoEnConflicto) || asr != 1 {
		t.Fatal(err)
	}
	var persisted string
	var soloVoz bool
	if err := admin.QueryRow(ctx, "SELECT string_agg(contenido||metadatos::text,' '),bool_and(canal='voz') FROM asistente.mensaje WHERE conversacion_id=$1", conv).Scan(&persisted, &soloVoz); err != nil || !soloVoz {
		t.Fatalf("canal incorrecto: %v", err)
	}
	if strings.Contains(persisted, base64.StdEncoding.EncodeToString(a.Datos)[:40]) || strings.Contains(persisted, "MIME") {
		t.Fatal("se persistió audio")
	}
	var count int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM asistente.accion_pendiente WHERE conversacion_id=$1", conv).Scan(&count); err != nil || count != 1 {
		t.Fatalf("acciones=%d %v", count, err)
	}
	for _, cambiarParte := range []bool{true, false} {
		id, _ := identidad.DeContexto(ctx)
		if cambiarParte {
			id.ParteID = uuid.NewString()
		} else {
			id.UsuarioID = uuid.NewString()
		}
		if _, err := voz.EnviarVoz(identidad.ConIdentidad(ctx, id), conv, a, uuid.New()); !errors.Is(err, ErrConversacionTextoNoEncontrada) || asr != 1 {
			t.Fatal("conversación ajena llegó a ASR", err)
		}
	}
}

func TestVozDBReintentoConConversacionConocida(t *testing.T) {
	asr := 0
	fallar := true
	p := proveedorCicloPrueba{generar: func(_ context.Context, s SolicitudIA) (RespuestaIA, error) {
		if s.Historial[0].Partes[0].Audio != nil {
			asr++
			return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: `{"transcripcion":"Consulta mi saldo"}`}}}}, nil
		}
		if fallar {
			return RespuestaIA{}, ErrNoDisponibleIA
		}
		return respuestaTextoDB(), nil
	}}
	_, _, ctx, texto := prepararTextoDB(t, p, nil)
	voz := NuevoServicioVoz(texto, p)
	a := &AudioIA{MIME: "audio/wav", Datos: clipPrueba(t, "tono.wav")}
	r, err := voz.EnviarVoz(ctx, uuid.Nil, a, uuid.Nil)
	if err == nil {
		t.Fatal("faltó fallo")
	}
	user, conv := r.MensajeUsuarioID, r.ConversacionID
	fallar = false
	r, err = voz.EnviarVoz(ctx, conv, a, uuid.Nil)
	if err != nil || r.MensajeUsuarioID != user || asr != 1 || r.Transcripcion != "Consulta mi saldo" {
		t.Fatalf("reintento: %+v asr=%d %v", r, asr, err)
	}
}

func TestVozDBSinVozFalloYReferenciasAjenas(t *testing.T) {
	llamadas := 0
	fallo := error(nil)
	raw := `{"transcripcion":""}`
	p := proveedorCicloPrueba{generar: func(context.Context, SolicitudIA) (RespuestaIA, error) {
		llamadas++
		return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: raw}}}}, fallo
	}}
	admin, _, ctx, texto := prepararTextoDB(t, p, nil)
	voz := NuevoServicioVoz(texto, p)
	a := &AudioIA{MIME: "audio/wav", Datos: clipPrueba(t, "silencio.wav")}
	if _, err := voz.EnviarVoz(ctx, uuid.Nil, a, uuid.New()); !errors.Is(err, ErrAudioNoEntendido) {
		t.Fatal(err)
	}
	fallo = ErrTimeoutIA
	if _, err := voz.EnviarVoz(ctx, uuid.Nil, a, uuid.New()); !errors.Is(err, ErrTimeoutIA) {
		t.Fatal(err)
	}
	if _, err := voz.EnviarVoz(ctx, uuid.New(), a, uuid.New()); !errors.Is(err, ErrConversacionTextoNoEncontrada) || llamadas != 2 {
		t.Fatal("referencia ajena llegó al proveedor", err)
	}
	id, _ := identidad.DeContexto(ctx)
	var count int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM asistente.conversacion WHERE usuario_id=$1", id.ParteID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("creó turno sin transcripción: %d %v", count, err)
	}
}
