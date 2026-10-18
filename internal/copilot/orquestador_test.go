package copilot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

type memoriaCicloPrueba struct{ sesion *sesionCicloPrueba }

func (m memoriaCicloPrueba) Abrir(context.Context, EntradaOrquestador) (SesionCiclo, error) {
	return m.sesion, nil
}

func TestOrquestadorLecturasConcurrentesConOrdenEstable(t *testing.T) {
	entradas := make(chan struct{}, 2)
	continuar := make(chan struct{})
	manejar := func(ctx context.Context, _ identidad.Identidad, _ ArgumentosHerramienta) (DatosHerramienta, error) {
		entradas <- struct{}{}
		select {
		case <-continuar:
			return SaldoHerramienta{Disponible: 100}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	vueltas := 0
	p := proveedorCicloPrueba{generar: func(_ context.Context, s SolicitudIA) (RespuestaIA, error) {
		vueltas++
		if vueltas == 1 {
			return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{
				{Llamada: &LlamadaIA{ID: "a", Nombre: "consultar_saldo", Argumentos: []byte(`{}`)}},
				{Llamada: &LlamadaIA{ID: "b", Nombre: "consultar_saldo", Argumentos: []byte(`{}`)}},
			}}}, nil
		}
		partes := s.Historial[len(s.Historial)-1].Partes
		if partes[0].Resultado.ID != "a" || partes[1].Resultado.ID != "b" {
			t.Error("se perdió correlación al paralelizar")
		}
		return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: "Resultado listo."}}}}, nil
	}}
	o, _, ctx := cicloPrueba(t, p, map[string]ManejadorHerramienta{"consultar_saldo": manejar})
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := o.Responder(ctx, EntradaOrquestador{ConversacionID: uuid.New(), MensajeID: uuid.New()})
		done <- err
	}()
	for range 2 {
		select {
		case <-entradas:
		case <-ctx.Done():
			t.Fatal("las lecturas no se ejecutaron en paralelo")
		}
	}
	close(continuar)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestOrquestadorAccionProvieneDelServidor(t *testing.T) {
	accion := uuid.New()
	vueltas := 0
	p := proveedorCicloPrueba{generar: func(context.Context, SolicitudIA) (RespuestaIA, error) {
		vueltas++
		if vueltas == 1 {
			return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Llamada: &LlamadaIA{Nombre: "preparar_transferencia", Argumentos: []byte(`{"celular":"+573001234567","monto_pesos":"100"}`)}}}}}, nil
		}
		return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: "Propuesta de 999 pesos, identificador inventado."}}}}, nil
	}}
	o, s, ctx := cicloPrueba(t, p, map[string]ManejadorHerramienta{"preparar_transferencia": func(context.Context, identidad.Identidad, ArgumentosHerramienta) (DatosHerramienta, error) {
		return AccionHerramienta{AccionID: accion, Destinatario: DestinatarioHerramienta{Nombre: "Laura Ruiz", Celular: "+573001234567"}, Monto: 10000, ExpiraEn: time.Now().Add(time.Minute)}, nil
	}})
	s.accion = &AccionOrquestador{ID: accion, Monto: nucleo.Centavos(10000), NombreMostrado: "Laura Ruiz", RequiereCodigo: true}
	r, err := o.Responder(ctx, EntradaOrquestador{ConversacionID: uuid.New(), MensajeID: uuid.New()})
	if err != nil || r.AccionPendiente == nil || r.AccionPendiente.ID != accion || r.AccionPendiente.Monto != 10000 || !r.AccionPendiente.RequiereCodigo {
		t.Fatalf("acción: %+v %v", r, err)
	}
}

