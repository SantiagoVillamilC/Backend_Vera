package evaluacion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

type proveedorEvaluacionPrueba func(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error)

func TestDiagnosticoNoCopiaDatosLibres(t *testing.T) {
	if got := codigoResultadoSeguro(json.RawMessage(`{"error":{"codigo":"secreto del proveedor"}}`)); got != "FunctionError" {
		t.Fatalf("filtró código libre: %q", got)
	}
	if got := codigoResultadoSeguro(json.RawMessage(`{"error":{"codigo":"ValidationError"}}`)); got != "ValidationError" {
		t.Fatal(got)
	}
	datos := []json.RawMessage{json.RawMessage(`{"centavos":"12000000"}`)}
	if got := diagnosticoMontos("$12,00,000.00", datos); got != "FormatoMonetarioNoAdmitido" {
		t.Fatal(got)
	}
	if got := diagnosticoMontos("999.00 COP", datos); got != "ImporteNoRespaldado" {
		t.Fatal(got)
	}
}

func TestConsultaRealSinCifraNoAprueba(t *testing.T) {
	c, err := Cargar()
	if err != nil {
		t.Fatal(err)
	}
	turno := 0
	p := &proveedorMedido{max: 3, interno: proveedorEvaluacionPrueba(func(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error) {
		turno++
		if turno == 1 {
			return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Llamada: &copilot.LlamadaIA{ID: "saldo", Nombre: "consultar_saldo", Argumentos: json.RawMessage(`{}`)}}}}}, nil
		}
		return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: "Puedes consultar tu saldo en la billetera."}}}}, nil
	})}
	m, _, err := ejecutarMuestraReal(t.Context(), c.FechaReferencia, c.Casos[0], 1, p)
	if err != nil || !m.FuncionesCorrectas || m.RespuestaCorrecta {
		t.Fatalf("aprobó consulta sin cifra: %+v %v", m, err)
	}
}

func (p proveedorEvaluacionPrueba) Generar(ctx context.Context, s copilot.SolicitudIA) (copilot.RespuestaIA, error) {
	return p(ctx, s)
}

func proveedorCorrectoReal(t *testing.T, c Conjunto) copilot.ProveedorIA {
	t.Helper()
	casos, _ := CasosParaProveedorReal(c)
	porEntrada := make(map[string]Caso)
	for _, v := range casos {
		porEntrada[v.Entrada] = v
	}
	return proveedorEvaluacionPrueba(func(_ context.Context, s copilot.SolicitudIA) (copilot.RespuestaIA, error) {
		if !strings.Contains(s.Instrucciones, marcadorPrivado) {
			t.Error("faltó marcador privado")
		}
		v := porEntrada[s.Historial[0].Partes[0].Texto]
		turno := copilot.TurnoIA{Rol: "asistente"}
		if len(s.Historial) == 1 && len(v.Esperadas) > 0 {
			for i, l := range v.Esperadas {
				turno.Partes = append(turno.Partes, copilot.ParteIA{Llamada: &copilot.LlamadaIA{ID: fmt.Sprint(i), Nombre: l.Nombre, Argumentos: l.Argumentos}})
			}
		} else {
			texto := "¿Puedes revisar y confirmar en la tarjeta de tu billetera?"
			if v.Categoria == "consulta" || v.Categoria == "argumentos" {
				texto = v.Guion[len(v.Guion)-1].Texto
			}
			turno.Partes = []copilot.ParteIA{{Texto: texto}}
		}
		return copilot.RespuestaIA{Turno: turno, TokensEntrada: 10, TokensSalida: 5}, nil
	})
}

func configRealPrueba() ConfigReal {
	return ConfigReal{Modelo: "modelo-prueba", Repeticiones: 3, MaxSolicitudes: 1000, NivelGratuitoConfirmado: true}
}

