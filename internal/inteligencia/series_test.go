package inteligencia

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
)

func TestSeriesConsultablesProyectaElContexto(t *testing.T) {
	t.Parallel()

	centro := time.Date(2026, 9, 1, 0, 0, 0, 0, avisos.Zona)
	proximo := avisos.ProximoPago{Centro: centro, MontoCentro: 5_000_000}
	contexto := avisos.Contexto{
		Series: []avisos.Serie{{
			Identidad:  avisos.Identidad{Tipo: "llave", Valor: "L-ARRENDADOR"},
			Estado:     avisos.Madura,
			Cadencia:   "mensual",
			Indice:     avisos.Indice{CadenciaDias: 30},
			UltimoPago: time.Date(2026, 8, 1, 0, 0, 0, 0, avisos.Zona),
			Proximo:    &proximo,
		}, {
			Identidad:  avisos.Identidad{Tipo: "nombre", Valor: "TIENDA"},
			Estado:     avisos.Emergente,
			Cadencia:   "semanal",
			Indice:     avisos.Indice{CadenciaDias: 7},
			UltimoPago: time.Date(2026, 8, 25, 0, 0, 0, 0, avisos.Zona),
		}},
		Pagos: []avisos.Pago{
			{Identidad: avisos.Identidad{Tipo: "llave", Valor: "L-ARRENDADOR"}, OcurridoEn: time.Date(2026, 6, 1, 0, 0, 0, 0, avisos.Zona), Categoria: "alimentos_mercado"},
			{Identidad: avisos.Identidad{Tipo: "llave", Valor: "L-ARRENDADOR"}, OcurridoEn: time.Date(2026, 8, 1, 0, 0, 0, 0, avisos.Zona), Categoria: "vivienda_servicios"},
			{Identidad: avisos.Identidad{Tipo: "nombre", Valor: "TIENDA"}, OcurridoEn: time.Date(2026, 8, 25, 0, 0, 0, 0, avisos.Zona), Categoria: ""},
		},
	}

	series := seriesConsultables(contexto)
	if len(series) != 2 {
		t.Fatalf("series = %d, quiero 2", len(series))
	}
	primera := series[0]
	if primera.IdentidadTipo != "llave" || primera.IdentidadValor != "L-ARRENDADOR" {
		t.Errorf("identidad = %q/%q", primera.IdentidadTipo, primera.IdentidadValor)
	}
	if primera.Estado != string(avisos.Madura) || primera.Cadencia != "mensual" || primera.CadenciaDias != 30 {
		t.Errorf("estado/cadencia = %q/%q/%d", primera.Estado, primera.Cadencia, primera.CadenciaDias)
	}
	if primera.Categoria != "vivienda_servicios" {
		t.Errorf("categoría = %q, quiero la última pagada", primera.Categoria)
	}
	if primera.ProximoFecha == nil || !primera.ProximoFecha.Equal(centro) || primera.ProximoMonto == nil || *primera.ProximoMonto != 5_000_000 {
		t.Errorf("próximo = %v/%v", primera.ProximoFecha, primera.ProximoMonto)
	}
	segunda := series[1]
	if segunda.ProximoFecha != nil || segunda.ProximoMonto != nil {
		t.Errorf("la segunda serie no debería tener próximo pago")
	}
}

func TestListarSeriesDevuelveMontosComoTexto(t *testing.T) {
	t.Parallel()

	centro := time.Date(2026, 10, 1, 0, 0, 0, 0, zonaBogota)
	monto := int64(5_000_000)
	categoria := "alimentos_mercado"
	falso := &servicioDeLecturaFalso{series: []SerieConsultable{{
		IdentidadTipo:  "llave",
		IdentidadValor: "L-ARRENDADOR",
		Estado:         "madura",
		Cadencia:       "mensual",
		CadenciaDias:   30,
		UltimoPago:     time.Date(2026, 9, 1, 0, 0, 0, 0, zonaBogota),
		ProximoFecha:   &centro,
		ProximoMonto:   &monto,
		Categoria:      categoria,
	}}}

	rec := httptest.NewRecorder()
	req := peticionConIdentidad(t, "/v1/inteligencia/series")
	NuevoHandler(falso).ListarSeriesInteligencia(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d", rec.Code)
	}
	var sobre struct {
		Success bool `json:"success"`
		Data    []struct {
			Identidad            string  `json:"identidad"`
			CadenciaDias         int     `json:"cadencia_dias"`
			ProximoMontoCentavos *string `json:"proximo_monto_centavos"`
			Categoria            *string `json:"categoria"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sobre); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if !sobre.Success || len(sobre.Data) != 1 {
		t.Fatalf("sobre = %+v", sobre)
	}
	fila := sobre.Data[0]
	if fila.Identidad != "L-ARRENDADOR" || fila.CadenciaDias != 30 {
		t.Errorf("fila = %+v", fila)
	}
	if fila.ProximoMontoCentavos == nil || *fila.ProximoMontoCentavos != "5000000" {
		t.Errorf("proximo_monto_centavos = %v, quiero 5000000 como texto", fila.ProximoMontoCentavos)
	}
	if fila.Categoria == nil || *fila.Categoria != categoria {
		t.Errorf("categoria = %v", fila.Categoria)
	}
	if falso.seriesUsuario != usuarioDePrueba {
		t.Errorf("usuario = %q, quiero el de la identidad", falso.seriesUsuario)
	}
}
