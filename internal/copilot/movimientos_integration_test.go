//go:build integration

package copilot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

func TestMovimientosPeriodoLedgerReal(t *testing.T) {
	admin, app := poolPersistencia(t, "DATABASE_URL"), poolPersistencia(t, "APP_DATABASE_URL")
	ctx := t.Context()
	s := nucleo.NewServicio(app, nil, nil, nil, nil)
	usuario, laura, luis, tercero := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []string{usuario, laura, luis, tercero} {
		if _, err := s.AbrirSubcuentasDeUsuario(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	for id, nombre := range map[string]string{laura: "Laura Rodriguez", luis: "Luis Ramirez"} {
		exec("INSERT INTO auth.partes(id,tipo) VALUES ($1,'persona')", id)
		exec("INSERT INTO auth.personas(parte_id,tipo_documento,documento,nombre) VALUES ($1,'CC',$2,$3)", id, uuid.NewString(), nombre)
	}
	fecha := func(texto string) time.Time {
		t.Helper()
		f, err := time.Parse(time.RFC3339, texto)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	tr := func(emisor, destino string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		exec("INSERT INTO nucleo.transferencia(id,usuario_id,cuenta_origen,llave_destino,monto,moneda,idempotency_key,estado) VALUES ($1,$2,$3,$4,500,'COP',$5,'confirmada')", id, emisor, nucleo.NombreCuentaDisponible(emisor), destino, uuid.NewString())
		return id
	}
	type linea struct {
		cuenta string
		monto  nucleo.Centavos
	}
	asiento := func(tipo string, cuando time.Time, transferencia *uuid.UUID, lineas ...linea) {
		t.Helper()
		tx, err := admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		var id int64
		if err := tx.QueryRow(ctx, "INSERT INTO nucleo.journal_entry(tipo,transferencia_id,ocurrido_en) VALUES ($1,$2,$3) RETURNING id", tipo, transferencia, cuando).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, l := range lineas {
			if _, err := tx.Exec(ctx, "INSERT INTO nucleo.posting(entry_id,cuenta_id,monto,moneda,ocurrido_en) VALUES ($1,$2,$3,'COP','2000-01-01')", id, l.cuenta, l.monto.Int64()); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	deposito := func(u string, f time.Time, m nucleo.Centavos) {
		asiento("deposito", f, nil, linea{nucleo.NombreCuentaDisponible(u), -m}, linea{nucleo.CuentaCustodiaBanco, m})
	}
	propio, retenido := nucleo.NombreCuentaDisponible(usuario), nucleo.NombreCuentaRetenido(usuario)
	deposito(usuario, fecha("2026-10-01T04:59:59Z"), 999999)
	deposito(usuario, fecha("2026-10-01T05:00:00Z"), 10000)
	envio := tr(usuario, "+573001234567")
	asiento("retencion", fecha("2026-10-01T12:00:00Z"), &envio, linea{propio, 500}, linea{retenido, -500}, linea{propio, 2}, linea{nucleo.CuentaGMFPorPagar, -2})
	asiento("transferencia_interna", fecha("2026-10-01T12:01:00Z"), &envio, linea{retenido, 500}, linea{nucleo.NombreCuentaDisponible(laura), -500})
	devolucion := tr(usuario, laura)
	asiento("compensacion", fecha("2026-10-02T12:00:00Z"), &devolucion, linea{propio, -100}, linea{nucleo.CuentaLiquidacionPendienteRiel, 100}, linea{propio, -1}, linea{nucleo.CuentaGMFPorPagar, 1})
	recibido := tr(luis, usuario)
	asiento("transferencia_interna", fecha("2026-10-02T15:00:00Z"), &recibido, linea{nucleo.NombreCuentaRetenido(luis), 300}, linea{propio, -300})
	deposito(usuario, fecha("2026-10-03T04:59:59Z"), 200)
	deposito(usuario, fecha("2026-10-03T05:00:00Z"), 999999)
	deposito(tercero, fecha("2026-10-02T15:00:00Z"), 7777777)
	var fechaIncorrecta int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM nucleo.posting p JOIN nucleo.journal_entry j ON j.id=p.entry_id WHERE p.ocurrido_en IS DISTINCT FROM j.ocurrido_en").Scan(&fechaIncorrecta); err != nil || fechaIncorrecta != 0 {
		t.Fatalf("fecha manipulable: %d %v", fechaIncorrecta, err)
	}
	resolutor := auth.NuevoResolutorDestino(app, time.Hour)
	c, err := NuevoCatalogo(map[string]ManejadorHerramienta{"listar_movimientos": ConsultaMovimientosIA(s, resolutor), "resumir_movimientos": ResumenMovimientosIA(s, resolutor)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx = identidad.ConIdentidad(ctx, identidad.Identidad{UsuarioID: usuario, ParteID: uuid.NewString()})
	llamar := func(nombre, args string) json.RawMessage {
		t.Helper()
		r, err := c.Ejecutar(ctx, LlamadaIA{Nombre: nombre, Argumentos: []byte(args)})
		if err != nil {
			t.Fatal(err)
		}
		for _, secreto := range []string{usuario, laura, luis, tercero, "Laura Rodriguez", "Luis Ramirez", "+573001234567"} {
			if strings.Contains(string(r.Datos), secreto) {
				t.Fatalf("se filtró %s", secreto)
			}
		}
		return r.Datos
	}
	var filasAntes int64
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM nucleo.posting").Scan(&filasAntes); err != nil {
		t.Fatal(err)
	}
	raw := llamar("listar_movimientos", `{"desde":"2026-10-01","hasta":"2026-10-02"}`)
	var lista struct {
		Datos struct {
			Total       int64 `json:"total"`
			HayMas      bool  `json:"hay_mas"`
			Movimientos []struct {
				Fecha       string `json:"fecha"`
				Motivo      string `json:"motivo"`
				Contraparte string `json:"contraparte"`
				Monto       struct {
					Centavos nucleo.Centavos `json:"centavos"`
				} `json:"monto"`
				Componentes []json.RawMessage `json:"componentes"`
			} `json:"movimientos"`
		} `json:"datos"`
	}
	if err := json.Unmarshal(raw, &lista); err != nil {
		t.Fatal(err)
	}
	if lista.Datos.Total != 5 || len(lista.Datos.Movimientos) != 5 || lista.Datos.HayMas || lista.Datos.Movimientos[0].Monto.Centavos != 200 || lista.Datos.Movimientos[3].Monto.Centavos != -502 || len(lista.Datos.Movimientos[3].Componentes) != 2 || lista.Datos.Movimientos[3].Contraparte != "L*** R***" {
		t.Fatalf("asientos/fechas/contraparte: %s", raw)
	}
	for _, m := range lista.Datos.Movimientos {
		if !strings.HasSuffix(m.Fecha, "-05:00") {
			t.Fatal(m.Fecha)
		}
	}
	type resumen struct {
		Datos struct {
			Entradas, Salidas, Neto struct {
				Centavos nucleo.Centavos `json:"centavos"`
			}
			Cantidad int `json:"cantidad"`
			Grupos   []struct {
				Grupo    string `json:"grupo"`
				Cantidad int    `json:"cantidad"`
				Total    struct {
					Centavos nucleo.Centavos `json:"centavos"`
				} `json:"total"`
			} `json:"grupos"`
		} `json:"datos"`
	}
	for _, agrupar := range []string{"motivo", "contraparte"} {
		raw := llamar("resumir_movimientos", `{"desde":"2026-10-01","hasta":"2026-10-02","agrupar_por":"`+agrupar+`"}`)
		var r resumen
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		if r.Datos.Entradas.Centavos != 10601 || r.Datos.Salidas.Centavos != 502 || r.Datos.Neto.Centavos != 10099 || r.Datos.Cantidad != 5 {
			t.Fatalf("sumas: %s", raw)
		}
		for i := 1; i < len(r.Datos.Grupos); i++ {
			if r.Datos.Grupos[i-1].Total.Centavos < r.Datos.Grupos[i].Total.Centavos {
				t.Fatal("orden incorrecto")
			}
		}
		if agrupar == "motivo" && len(r.Datos.Grupos) != 6 {
			t.Fatal("motivos perdidos")
		}
		if agrupar == "contraparte" && (len(r.Datos.Grupos) != 3 || r.Datos.Grupos[1].Grupo != r.Datos.Grupos[2].Grupo || r.Datos.Grupos[1].Total.Centavos != 603 || r.Datos.Grupos[2].Total.Centavos != 300) {
			t.Fatalf("contrapartes homónimas fusionadas: %s", raw)
		}
	}
	raw = llamar("listar_movimientos", `{"desde":"2026-10-01","hasta":"2026-10-02","tipo":"gravamen"}`)
	if err := json.Unmarshal(raw, &lista); err != nil {
		t.Fatal(err)
	}
	if lista.Datos.Total != 1 || len(lista.Datos.Movimientos) != 1 || lista.Datos.Movimientos[0].Monto.Centavos != -2 {
		t.Fatal(string(raw))
	}
	raw = llamar("listar_movimientos", `{"desde":"2026-09-01","hasta":"2026-09-01"}`)
	if !strings.Contains(string(raw), `"movimientos":[]`) {
		t.Fatal(string(raw))
	}
	raw = llamar("resumir_movimientos", `{"desde":"2026-09-01","hasta":"2026-09-01","agrupar_por":"motivo"}`)
	if !strings.Contains(string(raw), `"grupos":[]`) || !strings.Contains(string(raw), `"cantidad":0`) {
		t.Fatal(string(raw))
	}
	var filasDespues int64
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM nucleo.posting").Scan(&filasDespues); err != nil || filasAntes != filasDespues {
		t.Fatal("la lectura modificó el ledger")
	}
	// Más de una página SQL, empates de fecha y conteo completo pese al límite visible.
	for range 501 {
		deposito(usuario, fecha("2026-10-06T12:00:00Z"), 1)
	}
	raw = llamar("listar_movimientos", `{"desde":"2026-10-06","hasta":"2026-10-06"}`)
	if err := json.Unmarshal(raw, &lista); err != nil {
		t.Fatal(err)
	}
	if lista.Datos.Total != 501 || len(lista.Datos.Movimientos) != 20 || !lista.Datos.HayMas {
		t.Fatalf("paginación: %s", raw)
	}
	raw = llamar("resumir_movimientos", `{"desde":"2026-10-06","hasta":"2026-10-06","agrupar_por":"motivo"}`)
	var completo resumen
	if err := json.Unmarshal(raw, &completo); err != nil {
		t.Fatal(err)
	}
	if completo.Datos.Cantidad != 501 || completo.Datos.Entradas.Centavos != 501 {
		t.Fatal(string(raw))
	}
}
