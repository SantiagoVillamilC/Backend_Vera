package avisos

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const huellaVectoresAvisos = "926ca712b8ec6ba6bb00f955aa5c5f1d8e7384e697bccfb9f92119cde0160faf"

type documentoDeVectoresDeAvisos struct {
	Version    int                 `json:"version"`
	Grado      string              `json:"grado"`
	Escenarios []string            `json:"escenarios"`
	Casos      []casoDeVectorAviso `json:"casos"`
}

type casoDeVectorAviso struct {
	ID                 string  `json:"id"`
	Escenario          string  `json:"escenario"`
	Tipo               Tipo    `json:"tipo"`
	Clave              string  `json:"clave"`
	Titulo             string  `json:"titulo"`
	Texto              string  `json:"texto"`
	Porque             string  `json:"porque"`
	Accion             string  `json:"accion"`
	MontoCentavos      int64   `json:"monto_centavos"`
	ReferenciaCentavos *int64  `json:"referencia_centavos"`
	Fecha              *string `json:"fecha"`
	Mes                *string `json:"mes"`
	Periodo            string  `json:"periodo"`
}

func cargarVectoresDeAvisos(t *testing.T) documentoDeVectoresDeAvisos {
	t.Helper()
	ruta := filepath.Join("testdata", "vectores-avisos.json")
	datos, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("leer vectores de avisos: %v", err)
	}
	suma := sha256.Sum256(datos)
	if hex.EncodeToString(suma[:]) != huellaVectoresAvisos {
		t.Fatalf("el archivo de vectores de avisos cambió: sha256 = %s", hex.EncodeToString(suma[:]))
	}
	var documento documentoDeVectoresDeAvisos
	if err := json.Unmarshal(datos, &documento); err != nil {
		t.Fatalf("decodificar vectores de avisos: %v", err)
	}
	return documento
}

func TestLosVectoresDeAvisosCoincidenConLaReferencia(t *testing.T) {
	documento := cargarVectoresDeAvisos(t)
	if len(documento.Casos) != 7 {
		t.Fatalf("casos = %d, want 7", len(documento.Casos))
	}
	if documento.Grado != string(ReglaViva)+"/"+string(LineaBaseViva) {
		t.Fatalf("grado = %q, want %q", documento.Grado, string(ReglaViva)+"/"+string(LineaBaseViva))
	}
	escenarios := map[string]func() ([]Pago, []Ingreso){
		"presupuesto_anticipado": escenarioPresupuesto,
		"categoria_sobre_media":  escenarioCategoria,
		"caida_ingreso":          escenarioCaida,
		"pago_no_alcanza":        escenarioPago,
		"cobro_nuevo":            escenarioCobro,
	}
	for _, escenario := range documento.Escenarios {
		constructor, existe := escenarios[escenario]
		if !existe {
			t.Fatalf("el documento declara el escenario %q, que no está en las pruebas", escenario)
		}
		pagos, ingresos := constructor()
		obtenidas := map[Tipo]Tarjeta{}
		for _, tarjeta := range tarjetasDeEscenario(t, pagos, ingresos, 0) {
			obtenidas[tarjeta.Tipo] = tarjeta
		}
		for _, caso := range documento.Casos {
			if caso.Escenario != escenario {
				continue
			}
			tarjeta, existe := obtenidas[caso.Tipo]
			if !existe {
				t.Fatalf("%s: falta la tarjeta %s", caso.ID, caso.Tipo)
			}
			if tarjeta.Texto != caso.Texto || tarjeta.Titulo != caso.Titulo ||
				tarjeta.Porque != caso.Porque || tarjeta.Accion != caso.Accion {
				t.Fatalf("%s: la tarjeta no coincide\nobtenida: %+v\nesperada: %+v", caso.ID, tarjeta, caso)
			}
			if tarjeta.MontoCentavos != caso.MontoCentavos {
				t.Fatalf("%s: monto = %d, want %d", caso.ID, tarjeta.MontoCentavos, caso.MontoCentavos)
			}
			if (tarjeta.ReferenciaCentavos == nil) != (caso.ReferenciaCentavos == nil) {
				t.Fatalf("%s: la referencia no coincide", caso.ID)
			}
			if caso.ReferenciaCentavos != nil && *tarjeta.ReferenciaCentavos != *caso.ReferenciaCentavos {
				t.Fatalf("%s: referencia = %d, want %d", caso.ID, *tarjeta.ReferenciaCentavos, *caso.ReferenciaCentavos)
			}
			if tarjeta.Periodo.Format("2006-01-02") != caso.Periodo {
				t.Fatalf("%s: periodo = %s, want %s", caso.ID, tarjeta.Periodo.Format("2006-01-02"), caso.Periodo)
			}
			if reproduce, motivo := VerificarTarjeta(tarjeta); !reproduce {
				t.Fatalf("%s: no verifica: %s", caso.ID, motivo)
			}
		}
	}
}
