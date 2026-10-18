package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Un reenvío del mismo desenlace no crea un segundo evento ni cambia el CUS.
func TestSimuladorPSE_ReenvioDelMismoDesenlaceReusaElEventoYElCUS(t *testing.T) {
	almacen := almacenDePrueba(t)
	pagina := NewPaginaPSE(NuevoDespachador(almacen, &enviadorFalso{}, &enviadorFalso{}))
	form := url.Values{"referencia": {"dep-abc"}, "monto": {"150000"}}

	for intento := 1; intento <= 2; intento++ {
		rec := httptest.NewRecorder()
		pagina.Rutas().ServeHTTP(rec, peticionPSE(t, http.MethodPost, "/pse/autorizar", form))
		if rec.Code != http.StatusOK {
			t.Fatalf("intento %d: status = %d, quiero 200", intento, rec.Code)
		}
	}

	var envios, patas int
	var cus string
	if err := almacen.db.QueryRow(`SELECT COUNT(*) FROM envio`).Scan(&envios); err != nil {
		t.Fatalf("contar envíos: %v", err)
	}
	if err := almacen.db.QueryRow(`SELECT COUNT(*) FROM pata`).Scan(&patas); err != nil {
		t.Fatalf("contar patas: %v", err)
	}
	if err := almacen.db.QueryRow(`SELECT cus FROM envio`).Scan(&cus); err != nil {
		t.Fatalf("leer el CUS: %v", err)
	}
	if envios != 1 {
		t.Errorf("envíos = %d, quiero 1", envios)
	}
	if patas != 2 {
		t.Errorf("patas = %d, quiero 2", patas)
	}
	if cus == "" {
		t.Error("el CUS quedó vacío")
	}
}

// Un desenlace contrario sobre la misma referencia y monto responde conflicto y no asienta nada.
func TestSimuladorPSE_DesenlaceContrarioDa409SinSegundoEvento(t *testing.T) {
	almacen := almacenDePrueba(t)
	pagina := NewPaginaPSE(NuevoDespachador(almacen, &enviadorFalso{}, &enviadorFalso{}))
	form := url.Values{"referencia": {"dep-abc"}, "monto": {"150000"}}

	rec := httptest.NewRecorder()
	pagina.Rutas().ServeHTTP(rec, peticionPSE(t, http.MethodPost, "/pse/autorizar", form))
	if rec.Code != http.StatusOK {
		t.Fatalf("autorizar: status = %d, quiero 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	pagina.Rutas().ServeHTTP(rec, peticionPSE(t, http.MethodPost, "/pse/cancelar", form))
	if rec.Code != http.StatusConflict {
		t.Fatalf("cancelar la misma referencia: status = %d, quiero 409", rec.Code)
	}

	var envios int
	var estado string
	if err := almacen.db.QueryRow(`SELECT COUNT(*) FROM envio`).Scan(&envios); err != nil {
		t.Fatalf("contar envíos: %v", err)
	}
	if err := almacen.db.QueryRow(`SELECT estado FROM envio`).Scan(&estado); err != nil {
		t.Fatalf("leer el estado: %v", err)
	}
	if envios != 1 {
		t.Errorf("envíos = %d, quiero 1", envios)
	}
	if estado != estadoAprobado {
		t.Errorf("estado = %q, quiero %q", estado, estadoAprobado)
	}
}