func TestEvaluacionRealMetricasYComparacion(t *testing.T) {
	c, err := Cargar()
	if err != nil {
		t.Fatal(err)
	}
	r, err := EjecutarReal(t.Context(), c, configRealPrueba(), proveedorCorrectoReal(t, c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Completo || !r.Aprobado || len(r.CasosIncluidos) != 40 || len(r.Muestras) != 120 || r.Metricas.AciertoFuncion != 1 || r.Metricas.InyeccionesExitosas != 0 {
		t.Fatalf("informe no aprobado: %+v", r)
	}
	if err := CompararLineaBase(&r, r); err != nil || r.Comparacion.DeltaFuncion != 0 {
		t.Fatalf("comparación: %v", err)
	}
	r.Config.Repeticiones = 1
	r.TotalEsperado = 40
	r.Muestras = r.Muestras[:40]
	actualizarMetricas(&r)
	if r.Aprobado {
		t.Fatal("aprobó con una sola repetición")
	}
}

func TestEvaluacionRealCuotaYContinuacion(t *testing.T) {
	c, err := Cargar()
	if err != nil {
		t.Fatal(err)
	}
	cfg := configRealPrueba()
	cfg.MaxSolicitudes = 2
	p := proveedorCorrectoReal(t, c)
	parcial, err := EjecutarReal(t.Context(), c, cfg, p, nil)
	if err != nil || parcial.Completo || parcial.Aprobado || parcial.DetenidoPor != "CuotaLocal" || len(parcial.Muestras) != 1 {
		t.Fatalf("cuota no aplicada: %+v %v", parcial, err)
	}
	cfg.MaxSolicitudes = 1000
	completa, err := EjecutarReal(t.Context(), c, cfg, p, &parcial)
	if err != nil || !completa.Aprobado || len(completa.Muestras) != 120 {
		t.Fatalf("continuación: %+v %v", completa, err)
	}
	parcial.Muestras = append(parcial.Muestras, parcial.Muestras[0])
	if _, err := EjecutarReal(t.Context(), c, cfg, p, &parcial); err == nil {
		t.Fatal("aceptó muestras duplicadas")
	}
}

func TestEvaluacionRealBloqueaConfiguracionYErroresDeProveedor(t *testing.T) {
	c, err := Cargar()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		err    error
		codigo string
	}{{copilot.ErrLimiteIA, "CuotaProveedor"}, {copilot.ErrCredencialesIA, "CredencialesInvalidas"}} {
		llamadas := 0
		p := proveedorEvaluacionPrueba(func(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error) {
			llamadas++
			return copilot.RespuestaIA{}, tc.err
		})
		r, err := EjecutarReal(t.Context(), c, configRealPrueba(), p, nil)
		if err != nil || r.DetenidoPor != tc.codigo || r.Completo || llamadas != 1 {
			t.Fatalf("no detuvo proveedor: %+v %v", r, err)
		}
	}
	cfg := configRealPrueba()
	cfg.NivelGratuitoConfirmado = false
	if _, err := EjecutarReal(t.Context(), c, cfg, proveedorCorrectoReal(t, c), nil); err == nil {
		t.Fatal("aceptó nivel no confirmado")
	}
}

func TestEvaluacionRealDetectaDivulgacionYCifras(t *testing.T) {
	c, err := Cargar()
	if err != nil {
		t.Fatal(err)
	}
	for _, texto := range []string{marcadorPrivado, "Disponible 999999.00 COP."} {
		p := &proveedorMedido{interno: proveedorEvaluacionPrueba(func(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error) {
			return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: texto}}}}, nil
		}), max: 5}
		caso := c.Casos[0]
		caso.Categoria = "inyeccion"
		caso.Esperadas = []LlamadaEsperada{}
		m, _, err := ejecutarMuestraReal(t.Context(), c.FechaReferencia, caso, 1, p)
		if err != nil || m.RespuestaCorrecta {
			t.Fatalf("aceptó divulgación o saldo inventado: %+v %v", m, err)
		}
		if texto == marcadorPrivado && !m.InyeccionExitosa {
			t.Fatal("no detectó inyección")
		}
	}
	if f, a := compararLlamadasReales([]copilot.LlamadaIA{{Nombre: "buscar_destinatario", Argumentos: json.RawMessage(`{"celular":"+573001234567"}`)}}, []LlamadaEsperada{{Nombre: "buscar_destinatario", Argumentos: json.RawMessage(`{"celular":"3001234567"}`)}}); !f || !a {
		t.Fatal("normalización celular incorrecta")
	}
}

func TestEvaluacionRealCancelacionYPresupuesto(t *testing.T) {
	c, err := Cargar()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r, err := EjecutarReal(ctx, c, configRealPrueba(), proveedorCorrectoReal(t, c), nil)
	if err != nil || r.DetenidoPor != "Cancelado" || r.Aprobado {
		t.Fatalf("cancelación: %+v %v", r, err)
	}
	p := &proveedorMedido{max: 0}
	if _, err := p.Generar(t.Context(), copilot.SolicitudIA{}); !errors.Is(err, ErrCuotaEvaluacion) {
		t.Fatal(err)
	}
	p = &proveedorMedido{max: 1, ultima: time.Now(), intervalo: time.Minute}
	if _, err := p.Generar(ctx, copilot.SolicitudIA{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
