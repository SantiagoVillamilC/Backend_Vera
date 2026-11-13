package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
)

type lectorConsumoFalso struct {
	resumen  inteligencia.ResumenDelMes
	err      error
	llamadas int
	usuario  string
	mes      string
	ahora    time.Time
}

func (l *lectorConsumoFalso) Resumen(_ context.Context, usuarioID, mes string, ahora time.Time) (inteligencia.ResumenDelMes, error) {
	l.llamadas++
	l.usuario, l.mes, l.ahora = usuarioID, mes, ahora
	return l.resumen, l.err
}

type vistaConsumo struct {
	Datos struct {
		Mes       string `json:"mes"`
		Alcance   string `json:"alcance"`
		Moneda    string `json:"moneda"`
		Evaluable bool   `json:"evaluable"`
		Total     struct {
			Centavos, Pesos string
		} `json:"total"`
		Categorias []struct {
			Categoria, Nombre string
			Total             struct {
				Centavos, Pesos string
			} `json:"total"`
			Mediana *struct {
				Centavos, Pesos string
			} `json:"mediana"`
		} `json:"categorias"`
		HayMas bool `json:"hay_mas"`
	} `json:"datos"`
}

func resumenDePrueba() inteligencia.ResumenDelMes {
	mediana := int64(4000000)
	return inteligencia.ResumenDelMes{
		Mes: "2026-09", Desde: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Hasta: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Alcance: "billetera", Moneda: "COP", Evaluable: true, MesesCompletos: 4, MesesRequeridos: 3,
		TotalCentavos: 7000000, CupoAvisos: avisos.CupoMensualPorDefecto, AvisosEmitidos: 2,
		Categorias: []inteligencia.ResumenDeCategoria{
			{Codigo: "alimentos_mercado", Nombre: "Alimentos y mercado", TotalCentavos: 5000000, MedianaCentavos: &mediana, MesesConGasto: 4},
			{Codigo: "transporte", Nombre: "Transporte", TotalCentavos: 2000000, MesesConGasto: 1},
		},
	}
}