func TestOrquestadorReanudaSinReejecutar(t *testing.T) {
	p := proveedorCicloPrueba{generar: func(_ context.Context, s SolicitudIA) (RespuestaIA, error) {
		if s.Historial[len(s.Historial)-1].Rol != "herramienta" {
			t.Error("se perdió contexto interno")
		}
		return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: "Respuesta recuperada."}}}}, nil
	}}
	o, s, ctx := cicloPrueba(t, p, map[string]ManejadorHerramienta{"consultar_saldo": func(context.Context, identidad.Identidad, ArgumentosHerramienta) (DatosHerramienta, error) {
		t.Error("función repetida")
		return nil, nil
	}})
	s.historial = append(s.historial, TurnoIA{Rol: "asistente", Partes: []ParteIA{{Llamada: &LlamadaIA{ID: "anterior", Nombre: "consultar_saldo", Argumentos: []byte(`{}`)}}}}, TurnoIA{Rol: "herramienta", Partes: []ParteIA{{Resultado: &ResultadoIA{ID: "anterior", Nombre: "consultar_saldo", Datos: []byte(`{"datos":{}}`)}}}})
	r, err := o.Responder(ctx, EntradaOrquestador{ConversacionID: uuid.New(), MensajeID: uuid.New()})
	if err != nil || r.Vueltas != 2 || r.Llamadas != 1 {
		t.Fatalf("reanudar: %+v %v", r, err)
	}
}

type sesionCicloPrueba struct {
	registros []RegistroCiclo
	cerrada   bool
	historial []TurnoIA
	accion    *AccionOrquestador
	fallo     error
}

func (s *sesionCicloPrueba) Historial() []TurnoIA { return s.historial }
func (s *sesionCicloPrueba) Guardar(_ context.Context, r RegistroCiclo) error {
	s.registros = append(s.registros, r)
	return s.fallo
}
func (s *sesionCicloPrueba) Accion(context.Context, uuid.UUID) (*AccionOrquestador, error) {
	return s.accion, s.fallo
}
func (s *sesionCicloPrueba) Cerrar(context.Context) error { s.cerrada = true; return nil }

type autorizacionCicloPrueba struct{ err error }

func (a autorizacionCicloPrueba) Exigir(context.Context) error { return a.err }

type proveedorCicloPrueba struct {
	generar func(context.Context, SolicitudIA) (RespuestaIA, error)
}

func (p proveedorCicloPrueba) Generar(ctx context.Context, s SolicitudIA) (RespuestaIA, error) {
	return p.generar(ctx, s)
}

func cicloPrueba(t *testing.T, p ProveedorIA, manejadores map[string]ManejadorHerramienta) (*Orquestador, *sesionCicloPrueba, context.Context) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalogo, err := NuevoCatalogo(manejadores, logger)
	if err != nil {
		t.Fatal(err)
	}
	sesion := &sesionCicloPrueba{historial: []TurnoIA{{Rol: "usuario", Partes: []ParteIA{{Texto: "mi consulta"}}}}}
	o, err := NuevoOrquestador(p, catalogo, memoriaCicloPrueba{sesion}, autorizacionCicloPrueba{}, "modelo-prueba", logger)
	if err != nil {
		t.Fatal(err)
	}
	o.ahora = func() time.Time { return time.Date(2026, 10, 18, 5, 30, 0, 0, time.UTC) }
	ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()})
	return o, sesion, ctx
}

func TestOrquestadorRespuestaDirecta(t *testing.T) {
	p := proveedorCicloPrueba{generar: func(_ context.Context, s SolicitudIA) (RespuestaIA, error) {
		if !strings.Contains(s.Instrucciones, "2026-10-18T00:30:00-05:00") || !strings.Contains(s.Instrucciones, "Vera") || len(s.Funciones) != 7 || s.Historial[0].Rol != "usuario" {
			t.Fatalf("solicitud incompleta: %+v", s)
		}
		return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: "Hola, ¿cómo te ayudo?"}}}, TokensEntrada: 10, TokensSalida: 5}, nil
	}}
	o, s, ctx := cicloPrueba(t, p, nil)
	r, err := o.Responder(ctx, EntradaOrquestador{ConversacionID: uuid.New(), MensajeID: uuid.New()})
	if err != nil || r.Texto != "Hola, ¿cómo te ayudo?" || r.VersionInstrucciones != VersionInstruccionesVera || r.Vueltas != 1 || r.Llamadas != 0 || r.TokensEntrada != 10 || r.TokensSalida != 5 || len(s.registros) != 1 || !s.registros[0].Final || !s.cerrada {
		t.Fatalf("respuesta: %+v %v", r, err)
	}
}

