package evaluacion

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func respuestaRecuperada() copilot.RespuestaIA {
	return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: "Hola, puedo ayudarte con tu billetera."}}}}
}

func TestRecuperacionCuentaIntentosYNoReintentaBloqueos(t *testing.T) {
	for _, tc := range []struct {
		nombre   string
		fallo    error
		intentos int
	}{
		{"timeout", copilot.ErrTimeoutIA, 2}, {"servicio", copilot.ErrNoDisponibleIA, 2}, {"bloqueo", &copilot.ErrorBloqueo{Motivo: copilot.BloqueoSeguridad}, 1}, {"credencial", copilot.ErrCredencialesIA, 1},
	} {
		t.Run(tc.nombre, func(t *testing.T) {
			n := 0
			p := proveedorEvaluacionPrueba(func(_ context.Context, s copilot.SolicitudIA) (copilot.RespuestaIA, error) {
				n++
				if strings.Contains(s.Instrucciones, marcadorPrivado) {
					t.Error("envió marcador de evaluación al laboratorio")
				}
				if n == 1 {
					return copilot.RespuestaIA{}, tc.fallo
				}
				return respuestaRecuperada(), nil
			})
			c, err := NuevaConsola(configConsolaPrueba(), p, nil, time.Now, ConRecuperacion())
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Preguntar(t.Context(), "Hola")
			if n != tc.intentos {
				t.Fatal("intentos incorrectos", n)
			}
			usadas, _ := c.Consumo()
			if usadas != tc.intentos {
				t.Fatal("reintento sin contabilizar")
			}
			if tc.intentos == 2 && err != nil {
				t.Fatal(err)
			}
			if tc.intentos == 1 && (!errors.Is(err, tc.fallo) || len(c.memoria.historial) != 0) {
				t.Fatal("no conservó el fallo o dejó historial parcial", err)
			}
		})
	}
}

func TestCuotaTemporalNoSeReenviaYSeRecuperaSinReiniciar(t *testing.T) {
	ahora := time.Date(2026, 10, 20, 5, 0, 0, 0, time.UTC)
	n := 0
	p := proveedorEvaluacionPrueba(func(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error) {
		n++
		if n == 1 {
			return copilot.RespuestaIA{}, &copilot.ErrorLimiteIA{Clase: copilot.LimiteMinuto, ReintentarEn: 3 * time.Second}
		}
		return respuestaRecuperada(), nil
	})
	c, err := NuevaConsola(configConsolaPrueba(), p, nil, func() time.Time { return ahora }, ConRecuperacion())
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := c.Preguntar(t.Context(), "Hola"); !errors.Is(err, copilot.ErrLimiteIA) {
			t.Fatal(err)
		}
	}
	c.NuevaConversacion()
	chat, voz := c.Esperas()
	if chat != 3 || voz != 0 || n != 1 {
		t.Fatal("reinició cuota o volvió a llamar", chat, voz, n)
	}
	ahora = ahora.Add(4 * time.Second)
	if _, err := c.Preguntar(t.Context(), "Hola"); err != nil || n != 2 {
		t.Fatal("no recuperó la conversación", err, n)
	}
}

func TestCorteDiarioRespetaMedianochePacifico(t *testing.T) {
	var p pausaProveedor
	ahora := time.Date(2026, 10, 20, 5, 0, 0, 0, time.UTC)
	p.registrar(&copilot.ErrorLimiteIA{Clase: copilot.LimiteDia, ReintentarEn: 2 * time.Second}, ahora)
	if p.segundos(ahora) != 7200 {
		t.Fatal("no usó medianoche del Pacífico", p.hasta)
	}
	if p.comprobar(ahora.Add(3*time.Second)) == nil {
		t.Fatal("liberó cuota diaria como cuota por minuto")
	}
}

type sintetizadorRecuperacion struct {
	llamadas       int
	parcial, cuota bool
}

func (v *sintetizadorRecuperacion) Sintetizar(context.Context, string, string) ([]byte, error) {
	v.llamadas++
	if v.cuota {
		return nil, &copilot.ErrorLimiteIA{Clase: copilot.LimiteMinuto, ReintentarEn: time.Minute}
	}
	if v.llamadas == 1 {
		return nil, copilot.ErrNoDisponibleIA
	}
	return []byte("WAV simulado"), nil
}
func (v *sintetizadorRecuperacion) SintetizarFlujo(_ context.Context, _, _ string, recibir func([]byte) error) ([]byte, error) {
	v.llamadas++
	if v.parcial {
		if err := recibir([]byte{0, 0}); err != nil {
			return nil, err
		}
	}
	return nil, copilot.ErrNoDisponibleIA
}

func TestVozNoBloqueaChatNiRepiteAudioParcial(t *testing.T) {
	p := proveedorEvaluacionPrueba(func(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error) {
		return respuestaRecuperada(), nil
	})
	c, _ := NuevaConsola(configConsolaPrueba(), p, nil, time.Now, ConRecuperacion())
	v := &sintetizadorRecuperacion{cuota: true}
	for range 2 {
		if _, err := c.Sintetizar(t.Context(), v, "Hola", "Aoede"); !errors.Is(err, copilot.ErrLimiteIA) {
			t.Fatal(err)
		}
	}
	if v.llamadas != 1 {
		t.Fatal("reenvió voz durante el cooldown")
	}
	if _, err := c.Preguntar(t.Context(), "Hola"); err != nil {
		t.Fatal("la cuota TTS bloqueó el chat", err)
	}
	c, _ = NuevaConsola(configConsolaPrueba(), p, nil, time.Now, ConRecuperacion())
	v = &sintetizadorRecuperacion{parcial: true}
	_, err := c.SintetizarFlujo(t.Context(), v, "Hola", "Aoede", func([]byte) error { return nil })
	if err == nil || v.llamadas != 1 {
		t.Fatal("reintentó después de reproducir parte del audio")
	}
}

func TestRecuperacionNoSobrepasaPresupuestoNiCancelacion(t *testing.T) {
	n := 0
	p := proveedorEvaluacionPrueba(func(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error) {
		n++
		return copilot.RespuestaIA{}, copilot.ErrTimeoutIA
	})
	cfg := configConsolaPrueba()
	cfg.MaxSolicitudes = 1
	c, _ := NuevaConsola(cfg, p, nil, time.Now, ConRecuperacion())
	if _, err := c.Preguntar(t.Context(), "Hola"); !errors.Is(err, ErrCuotaEvaluacion) || n != 1 {
		t.Fatal(err, n)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := esperarRecuperacion(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