func ejecutarConsumo(t *testing.T, lector LectorResumenConsumoIA, raw string) ResultadoIA {
	t.Helper()
	c := catalogoPrueba(t, map[string]ManejadorHerramienta{"resumir_consumo_por_categoria": ResumenConsumoIA(lector)})
	r, err := c.Ejecutar(contextoHerramienta(t), LlamadaIA{Nombre: "resumir_consumo_por_categoria", Argumentos: json.RawMessage(raw)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestConsumoUsaIdentidadYMesDelArgumento(t *testing.T) {
	lector := &lectorConsumoFalso{resumen: resumenDePrueba()}
	r := ejecutarConsumo(t, lector, `{"mes":"2026-09"}`)
	if codigoResultado(t, r) != "" {
		t.Fatalf("resultado=%s", r.Datos)
	}
	if lector.llamadas != 1 || lector.usuario != "usuario-autenticado" || lector.mes != "2026-09" || lector.ahora.IsZero() {
		t.Fatalf("puerto: llamadas=%d usuario=%q mes=%q ahora=%v", lector.llamadas, lector.usuario, lector.mes, lector.ahora)
	}
	var v vistaConsumo
	if err := json.Unmarshal(r.Datos, &v); err != nil {
		t.Fatal(err)
	}
	if v.Datos.Mes != "2026-09" || v.Datos.Moneda != "COP" || v.Datos.Alcance != "billetera" || !v.Datos.Evaluable {
		t.Fatalf("cabecera: %+v", v.Datos)
	}
	if v.Datos.Total.Centavos != "7000000" || v.Datos.Total.Pesos != "70000.00 COP" {
		t.Fatalf("total: %+v", v.Datos.Total)
	}
	if len(v.Datos.Categorias) != 2 || v.Datos.Categorias[0].Nombre != "Alimentos y mercado" || v.Datos.Categorias[0].Total.Pesos != "50000.00 COP" {
		t.Fatalf("categorías: %+v", v.Datos.Categorias)
	}
	if v.Datos.Categorias[0].Mediana == nil || v.Datos.Categorias[0].Mediana.Centavos != "4000000" {
		t.Fatalf("mediana evaluable: %+v", v.Datos.Categorias[0].Mediana)
	}
	if v.Datos.Categorias[1].Mediana != nil {
		t.Fatalf("mediana sin meses suficientes: %+v", v.Datos.Categorias[1].Mediana)
	}
	for _, ausente := range []string{"cupo_avisos", "avisos_emitidos", "usuario_id", "conversacion"} {
		if strings.Contains(string(r.Datos), ausente) {
			t.Fatalf("expone %s: %s", ausente, r.Datos)
		}
	}
}

func TestConsumoDelegaElMesVacio(t *testing.T) {
	lector := &lectorConsumoFalso{resumen: resumenDePrueba()}
	if r := ejecutarConsumo(t, lector, `{}`); codigoResultado(t, r) != "" {
		t.Fatalf("resultado=%s", r.Datos)
	}
	if lector.mes != "" {
		t.Fatalf("el manejador inventó el mes: %q", lector.mes)
	}
}

func TestConsumoRecortaYAnunciaCategoriasOmitidas(t *testing.T) {
	resumen := resumenDePrueba()
	resumen.Categorias = make([]inteligencia.ResumenDeCategoria, 12)
	for i := range resumen.Categorias {
		resumen.Categorias[i] = inteligencia.ResumenDeCategoria{Codigo: "transporte", Nombre: "Transporte", TotalCentavos: int64(1000 + i)}
	}
	lector := &lectorConsumoFalso{resumen: resumen}
	r := ejecutarConsumo(t, lector, `{"mes":"2026-09"}`)
	var v vistaConsumo
	if err := json.Unmarshal(r.Datos, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Datos.Categorias) != 10 || !v.Datos.HayMas {
		t.Fatalf("tope de categorías: %d hay_mas=%v", len(v.Datos.Categorias), v.Datos.HayMas)
	}
}

func TestConsumoTraduceErroresDelPuerto(t *testing.T) {
	for nombre, caso := range map[string]struct {
		lector LectorResumenConsumoIA
		codigo string
	}{
		"mes inválido":       {&lectorConsumoFalso{err: inteligencia.ErrMesInvalido}, "ValidationError"},
		"sin lector":         {nil, "FunctionUnavailable"},
		"lector nulo tipado": {(*inteligencia.Servicio)(nil), "FunctionUnavailable"},
		"base caída":         {&lectorConsumoFalso{err: errors.New("pool agotado")}, "FunctionUnavailable"},
	} {
		t.Run(nombre, func(t *testing.T) {
			r := ejecutarConsumo(t, caso.lector, `{"mes":"2026-09"}`)
			if codigo := codigoResultado(t, r); codigo != caso.codigo {
				t.Fatalf("codigo=%s want %s", codigo, caso.codigo)
			}
		})
	}
}

func TestConsumoRechazaMesMalFormadoAntesDelPuerto(t *testing.T) {
	lector := &lectorConsumoFalso{resumen: resumenDePrueba()}
	for _, raw := range []string{`{"mes":"2026-13"}`, `{"mes":"septiembre"}`, `{"mes":"2026-09-01"}`, `{"usuario_id":"otra"}`} {
		if codigo := codigoResultado(t, ejecutarConsumo(t, lector, raw)); codigo != "ValidationError" {
			t.Fatalf("%s aceptado como %q", raw, codigo)
		}
	}
	if lector.llamadas != 0 {
		t.Fatalf("llegó al puerto: %d", lector.llamadas)
	}
}

func TestConsumoEsLecturaSinPuertoDeDinero(t *testing.T) {
	c := catalogoPrueba(t, nil)
	if clase, ok := c.Clase("resumir_consumo_por_categoria"); !ok || clase != Lectura {
		t.Fatalf("clase=%v ok=%v", clase, ok)
	}
	for _, f := range c.Declaraciones() {
		for _, prohibido := range []string{"confirmar", "ejecutar", "transferir", "preparar"} {
			if strings.Contains(f.Nombre, prohibido) && f.Nombre != "preparar_transferencia" {
				t.Fatalf("%s parece una función de dinero", f.Nombre)
			}
		}
	}
}