func TestOrquestadorFuncionYErrorComoDatos(t *testing.T) {
	vueltas := 0
	p := proveedorCicloPrueba{generar: func(_ context.Context, s SolicitudIA) (RespuestaIA, error) {
		vueltas++
		if vueltas == 1 {
			return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Llamada: &LlamadaIA{ID: "uno", Nombre: "consultar_saldo", Argumentos: []byte(`{}`)}, Firma: []byte("firma-opaca")}}}}, nil
		}
		if len(s.Historial) != 3 || s.Historial[1].Partes[0].Firma[0] != 'f' || s.Historial[2].Rol != "herramienta" || s.Historial[2].Partes[0].Resultado.ID != "uno" || !strings.Contains(string(s.Historial[2].Partes[0].Resultado.Datos), "NotFound") {
			t.Fatalf("protocolo: %+v", s.Historial)
		}
		if strings.Contains(s.Instrucciones, "NotFound") {
			t.Fatal("resultado elevado a instrucciones")
		}
		return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: "No encontré la información."}}}}, nil
	}}
	o, s, ctx := cicloPrueba(t, p, map[string]ManejadorHerramienta{"consultar_saldo": func(ctx context.Context, id identidad.Identidad, _ ArgumentosHerramienta) (DatosHerramienta, error) {
		if id.UsuarioID == "" {
			t.Fatal("sin identidad")
		}
		if _, ok := ConversacionDelContexto(ctx); !ok {
			t.Fatal("sin conversación")
		}
		return nil, ErrHerramientaNoEncontrado
	}})
	r, err := o.Responder(ctx, EntradaOrquestador{ConversacionID: uuid.New(), MensajeID: uuid.New()})
	if err != nil || r.Llamadas != 1 || r.Vueltas != 2 || len(s.registros) != 2 || len(s.registros[0].Resultados) != 1 {
		t.Fatalf("ciclo: %+v %v", r, err)
	}
}

func TestOrquestadorTopesYCancelacion(t *testing.T) {
	for _, caso := range []struct {
		nombre   string
		cantidad int
		vueltas  int
		llamadas int
	}{{"vueltas", 1, 5, 4}, {"llamadas", 3, 3, 6}, {"loteExcesivo", 9, 1, 0}} {
		t.Run(caso.nombre, func(t *testing.T) {
			p := proveedorCicloPrueba{generar: func(context.Context, SolicitudIA) (RespuestaIA, error) {
				partes := make([]ParteIA, caso.cantidad)
				for i := range partes {
					partes[i].Llamada = &LlamadaIA{Nombre: "consultar_saldo", Argumentos: []byte(`{}`)}
				}
				return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: partes}}, nil
			}}
			o, s, ctx := cicloPrueba(t, p, nil)
			r, err := o.Responder(ctx, EntradaOrquestador{ConversacionID: uuid.New(), MensajeID: uuid.New()})
			if err != nil || !r.Limitado || r.Vueltas != caso.vueltas || r.Llamadas != caso.llamadas || !s.registros[len(s.registros)-1].Final {
				t.Fatalf("tope: %+v %v", r, err)
			}
		})
	}
	p := proveedorCicloPrueba{generar: func(ctx context.Context, _ SolicitudIA) (RespuestaIA, error) {
		<-ctx.Done()
		return RespuestaIA{}, ctx.Err()
	}}
	o, s, ctx := cicloPrueba(t, p, nil)
	o.plazo = time.Millisecond
	if _, err := o.Responder(ctx, EntradaOrquestador{ConversacionID: uuid.New(), MensajeID: uuid.New()}); !errors.Is(err, context.DeadlineExceeded) || !s.cerrada {
		t.Fatal(err)
	}
}
