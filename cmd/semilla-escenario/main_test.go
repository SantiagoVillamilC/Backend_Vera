package main

import (
	"testing"
	"time"
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
		saldo := int64(0)
		enel, internet := 0, 0
		for _, m := range plan(ahora) {
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
		}
		if saldo != 2970000 || enel < 3 || internet < 3 {
			t.Fatalf("mes=%d saldo=%d ENEL=%d INTERNET=%d", mes, saldo, enel, internet)
		}
	}
}
