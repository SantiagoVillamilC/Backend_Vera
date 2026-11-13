package main

import (
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
)

func TestGuardaDSN(t *testing.T) {
	valido := "postgres://demo:clave@127.0.0.1:55437/vera_semilla_test?sslmode=disable"
	if e := validarDSN(valido); e != nil {
		t.Fatal(e)
	}
	for _, dsn := range []string{"", "postgres://demo@127.0.0.1:5432/vera_semilla_test", "postgres://demo@servidor:55437/vera_semilla_test", "postgres://demo@127.0.0.1:55437/produccion", valido + "&host=remoto", valido + "&dbname=produccion", valido + "&service=produccion"} {
		if validarDSN(dsn) == nil {
			t.Fatal("aceptó", dsn)
		}
	}
}

func TestPlanSaldoYPeriodos(t *testing.T) {
	for mes := time.January; mes <= time.December; mes++ {
		ahora := time.Date(2026, mes, 19, 12, 0, 0, 0, time.FixedZone("Bogota", -18000))
		movimientos := plan(ahora)
		saldo := int64(0)
		enel, internet, compra := 0, 0, 0
		for _, m := range movimientos {
			if m.fecha.After(ahora) || m.centavos == 0 {
				t.Fatal("movimiento inválido", m)
			}
			saldo += m.centavos
			if m.nombre == "ENEL" {
				enel++
			}
			if m.nombre == "INTERNET HOGAR" {
				internet++
			}
			if m.nombre == "TECNOLOGIA DEMO" {
				compra++
			}
		}
		if len(movimientos) != 52 || saldo != 970000 || enel < 3 || internet < 3 || compra != 1 {
			t.Fatalf("mes=%d movimientos=%d saldo=%d ENEL=%d INTERNET=%d compra=%d", mes, len(movimientos), saldo, enel, internet, compra)
		}
	}
}

func TestCupoDesdeEntorno(t *testing.T) {
	t.Parallel()
	if cupo, e := cupoDesdeEntorno(""); e != nil || cupo != avisos.CupoMensualPorDefecto {
		t.Fatalf("vacío dio (%d, %v), quiero (%d, nil)", cupo, e, avisos.CupoMensualPorDefecto)
	}
	if cupo, e := cupoDesdeEntorno("5"); e != nil || cupo != 5 {
		t.Fatalf("5 dio (%d, %v), quiero (5, nil)", cupo, e)
	}
	for _, valor := range []string{"0", "-1", "tres", "5.5", " 5"} {
		t.Run(valor, func(t *testing.T) {
			t.Parallel()
			if _, e := cupoDesdeEntorno(valor); e == nil || !strings.Contains(e.Error(), "AVISOS_CUPO") {
				t.Fatalf("%q dio err = %v, quiero un error de AVISOS_CUPO", valor, e)
			}
		})
	}
}

func TestCumpleAceptacionExigeCadaFamilia(t *testing.T) {
	completo := estado{
		Asientos:   make([]int64, 52),
		Series:     7,
		Categorias: map[string]int{"sin_categoria": 13, "comisiones_costos_financieros": 8, "alimentos_mercado": 6, "transporte": 6, "restaurantes_domicilios": 6, "salud": 6, "entretenimiento": 6, "compras": 1},
		Origenes:   map[string]int{"lexico": 39, "sin_categoria": 13},
		Avisos:     map[string]int{"caida_ingreso": 1, "cobro_nuevo": 1, "pago_no_alcanza": 1, "presupuesto_anticipado": 1},
	}
	if !cumpleAceptacion(completo) {
		t.Fatal("el escenario completo no cumple")
	}
	for _, tipo := range []string{"caida_ingreso", "cobro_nuevo", "pago_no_alcanza", "presupuesto_anticipado"} {
		sinFamilia := completo
		sinFamilia.Avisos = map[string]int{}
		for k, v := range completo.Avisos {
			if k != tipo {
				sinFamilia.Avisos[k] = v
			}
		}
		if cumpleAceptacion(sinFamilia) {
			t.Fatalf("cumple con un aviso de menos: %s", tipo)
		}
	}
	sinAsientos := completo
	sinAsientos.Asientos = completo.Asientos[:51]
	sinSeries := completo
	sinSeries.Series = 6
	otraReparticion := completo
	otraReparticion.Categorias = map[string]int{"sin_categoria": 14, "comisiones_costos_financieros": 7, "alimentos_mercado": 6, "transporte": 6, "restaurantes_domicilios": 6, "salud": 6, "entretenimiento": 6, "compras": 1}
	otroOrigen := completo
	otroOrigen.Origenes = map[string]int{"lexico": 38, "sin_categoria": 14}
	if cumpleAceptacion(sinAsientos) || cumpleAceptacion(sinSeries) || cumpleAceptacion(otraReparticion) || cumpleAceptacion(otroOrigen) {
		t.Fatal("acepta un escenario con otros conteos")
	}
}
